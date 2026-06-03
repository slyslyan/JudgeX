package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/propagation"

	"judgex/internal/cache"
	"judgex/internal/database"
	"judgex/internal/judge"
	"judgex/internal/model"
	"judgex/internal/queue"
)

// ============================================================================
// 提交处理器
// ============================================================================
//
// 本文件实现了提交（Submission）相关的所有 HTTP 接口。
// 提交是 JudgeX 的核心业务流程——用户提交代码，系统判题，返回结果。
//
// 完整提交流程：
//
//	┌────────┐  POST /api/submissions  ┌──────────┐  Publish  ┌────────┐
//	│ 客户端  │ ─────────────────────→ │  Submit   │ ────────→ │ Queue  │
//	│        │                        │  Handler  │           │        │
//	│        │ ←── submission_id ──── │           │           │ NSQ/  │
//	│        │                        └──────────┘           │ Redis │
//	│        │                                                    │
//	│        │  GET /api/submissions/:id/events (SSE)            │
//	│        │ ←── status updates ────────────────── ─── ─── ─  │
//	│        │                                                    ▼
//	│        │                                               ┌────────┐
//	│        │                                               │ Worker │
//	│        │                                               │ Judge  │
//	│        │                                               │ Sandbox│
//	│        │                                               └────────┘
//	│        │                                                    │
//	│        │ ←── final status (via SSE) ───  DB update ────────┘
//	└────────┘

// injectTraceParent 从 Gin 上下文中提取 W3C TraceContext 的 traceparent，
// 用于通过消息队列传播到 judge-worker，实现端到端分布式追踪。
func injectTraceParent(c *gin.Context) string {
	ctx := c.Request.Context()
	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)
	return carrier["traceparent"]
}

type SubmissionHandler struct{}

func NewSubmissionHandler() *SubmissionHandler {
	return &SubmissionHandler{}
}

type submitReq struct {
	ProblemID uint   `json:"problem_id" binding:"required"`
	Language  string `json:"language" binding:"required"`
	Code      string `json:"code" binding:"required"`
}

// ============================================================================
// 提交代码（核心流程）
// ============================================================================

// Submit 处理 POST /api/submissions。
//
// 流程：
// 1. 验证请求参数（题目 ID、编程语言、代码）
// 2. 检查题是否存在
// 3. 去重检测：相同用户+题目+语言+代码的提交在 5 秒内重复提交直接返回缓存结果
// 4. 创建数据库记录（status = "pending"）
// 5. 发布判题任务到消息队列
// 6. 返回 submission_id
//
// 去重机制：
// 使用 SHA256 哈希 user_id:problem_id:language:code，结果作为 Redis key。
// 如果 key 已存在，说明是重复提交，直接返回上次的判题结果。
// key 有效期由 cache.IncrWithTTL 的窗口控制（默认 5 秒）。
func (h *SubmissionHandler) Submit(c *gin.Context) {
	var req submitReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID := c.MustGet("user_id").(uint)

	// 验证题目是否存在
	var problem model.Problem
	if err := database.DB.First(&problem, req.ProblemID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "problem not found"})
		return
	}

	// 去重检测：相同内容秒级内重复提交
	hash := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s:%s", userID, req.ProblemID, req.Language, req.Code)))
	dedupKey := "dedup:" + hex.EncodeToString(hash[:])

	var cachedStatus string
	if cache.Get(dedupKey, &cachedStatus) {
		c.JSON(http.StatusOK, gin.H{
			"submission_id": 0,
			"status":        cachedStatus,
			"cached":        true,
		})
		return
	}

	// 创建提交记录
	submission := model.Submission{
		UserID:    userID,
		ProblemID: req.ProblemID,
		Language:  req.Language,
		Code:      req.Code,
		Status:    "pending",
	}
	database.DB.Create(&submission)

	// 发布判题任务到消息队列
	queue.Publish(queue.JudgeTask{
		SubmissionID: submission.ID,
		ProblemID:    req.ProblemID,
		UserID:       userID,
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
// 查询提交
// ============================================================================

// Get 处理 GET /api/submissions/:id。
// 返回提交记录的详细信息（包括题目名称）。
func (h *SubmissionHandler) Get(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid submission id"})
		return
	}

	var submission model.Submission
	if err := database.DB.First(&submission, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "submission not found"})
		return
	}

	// 关联查题目名称
	type SubmissionWithTitle struct {
		model.Submission
		ProblemTitle string `json:"problem_title"`
	}

	var problem model.Problem
	title := ""
	if err := database.DB.Select("title").First(&problem, submission.ProblemID).Error; err == nil {
		title = problem.Title
	}

	c.JSON(http.StatusOK, SubmissionWithTitle{
		Submission:   submission,
		ProblemTitle: title,
	})
}

// List 处理 GET /api/submissions。
// 支持分页（cursor 游标分页）、状态筛选、语言筛选、题目筛选、只看自己。
//
// 游标分页 vs 传统分页：
// - 传统分页（page/page_size）在数据变化时会导致重复或遗漏
// - 游标分页（cursor）基于上一页最后一条记录的 ID，性能稳定
//
// 筛选参数：
//   - cursor: 上一页最后一条记录的 ID（首次查询为 0）
//   - page_size: 每页数量（默认 20，最大 100）
//   - status: 状态筛选（如 "Accepted"）
//   - language: 语言筛选（如 "cpp"）
//   - problem_id: 题目筛选
//   - mine: "true" 表示只查当前用户的提交
func (h *SubmissionHandler) List(c *gin.Context) {
	cursor, _ := strconv.ParseInt(c.Query("cursor"), 10, 64)
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	if pageSize < 1 || pageSize > 100 {
		pageSize = 20
	}
	status := c.Query("status")
	language := c.Query("language")
	var problemID uint64
	if pid := c.Query("problem_id"); pid != "" {
		problemID, _ = strconv.ParseUint(pid, 10, 64)
	}
	mine := c.Query("mine") == "true"
	var myUserID uint
	if mine {
		if uid, exists := c.Get("user_id"); exists {
			myUserID = uid.(uint)
		}
	}

	type SubmissionWithTitle struct {
		model.Submission
		ProblemTitle string `json:"problem_title"`
	}

	var submissions []model.Submission
	q := database.DB.Order("id DESC").Limit(pageSize + 1)
	if cursor > 0 {
		q = q.Where("id < ?", cursor)
	}
	if status != "" {
		q = q.Where("status = ?", status)
	}
	if problemID > 0 {
		q = q.Where("problem_id = ?", problemID)
	}
	if language != "" {
		q = q.Where("language = ?", language)
	}
	if mine && myUserID > 0 {
		q = q.Where("user_id = ?", myUserID)
	}
	q.Find(&submissions)

	hasMore := len(submissions) > pageSize
	if hasMore {
		submissions = submissions[:pageSize]
	}

	// 批量查询题目名称
	problemIDs := make([]uint, 0, len(submissions))
	for _, s := range submissions {
		problemIDs = append(problemIDs, s.ProblemID)
	}
	var problems []model.Problem
	titleMap := make(map[uint]string)
	if len(problemIDs) > 0 {
		database.DB.Where("id IN ?", problemIDs).Find(&problems)
	}
	for _, p := range problems {
		titleMap[p.ID] = p.Title
	}

	result := make([]SubmissionWithTitle, 0, len(submissions))
	for _, s := range submissions {
		result = append(result, SubmissionWithTitle{
			Submission:   s,
			ProblemTitle: titleMap[s.ProblemID],
		})
	}

	var nextCursor int64
	if len(submissions) > 0 {
		nextCursor = submissions[len(submissions)-1].ID
	}

	c.JSON(http.StatusOK, gin.H{
		"submissions": result,
		"next_cursor": nextCursor,
		"has_more":    hasMore,
		"page_size":   pageSize,
	})
}

// ============================================================================
// 重判
// ============================================================================

// ReJudge 处理 POST /api/submissions/:id/rejudge。
// 将指定提交重置为 pending 状态并重新入队。
func (h *SubmissionHandler) ReJudge(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid submission id"})
		return
	}

	var submission model.Submission
	if err := database.DB.First(&submission, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "submission not found"})
		return
	}

	var problem model.Problem
	if err := database.DB.First(&problem, submission.ProblemID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "problem not found"})
		return
	}

	// 重置提交数据
	database.DB.Model(&submission).Updates(map[string]interface{}{
		"status":        "pending",
		"time_used":     0,
		"memory_used":   0,
		"passed_count":  0,
		"total_cases":   0,
		"error_message": "",
	})

	// 重新发布到判题队列
	queue.Publish(queue.JudgeTask{
		SubmissionID: submission.ID,
		ProblemID:    submission.ProblemID,
		UserID:       submission.UserID,
		Language:     submission.Language,
		Code:         submission.Code,
		TimeLimit:    problem.TimeLimit,
		MemoryLimit:  problem.MemoryLimit,
		TraceParent:  injectTraceParent(c),
	})

	c.JSON(http.StatusOK, gin.H{"message": "re-judging", "submission_id": submission.ID})
}

// ReJudgeBatch 处理 POST /api/submissions/rejudge-batch。
// 批量重判符合条件的提交。
//
// 筛选参数（至少提供一个）：
//   - problem_id: 重判指定题目的所有提交
//   - contest_id: 重判指定比赛的所有提交
//   - status: 只重判指定状态的提交（如 "Wrong Answer"）
func (h *SubmissionHandler) ReJudgeBatch(c *gin.Context) {
	var req struct {
		ProblemID uint   `json:"problem_id"`
		ContestID uint   `json:"contest_id"`
		Status    string `json:"status"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.ProblemID == 0 && req.ContestID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "problem_id or contest_id required"})
		return
	}

	var submissions []model.Submission
	q := database.DB
	if req.ProblemID > 0 {
		q = q.Where("problem_id = ?", req.ProblemID)
	}
	if req.ContestID > 0 {
		q = q.Where("contest_id = ?", req.ContestID)
	}
	if req.Status != "" {
		q = q.Where("status = ?", req.Status)
	}
	q.Find(&submissions)

	rejudged := 0
	for _, sub := range submissions {
		var problem model.Problem
		if database.DB.First(&problem, sub.ProblemID).Error != nil {
			continue
		}

		database.DB.Model(&sub).Updates(map[string]interface{}{
			"status":        "pending",
			"time_used":     0,
			"memory_used":   0,
			"passed_count":  0,
			"total_cases":   0,
			"error_message": "",
		})

		queue.Publish(queue.JudgeTask{
			SubmissionID: sub.ID,
			ProblemID:    sub.ProblemID,
			UserID:       sub.UserID,
			Language:     sub.Language,
			Code:         sub.Code,
			TimeLimit:    problem.TimeLimit,
			MemoryLimit:  problem.MemoryLimit,
			TraceParent:  injectTraceParent(c),
		})
		rejudged++
	}

	c.JSON(http.StatusOK, gin.H{"message": "rejudging", "count": rejudged})
}

// ============================================================================
// SSE 实时状态推送
// ============================================================================

// StreamEvents 处理 GET /api/submissions/:id/events。
// SSE 端点：当提交状态变化时实时推送给前端。
//
// 工作原理：
// 1. 发送当前状态（如果已是终态则立即关闭连接）
// 2. 订阅 Redis Pub/Sub 频道 "submission:<id>"
// 3. judge-worker 完成判题后更新数据库并发布消息到该频道
// 4. 前端收到新状态，如果是终态则关闭连接
//
// 状态判定：
// - 非终态：pending, judging（继续等待）
// - 终态：Accepted, Wrong Answer, TLE, MLE, Runtime Error, Compile Error
func (h *SubmissionHandler) StreamEvents(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid submission id"})
		return
	}

	var submission model.Submission
	if err := database.DB.First(&submission, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "submission not found"})
		return
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.WriteHeader(http.StatusOK)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return
	}

	writeEvent := func(data interface{}) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(c.Writer, "data: %s\n\n", b)
		flusher.Flush()
	}

	// 发送当前状态
	writeEvent(submission)

	// 如果已是终态，立即关闭
	if submission.Status != "pending" && submission.Status != "judging" {
		return
	}

	// 订阅 Redis 频道
	channel := fmt.Sprintf("submission:%d", id)
	ch, unsub := cache.Subscribe(channel)
	defer unsub()

	ctx := c.Request.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			var updated model.Submission
			if err := json.Unmarshal([]byte(msg), &updated); err != nil {
				continue
			}
			writeEvent(updated)
			// 如果到达终态，自动关闭 SSE 连接
			if updated.Status != "pending" && updated.Status != "judging" {
				return
			}
		}
	}
}

// ============================================================================
// 辅助接口
// ============================================================================

// LastCode 处理 GET /api/problems/:id/last-code。
// 返回当前用户对指定题目的最后一次提交的代码和语言。
// 用于前端"恢复上次编辑"功能。
func (h *SubmissionHandler) LastCode(c *gin.Context) {
	userID := c.MustGet("user_id").(uint)
	pid, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid problem id"})
		return
	}

	var sub model.Submission
	err = database.DB.Where("user_id = ? AND problem_id = ?", userID, pid).
		Order("id DESC").First(&sub).Error
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"code": nil, "language": nil})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"code":     sub.Code,
		"language": sub.Language,
	})
}

// Run 处理 POST /api/run。
// 运行代码但不保存提交记录（类似于"运行"而非"提交"）。
// 用于 Playground 或在线调试。
//
// 注意：此接口不经过消息队列，直接在请求中执行。
// 长时间运行的代码可能导致 HTTP 超时。
type runReq struct {
	Language    string `json:"language" binding:"required"`
	Code        string `json:"code" binding:"required"`
	Input       string `json:"input"`
	TimeLimit   int    `json:"time_limit"`
	MemoryLimit int    `json:"memory_limit"`
}

func (h *SubmissionHandler) Run(c *gin.Context) {
	var req runReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 默认值
	if req.TimeLimit <= 0 {
		req.TimeLimit = 5000
	}
	if req.MemoryLimit <= 0 {
		req.MemoryLimit = 256
	}

	result := judge.Run(req.Language, req.Code, req.Input, req.TimeLimit, req.MemoryLimit)

	c.JSON(http.StatusOK, gin.H{
		"status":      result.Status,
		"stdout":      result.Output,
		"stderr":      result.ErrorMsg,
		"time_used":   result.TimeUsed,
		"memory_used": result.MemoryUsed,
	})
}
