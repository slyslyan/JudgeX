package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"judgex/internal/database"
	"judgex/internal/model"
	"judgex/internal/queue"
)

// ============================================================================
// 比赛控制器
// ============================================================================
//
// 比赛（Contest）是 JudgeX 的核心功能之一，支持：
// - 创建/编辑/删除比赛
// - 比赛时间窗口管理（开始时间、结束时间）
// - 题目关联（A、B、C...编号）
// - 比赛内提交（时间窗口强制）
// - 动态状态计算（Not Started / Running / Ended）
// - 两种比赛规则：ACM（ICPC 标准）和 IOI（部分得分）
//
// 比赛状态是动态计算的（每次请求时计算），不存储在数据库中。
// 这样避免了管理员手动切换状态的麻烦。

type ContestHandler struct{}

func NewContestHandler() *ContestHandler {
	return &ContestHandler{}
}

// contestWithStatus 携带动态计算状态
type contestWithStatus struct {
	model.Contest
	Status string `json:"status"` // "Not Started" | "Running" | "Ended"
}

type createContestReq struct {
	Title       string `json:"title" binding:"required"`
	Description string `json:"description"`
	StartTime   string `json:"start_time" binding:"required"` // RFC3339 格式
	EndTime     string `json:"end_time" binding:"required"`
	RuleType    string `json:"rule_type"` // "ACM" (默认) 或 "IOI"
}

// ============================================================================
// 查询
// ============================================================================

// List 处理 GET /api/contests。
// 分页返回比赛列表，按开始时间倒序。
// 每场比赛附带动态计算的状态（Not Started / Running / Ended）。
func (h *ContestHandler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	offset := (page - 1) * pageSize

	var total int64
	database.DB.Model(&model.Contest{}).Count(&total)

	var contests []model.Contest
	database.DB.Order("start_time DESC").Offset(offset).Limit(pageSize).Find(&contests)

	now := time.Now()
	result := make([]contestWithStatus, len(contests))
	for i, c := range contests {
		result[i] = contestWithStatus{
			Contest: c,
			Status:  computeStatus(c.StartTime, c.EndTime, now),
		}
	}

	c.JSON(http.StatusOK, gin.H{"contests": result, "total": total})
}

// Get 处理 GET /api/contests/:id。
// 返回比赛详情和关联的题目列表。
func (h *ContestHandler) Get(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid contest id"})
		return
	}

	var contest model.Contest
	if err := database.DB.First(&contest, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "contest not found"})
		return
	}

	var contestProblems []model.ContestProblem
	database.DB.Where("contest_id = ?", id).Order("display_id").Find(&contestProblems)

	type ContestProblemWithTitle struct {
		model.ContestProblem
		ProblemTitle string `json:"problem_title"`
	}

	problemIDs := make([]uint, 0, len(contestProblems))
	for _, cp := range contestProblems {
		problemIDs = append(problemIDs, cp.ProblemID)
	}
	var problems []model.Problem
	titleMap := make(map[uint]string)
	if len(problemIDs) > 0 {
		database.DB.Where("id IN ?", problemIDs).Find(&problems)
	}
	for _, p := range problems {
		titleMap[p.ID] = p.Title
	}

	result := make([]ContestProblemWithTitle, 0, len(contestProblems))
	for _, cp := range contestProblems {
		result = append(result, ContestProblemWithTitle{
			ContestProblem: cp,
			ProblemTitle:   titleMap[cp.ProblemID],
		})
	}

	now := time.Now()
	c.JSON(http.StatusOK, gin.H{
		"contest": contestWithStatus{
			Contest: contest,
			Status:  computeStatus(contest.StartTime, contest.EndTime, now),
		},
		"problems": result,
	})
}

// ============================================================================
// 增删改
// ============================================================================

// Create 处理 POST /api/contests。
// 创建新比赛，时间使用 RFC3339 格式。
func (h *ContestHandler) Create(c *gin.Context) {
	var req createContestReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	start, err := time.Parse(time.RFC3339, req.StartTime)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid start_time format, use RFC3339"})
		return
	}
	end, err := time.Parse(time.RFC3339, req.EndTime)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid end_time format, use RFC3339"})
		return
	}
	if !end.After(start) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "end_time must be after start_time"})
		return
	}

	ruleType := req.RuleType
	if ruleType == "" {
		ruleType = "ACM"
	}

	contest := model.Contest{
		Title:       req.Title,
		Description: req.Description,
		StartTime:   start,
		EndTime:     end,
		RuleType:    ruleType,
	}
	database.DB.Create(&contest)
	c.JSON(http.StatusCreated, contest)
}

// Update 处理 PUT /api/contests/:id。
// 更新比赛信息，只更新提供的字段。
func (h *ContestHandler) Update(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid contest id"})
		return
	}

	var contest model.Contest
	if err := database.DB.First(&contest, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "contest not found"})
		return
	}

	var req createContestReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Title != "" {
		contest.Title = req.Title
	}
	if req.Description != "" {
		contest.Description = req.Description
	}
	if req.StartTime != "" {
		if t, err := time.Parse(time.RFC3339, req.StartTime); err == nil {
			contest.StartTime = t
		}
	}
	if req.EndTime != "" {
		if t, err := time.Parse(time.RFC3339, req.EndTime); err == nil {
			contest.EndTime = t
		}
	}
	if req.RuleType != "" {
		contest.RuleType = req.RuleType
	}

	database.DB.Save(&contest)
	c.JSON(http.StatusOK, contest)
}

// ============================================================================
// 比赛题目管理
// ============================================================================

type addProblemReq struct {
	ProblemID uint   `json:"problem_id" binding:"required"`
	DisplayID string `json:"display_id" binding:"required"` // "A", "B", "C", ...
}

// AddProblem 处理 POST /api/contests/:id/problems。
// 将题目添加到比赛中，并指定显示编号。
func (h *ContestHandler) AddProblem(c *gin.Context) {
	contestID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid contest id"})
		return
	}

	var req addProblemReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cp := model.ContestProblem{
		ContestID: uint(contestID),
		ProblemID: req.ProblemID,
		DisplayID: req.DisplayID,
	}
	if err := database.DB.Create(&cp).Error; err != nil {
		c.JSON(http.StatusConflict, gin.H{"error": "problem already in contest"})
		return
	}
	c.JSON(http.StatusCreated, cp)
}

// RemoveProblem 处理 DELETE /api/contests/:id/problems/:pid。
// 从比赛中移除题目。
func (h *ContestHandler) RemoveProblem(c *gin.Context) {
	contestID, _ := strconv.ParseUint(c.Param("id"), 10, 64)
	problemID, _ := strconv.ParseUint(c.Param("pid"), 10, 64)

	database.DB.Where("contest_id = ? AND problem_id = ?", contestID, problemID).Delete(&model.ContestProblem{})
	c.JSON(http.StatusOK, gin.H{"message": "removed"})
}

// ============================================================================
// 比赛内提交
// ============================================================================

// Submit 处理 POST /api/contests/:id/submissions。
// 在比赛时间窗口内提交代码。
//
// 安全检查：
// 1. 比赛是否已开始（未开始则禁止提交）
// 2. 比赛是否已结束（已结束同样禁止）
// 3. 题目是否属于该比赛
func (h *ContestHandler) Submit(c *gin.Context) {
	contestID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid contest id"})
		return
	}

	var contest model.Contest
	if err := database.DB.First(&contest, contestID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "contest not found"})
		return
	}

	// 时间窗口检查
	now := time.Now()
	if now.Before(contest.StartTime) {
		c.JSON(http.StatusForbidden, gin.H{"error": "contest has not started yet"})
		return
	}
	if now.After(contest.EndTime) {
		c.JSON(http.StatusForbidden, gin.H{"error": "contest has ended"})
		return
	}

	var req struct {
		ProblemID uint   `json:"problem_id" binding:"required"`
		Language  string `json:"language" binding:"required"`
		Code      string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 验证题目是否属于该比赛
	var cp model.ContestProblem
	if err := database.DB.Where("contest_id = ? AND problem_id = ?", contestID, req.ProblemID).First(&cp).Error; err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "problem not in contest"})
		return
	}

	userID := c.MustGet("user_id").(uint)

	var problem model.Problem
	if err := database.DB.First(&problem, req.ProblemID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "problem not found"})
		return
	}

	cid := uint(contestID)
	submission := model.Submission{
		UserID:    userID,
		ProblemID: req.ProblemID,
		ContestID: &cid,
		Language:  req.Language,
		Code:      req.Code,
		Status:    "pending",
	}
	database.DB.Create(&submission)

	queue.Publish(queue.JudgeTask{
		SubmissionID: submission.ID,
		ProblemID:    req.ProblemID,
		UserID:       userID,
		ContestID:    &cid,
		Language:     req.Language,
		Code:         req.Code,
		TimeLimit:    problem.TimeLimit,
		MemoryLimit:  problem.MemoryLimit,
		TraceParent:  injectTraceParent(c),
	})

	c.JSON(http.StatusCreated, gin.H{
		"submission_id": submission.ID,
		"status":        "pending",
	})
}

// ============================================================================
// 辅助函数
// ============================================================================

// computeStatus 动态计算比赛状态。
// 不存储在数据库中，每次请求实时计算。
func computeStatus(start, end, now time.Time) string {
	if now.Before(start) {
		return "Not Started"
	}
	if now.After(end) {
		return "Ended"
	}
	return "Running"
}
