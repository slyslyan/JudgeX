package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"judgex/internal/cache"
	"judgex/internal/database"
	"judgex/internal/model"
)

// ============================================================================
// 题目控制器
// ============================================================================
//
// 本文件实现了题目的 CRUD 操作和标签管理。
//
// 缓存策略：
// - 题目详情（Get）缓存 10 分钟，使用 singleflight 防缓存击穿
// - 空结果缓存 5 分钟，防止缓存穿透
// - 题目列表不缓存（数据变化频繁，且分页参数多）
//
// 标签系统：
// - 题目和标签是多对多关系（通过 problem_tag_links 表）
// - 支持按标签筛选和全文搜索
// - List 接口同时返回所有可用标签（用于前端筛选下拉框）

type ProblemHandler struct{}

func NewProblemHandler() *ProblemHandler {
	return &ProblemHandler{}
}

type problemReq struct {
	Title       string          `json:"title" binding:"required"`
	Description string          `json:"description"`
	TimeLimit   int             `json:"time_limit"`
	MemoryLimit int             `json:"memory_limit"`
	SampleCases json.RawMessage `json:"sample_cases"`
	Tags        []string        `json:"tags"`
	Number      *int            `json:"number"` // 题号，为 nil 时默认等于 ID
}

// ============================================================================
// 创建
// ============================================================================

// Create 处理 POST /api/problems。
// 创建题目，设置默认值（时间限制 1000ms，内存限制 128MB），同步标签。
func (h *ProblemHandler) Create(c *gin.Context) {
	var req problemReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.TimeLimit <= 0 {
		req.TimeLimit = 1000
	}
	if req.MemoryLimit <= 0 {
		req.MemoryLimit = 128
	}
	sampleCases := req.SampleCases
	if len(sampleCases) == 0 || string(sampleCases) == "null" {
		sampleCases = json.RawMessage("[]")
	}

	problem := model.Problem{
		Title:       req.Title,
		Description: req.Description,
		TimeLimit:   req.TimeLimit,
		MemoryLimit: req.MemoryLimit,
		SampleCases: sampleCases,
	}
	if err := database.DB.Create(&problem).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create problem"})
		return
	}

	// 设置题号（默认等于 ID，管理员可自定义）
	if req.Number != nil && *req.Number > 0 {
		database.DB.Model(&problem).Update("number", *req.Number)
	} else {
		database.DB.Model(&problem).Update("number", problem.ID)
	}

	// 同步标签
	syncProblemTags(problem.ID, req.Tags)

	cache.Del("problems:list")
	cache.AddProblemID(uint64(problem.ID)) // Bloom Filter 添加新 ID
	database.DB.Preload("Tags").First(&problem, problem.ID)
	c.JSON(http.StatusCreated, problem)
}

// ============================================================================
// 查询
// ============================================================================

// List 处理 GET /api/problems。
// 支持分页、全文搜索、标签筛选。
//
// 查询参数：
//   - page: 页码（默认 1）
//   - page_size: 每页数量（默认 20，最大 100）
//   - search: 搜索关键词（匹配标题）
//   - tag: 标签名筛选
//
// 返回结果包含：
//   - problems: 题目列表（含提交计数）
//   - total: 总数
//   - tags: 所有可用标签（供筛选下拉框使用）
func (h *ProblemHandler) List(c *gin.Context) {
	var problems []model.Problem
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	search := c.Query("search")
	tagFilter := c.Query("tag")

	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	var total int64
	q := database.DB.Model(&model.Problem{}).Preload("Tags")
	if search != "" {
		q = q.Where("title LIKE ?", "%"+search+"%")
	}
	if tagFilter != "" {
		// 通过 JOIN 筛选有指定标签的题目
		q = q.Joins("JOIN problem_tag_links ON problems.id = problem_tag_links.problem_id").
			Joins("JOIN problem_tags ON problem_tag_links.tag_id = problem_tags.id").
			Where("problem_tags.name = ?", tagFilter)
	}
	q.Count(&total)
	q.Order("number ASC").Offset(offset).Limit(pageSize).Find(&problems)

	// 为每个题目统计提交数
	type ProblemWithStats struct {
		model.Problem
		SubmissionCount int64 `json:"submission_count"`
	}
	result := make([]ProblemWithStats, 0, len(problems))
	for _, p := range problems {
		var count int64
		database.DB.Model(&model.Submission{}).Where("problem_id = ?", p.ID).Count(&count)
		result = append(result, ProblemWithStats{
			Problem:         p,
			SubmissionCount: count,
		})
	}

	// 返回所有标签（供前端筛选）
	var allTags []model.ProblemTag
	database.DB.Find(&allTags)

	c.JSON(http.StatusOK, gin.H{
		"problems":  result,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
		"tags":      allTags,
	})
}

// Get 处理 GET /api/problems/:id。
// 使用三级缓存防穿透策略：
// 1. 先查 Redis 缓存（正常缓存）
// 2. 如果缓存未命中，查"空值标记"（防止缓存穿透）
// 3. 如果都不存在，查数据库并通过 singleflight 合并并发请求
//
// singleflight（cache.Do）确保同一时刻多个并发请求只查一次数据库，
// 其余请求等待第一个结果返回，直接复用。
func (h *ProblemHandler) Get(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid problem id"})
		return
	}

	// 第零级：Bloom Filter 拦截不存在的 ID（防穿透）
	// 说"不存在"就一定不存在，直接返回 404，完全避免访问 Redis 和 DB
	if !cache.MightHaveProblem(id) {
		c.JSON(http.StatusNotFound, gin.H{"error": "problem not found"})
		return
	}

	cacheKey := "problem:" + strconv.FormatUint(id, 10)
	nullKey := "problem:null:" + strconv.FormatUint(id, 10)

	// 第一级：读取缓存
	var problem model.Problem
	if cache.Get(cacheKey, &problem) {
		c.JSON(http.StatusOK, problem)
		return
	}

	// 第二级：空值标记（说明之前查过，数据库中没有此 ID）
	var nullMarker string
	if cache.Get(nullKey, &nullMarker) {
		c.JSON(http.StatusNotFound, gin.H{"error": "problem not found"})
		return
	}

	// 第三级：通过 singleflight 查数据库
	// 多个并发请求中只有一个会执行数据库查询，其余等待结果
	result, dbErr := cache.Do(cacheKey, func() (interface{}, error) {
		var p model.Problem
		err := database.DB.Preload("Tags").First(&p, id).Error
		if err != nil {
			// 设置空值标记，防止后续请求反复穿透到数据库
			cache.Set(nullKey, "1", 5*time.Minute)
			return nil, err
		}
		cache.Set(cacheKey, p, 10*time.Minute)
		return &p, nil
	})

	if dbErr != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "problem not found"})
		return
	}

	c.JSON(http.StatusOK, result.(*model.Problem))
}

// ============================================================================
// 更新和删除
// ============================================================================

// Update 处理 PUT /api/problems/:id。
// 部分更新：只更新请求中提供的字段。
// 更新后清除缓存。
func (h *ProblemHandler) Update(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid problem id"})
		return
	}

	var problem model.Problem
	if err := database.DB.First(&problem, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "problem not found"})
		return
	}

	var req problemReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	updates := map[string]interface{}{}
	if req.Title != "" {
		updates["title"] = req.Title
	}
	if req.Description != "" {
		updates["description"] = req.Description
	}
	if req.TimeLimit > 0 {
		updates["time_limit"] = req.TimeLimit
	}
	if req.MemoryLimit > 0 {
		updates["memory_limit"] = req.MemoryLimit
	}
	if req.SampleCases != nil {
		updates["sample_cases"] = req.SampleCases
	}
	if req.Number != nil {
		if *req.Number > 0 {
			updates["number"] = *req.Number
		} else {
			updates["number"] = problem.ID
		}
	}
	database.DB.Model(&problem).Updates(updates)

	// 同步标签（如果提供了）
	if req.Tags != nil {
		syncProblemTags(problem.ID, req.Tags)
	}

	// 清除缓存
	cache.Del("problem:" + strconv.FormatUint(id, 10))
	cache.Del("problems:list")

	database.DB.Preload("Tags").First(&problem, id)
	c.JSON(http.StatusOK, problem)
}

// Delete 处理 DELETE /api/problems/:id。
// 删除题目及其标签关联。
func (h *ProblemHandler) Delete(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid problem id"})
		return
	}

	// 删除标签关联
	database.DB.Where("problem_id = ?", id).Delete(&map[string]interface{}{}, "problem_tag_links")
	if err := database.DB.Delete(&model.Problem{}, id).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete problem"})
		return
	}
	cache.Del("problem:" + strconv.FormatUint(id, 10))
	cache.Del("problems:list")
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}

// ============================================================================
// 标签同步
// ============================================================================

// syncProblemTags 同步题目的标签列表。
//
// 策略：先删除所有已有标签关联，再逐一创建新的关联。
// 这样确保了标签列表与请求完全一致（而非增量更新）。
//
// "FirstOrCreate" 模式：如果标签名已存在则复用，不存在则创建。
// "INSERT IGNORE" 防止并发写入导致的重复键冲突。
func syncProblemTags(problemID uint, tagNames []string) {
	// 删除旧的标签关联
	database.DB.Where("problem_id = ?", problemID).Delete(&map[string]interface{}{}, "problem_tag_links")

	for _, name := range tagNames {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		// 查找或创建标签
		var tag model.ProblemTag
		database.DB.Where("name = ?", name).FirstOrCreate(&tag, model.ProblemTag{Name: name})
		// 建立关联（INSERT IGNORE 防止竞争条件）
		database.DB.Exec("INSERT IGNORE INTO problem_tag_links (problem_id, tag_id) VALUES (?, ?)", problemID, tag.ID)
	}
}
