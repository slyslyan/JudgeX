package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"judgex/internal/ai"
	"judgex/internal/diagnostics"
	"judgex/internal/queue"
)

// ============================================================================
// AI 处理器
// ============================================================================
//
// 本文件实现了 JudgeX 的 AI 相关 HTTP 处理器，所有 AI 响应通过 SSE
// （Server-Sent Events）流式传输。
//
// SSE 优势：
// - 浏览器原生支持（EventSource API）
// - 单向通道（服务器→客户端），适合 AI 流式输出
// - 自动重连
//
// 实现的端点：
// - POST /api/ai/chat: 通用 AI 对话（diagnose / socratic / coach）
// - POST /api/ai/generate-test-script: AI 测试数据生成（admin）
// - POST /api/ai/sre: SRE 系统诊断（admin）
// - POST /api/ai/debug: AI Debug Agent（在 debug.go 中实现）
// - POST /api/ai/sre/agent: SRE Ops Agent（在 sre_agent.go 中实现）
// - GET /api/admin/ai/status: AI 熔断器状态
// - GET /api/admin/sre/snapshot: 系统快照
// - POST /api/admin/alerts/webhook: AlertManager Webhook

// chatRequest 是 AI 对话请求体。
type chatRequest struct {
	AgentType    string           `json:"agent_type"` // "diagnose" | "socratic" | "coach"
	ProblemID    uint             `json:"problem_id"`
	SubmissionID *int64           `json:"submission_id"`
	Message      string           `json:"message"`
	History      []ai.ChatMessage `json:"history"`
}

// sseStreamTimeout 是 SSE 流的超时时间。
// LLM 响应较慢时，超过此时间客户端会收到超时错误。
const sseStreamTimeout = 60 * time.Second

// ============================================================================
// 通用 AI 对话
// ============================================================================

// ChatStream 处理 POST /api/ai/chat。
// 流程：验证请求 → 注入检测 → 加载上下文 → 构建 prompt → SSE 流式响应
//
// 安全措施：
// 1. 提示注入检测（ScanForInjection）：高风险请求被拦截
// 2. 用户消息清洗（SanitizeUserMessage）
// 3. 上下文超时控制（sseStreamTimeout）
func ChatStream(c *gin.Context) {
	var req chatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 参数默认值
	if req.AgentType == "" {
		req.AgentType = "coach"
	}
	if req.ProblemID == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "problem_id is required"})
		return
	}
	if req.Message == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "message is required"})
		return
	}

	// 安全检测：扫描提示注入
	threat, reason := ai.ScanForInjection(req.Message)
	if threat == "high" {
		// 高风险注入：拦截请求，返回教育性回复
		c.Header("Content-Type", "text/event-stream")
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Content-Type-Options", "nosniff")
		c.SSEvent("error", reason)
		c.SSEvent("token", ai.GuardResponse())
		c.SSEvent("done", "")
		c.Writer.Flush()
		return
	}

	// 从数据库加载上下文（题目信息、提交信息等）
	promptCtx := ai.AssembleContext(req.AgentType, req.ProblemID, req.SubmissionID)
	systemPrompt := ai.BuildSystemPrompt(promptCtx)

	// 清洗用户消息
	sanitized := ai.SanitizeUserMessage(req.Message)

	// 构建消息历史
	messages := make([]ai.ChatMessage, 0, len(req.History)+1)
	messages = append(messages, req.History...)
	messages = append(messages, ai.ChatMessage{Role: "user", Content: sanitized})

	// 调用 LLM 流式接口
	ctx, cancel := context.WithTimeout(c.Request.Context(), sseStreamTimeout)
	defer cancel()

	ch := ai.StreamChat(ctx, systemPrompt, messages)

	// 设置 SSE 头
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Status(http.StatusOK)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming not supported"})
		return
	}

	// 流式输出：逐 chunk 推送 SSE 事件
	for chunk := range ch {
		if chunk.Error != "" {
			log.Printf("[ai] stream error: %s", chunk.Error)
			c.SSEvent("error", chunk.Error)
			flusher.Flush()
			return
		}
		if chunk.Done {
			c.SSEvent("done", "")
			flusher.Flush()
			return
		}
		c.SSEvent("token", chunk.Token)
		flusher.Flush()
	}
}

// ============================================================================
// 测试数据生成
// ============================================================================

// GenerateTestScript 处理 POST /api/admin/ai/generate-test-script。
// 调用 LLM 生成 Python 测试数据生成脚本。
// 该脚本使用标准库（无需外部依赖），生成 .in/.out 文件对。
//
// 限制：
// - 最少 1 个、最多 50 个测试用例
// - 脚本必须包含边界测试
// - 超时 90 秒（LLM 生成代码比对话慢）
func GenerateTestScript(c *gin.Context) {
	var req struct {
		ProblemDesc  string `json:"problem_desc" binding:"required"`
		InputFormat  string `json:"input_format"`
		OutputFormat string `json:"output_format"`
		Constraints  string `json:"constraints"`
		SolutionHint string `json:"solution_hint"` // 算法提示（不是完整代码）
		NumCases     int    `json:"num_cases"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if req.NumCases <= 0 {
		req.NumCases = 10
	}
	if req.NumCases > 50 {
		req.NumCases = 50
	}

	systemPrompt := `You are a test data generation expert for programming competitions.
You write Python scripts that generate high-quality test case files (.in and .out).

## Your task
Given a problem description, input/output format, and constraints, write a Python 3 script that:
1. Generates ` + fmt.Sprintf("%d", req.NumCases) + ` test cases with varying difficulty
2. Produces files named 1.in, 1.out, 2.in, 2.out, ... in a "testcases" subdirectory
3. Includes edge cases (min/max values, corner cases, large inputs)
4. Uses random but deterministic seeds for reproducibility

## Script template
` + "```python" + `
import os, random, sys

random.seed(42)
OUT_DIR = "testcases"
os.makedirs(OUT_DIR, exist_ok=True)

# Your generation logic here.
# Use random.randint(), random.choice(), etc.
# Write to OUT_DIR + "/1.in", OUT_DIR + "/1.out", etc.

print(f"Generated N test cases in {OUT_DIR}/")
` + "```" + `

## Rules
1. The script MUST be self-contained (no external deps beyond stdlib).
2. For each case, write <num>.in and <num>.out.
3. The .out file is the CORRECT output for the .in file — implement the solution logic in the script.
4. Include boundary tests (min N, max N, edge cases like all zeros, already sorted, reverse order, etc.).
5. Add brief comments explaining what each case tests.

Output ONLY the Python script in a code block. Briefly explain the test coverage strategy before the code.`

	userMessage := fmt.Sprintf(`## Problem Description
%s

## Input Format
%s

## Output Format
%s

## Constraints
%s

## Solution Approach (hint)
%s

Generate a test data script with %d cases.`,
		req.ProblemDesc, req.InputFormat, req.OutputFormat, req.Constraints, req.SolutionHint, req.NumCases)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
	defer cancel()

	ch := ai.StreamChat(ctx, systemPrompt, []ai.ChatMessage{
		{Role: "user", Content: userMessage},
	})

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Status(http.StatusOK)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming not supported"})
		return
	}

	for chunk := range ch {
		if chunk.Error != "" {
			log.Printf("[ai] test-gen error: %s", chunk.Error)
			c.SSEvent("error", chunk.Error)
			flusher.Flush()
			return
		}
		if chunk.Done {
			c.SSEvent("done", "")
			flusher.Flush()
			return
		}
		c.SSEvent("token", chunk.Token)
		flusher.Flush()
	}
}

// ============================================================================
// SRE 诊断和监控
// ============================================================================

// AIStatus 返回当前 LLM 连接的熔断器状态和模型信息。
func AIStatus(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"circuit_breaker": ai.LLCircuitBreaker.Stats(),
		"llm_model":       ai.Cfg.Model,
	})
}

// SRESnapshot 返回系统的实时快照（不调用 AI）。
// 包含队列状态、沙箱状态、数据库连接、最近错误等。
// 用于管理员仪表盘（SRE 面板）。
func SRESnapshot(c *gin.Context) {
	_, bufLen, _, workerCount := queue.Stats()
	snap := diagnostics.Collect(bufLen, workerCount)
	c.JSON(http.StatusOK, snap)
}

// SREDiagnose 处理 POST /api/admin/ai/sre。
// 收集系统快照，调用 LLM 进行 SRE 分析。
// 如果未提供问题，默认分析"系统是否有异常"。
func SREDiagnose(c *gin.Context) {
	var req struct {
		Question string `json:"question"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		req.Question = "Analyze the system snapshot and report any issues."
	}
	if req.Question == "" {
		req.Question = "Analyze the system snapshot and report any issues."
	}

	_, bufLen, _, workerCount := queue.Stats()
	snap := diagnostics.Collect(bufLen, workerCount)
	snapJSON, _ := json.MarshalIndent(snap, "", "  ")

	userMessage := fmt.Sprintf("## System Snapshot\n\n```json\n%s\n```\n\n## Question\n%s", string(snapJSON), req.Question)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()

	systemPrompt := ai.BuildSystemPrompt(ai.PromptContext{AgentType: "sre"})
	ch := ai.StreamChat(ctx, systemPrompt, []ai.ChatMessage{
		{Role: "user", Content: userMessage},
	})

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Status(http.StatusOK)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "streaming not supported"})
		return
	}

	for chunk := range ch {
		if chunk.Error != "" {
			log.Printf("[ai] sre error: %s", chunk.Error)
			c.SSEvent("error", chunk.Error)
			flusher.Flush()
			return
		}
		if chunk.Done {
			c.SSEvent("done", "")
			flusher.Flush()
			return
		}
		c.SSEvent("token", chunk.Token)
		flusher.Flush()
	}
}

// ============================================================================
// AlertManager Webhook 处理
// ============================================================================

// alertManagerWebhook 是 AlertManager 发送的 webhook 请求体。
// AlertManager 在告警触发（firing）或恢复（resolved）时会 POST 到此端点。
type alertManagerWebhook struct {
	Version           string              `json:"version"`
	GroupKey          string              `json:"groupKey"`
	Status            string              `json:"status"` // "firing" or "resolved"
	Receiver          string              `json:"receiver"`
	GroupLabels       map[string]string   `json:"groupLabels"`
	CommonLabels      map[string]string   `json:"commonLabels"`
	CommonAnnotations map[string]string   `json:"commonAnnotations"`
	Alerts            []alertManagerAlert `json:"alerts"`
}

type alertManagerAlert struct {
	Status       string            `json:"status"`
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     string            `json:"startsAt"`
	EndsAt       string            `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
}

type alertWebhookResponse struct {
	Status      string `json:"status"`
	AIDiagnosis string `json:"ai_diagnosis,omitempty"`
	AlertCount  int    `json:"alert_count"`
	Snapshot    any    `json:"snapshot"`
}

// AlertWebhook 处理 POST /api/admin/alerts/webhook。
//
// 工作流程：
// 1. 接收 AlertManager 的 webhook 请求
// 2. 收集当前系统快照
// 3. 在后台调用 LLM 分析告警和系统状态
// 4. 返回诊断结果
//
// 超时处理：
// - LLM 分析最多 60 秒
// - 如果超时，返回 "diagnosis_timeout" 状态
// - 不阻塞 AlertManager 的 webhook 响应
func AlertWebhook(c *gin.Context) {
	var payload alertManagerWebhook
	if err := c.ShouldBindJSON(&payload); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid webhook payload: " + err.Error()})
		return
	}

	_, bufLen, _, workerCount := queue.Stats()
	snap := diagnostics.Collect(bufLen, workerCount)

	alertSummary := buildAlertSummary(payload)
	log.Printf("[aiops] webhook received: %d alerts, status=%s, summary=%s",
		len(payload.Alerts), payload.Status, alertSummary)

	// 后台 goroutine 运行 AI 诊断
	type diagResult struct {
		text string
		err  error
	}
	diagCh := make(chan diagResult, 1)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		snapJSON, _ := json.MarshalIndent(snap, "", "  ")
		userMessage := fmt.Sprintf(`## AlertManager Notification

### Alert Summary
%s

### Current System Snapshot
`+"```json"+`
%s
`+"```"+`

## Task
Analyze these alerts in context of the current system state.
For each alert, determine:
1. Is this a genuine issue or a false alarm?
2. What is the likely root cause?
3. What action should be taken?
4. Priority: CRITICAL / WARNING / INFO

Then provide an overall system health assessment.`, alertSummary, string(snapJSON))

		systemPrompt := ai.BuildSystemPrompt(ai.PromptContext{AgentType: "sre"})
		ch := ai.StreamChat(ctx, systemPrompt, []ai.ChatMessage{
			{Role: "user", Content: userMessage},
		})

		var result strings.Builder
		for chunk := range ch {
			if chunk.Error != "" {
				diagCh <- diagResult{err: fmt.Errorf("%s", chunk.Error)}
				return
			}
			if chunk.Done {
				diagCh <- diagResult{text: result.String()}
				return
			}
			result.WriteString(chunk.Token)
		}
		diagCh <- diagResult{text: result.String()}
	}()

	// 等待诊断结果或超时
	select {
	case res := <-diagCh:
		if res.err != nil {
			log.Printf("[aiops] diagnosis error: %v", res.err)
			c.JSON(http.StatusOK, alertWebhookResponse{
				Status:     "acknowledged",
				AlertCount: len(payload.Alerts),
				Snapshot:   snap,
			})
			return
		}
		c.JSON(http.StatusOK, alertWebhookResponse{
			Status:      "diagnosed",
			AIDiagnosis: res.text,
			AlertCount:  len(payload.Alerts),
			Snapshot:    snap,
		})
	case <-time.After(65 * time.Second):
		c.JSON(http.StatusOK, alertWebhookResponse{
			Status:     "diagnosis_timeout",
			AlertCount: len(payload.Alerts),
			Snapshot:   snap,
		})
	}
}

// buildAlertSummary 将 AlertManager 的告警格式化为可读的摘要文本。
func buildAlertSummary(payload alertManagerWebhook) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Status: %s | Group: %s\n", payload.Status, payload.GroupKey))
	for _, alert := range payload.Alerts {
		name := alert.Labels["alertname"]
		severity := alert.Labels["severity"]
		summary := alert.Annotations["summary"]
		description := alert.Annotations["description"]
		b.WriteString(fmt.Sprintf("- [%s] %s", severity, name))
		if summary != "" {
			b.WriteString(": " + summary)
		}
		if description != "" {
			b.WriteString(" — " + truncateString(description, 200))
		}
		b.WriteString("\n")
	}
	return b.String()
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
