package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"judgex/internal/ai"
	"judgex/internal/config"
	"judgex/internal/database"
	"judgex/internal/judge"
	"judgex/internal/model"
	"judgex/internal/storage"
)

// ============================================================================
// AI Debug Agent — 智能调试代理
// ============================================================================
//
// Debug Agent 是一个 7 步骤的全自动调试流水线，帮助用户找出代码中的错误。
// 它与普通的 AI 聊天不同——它实际运行代码、对比输出、调用 LLM 分析，然后验证修复。
//
// 完整 7 步流程：
//
//   Step 1: 加载题目信息（标题、描述、样例、标签）
//   Step 2: 加载用户最近的 5 次提交记录（用于上下文）
//   Step 3: 加载隐藏测试数据（磁盘 → S3 → MySQL 降级）
//   Step 4: 逐条运行用户代码（限制最多 10 个测试点）
//   Step 5: 将测试结果发送给 LLM，让 AI 分析失败原因并生成修复代码
//   Step 6: 从 LLM 回复中提取代码块（通过 markdown 代码块正则匹配）
//   Step 7: 验证修复后的代码——重新运行所有测试点并报告结果
//
// SSE 事件类型：
//   status        — 当前步骤的状态文本（中英文）
//   test_results  — 原始代码的测试结果（JSON）
//   token         — LLM 流式输出的一个 token
//   fix           — AI 生成的修复代码
//   verification  — 修复代码的验证结果（JSON）
//   error         — 错误信息
//   done          — 流程结束标记

const debugStreamTimeout = 120 * time.Second // 整个调试流程的超时时间（2分钟）
const maxDebugCases = 10                     // 最多运行 10 个测试点（防止耗时过长）

// debugRequest 是 AI 调试接口的请求体。
type debugRequest struct {
	ProblemID uint   `json:"problem_id" binding:"required"` // 题目 ID
	Language  string `json:"language" binding:"required"`   // 编程语言（cpp, python, java, go）
	Code      string `json:"code" binding:"required"`       // 用户提交的源代码
}

// debugTestResult 记录单个测试点的执行结果。
type debugTestResult struct {
	CaseID   int    `json:"case_id"`   // 测试点编号（从 1 开始）
	Input    string `json:"input"`     // 输入数据（截断到 500 字符）
	Expected string `json:"expected"`  // 期望输出（截断到 500 字符）
	Actual   string `json:"actual"`    // 实际输出（截断到 500 字符）
	Passed   bool   `json:"passed"`    // 是否通过
	Status   string `json:"status"`    // 判题状态（Accepted, TLE, WA 等）
	TimeUsed int    `json:"time_used"` // 耗时（ms）
	ErrorMsg string `json:"error_msg"` // 错误信息
}

// localTestCase 是 worker.testCaseDisk 的副本，用于本 handler 包内。
// 因为 handler 不能引用 worker 包（循环依赖风险），所以复制了一份。
type localTestCase struct {
	Input    string
	Expected string
}

// DebugHandler 处理 POST /api/ai/debug。
// 这是 AI Debug Agent 的入口，执行完整的 7 步调试流程。
func DebugHandler(c *gin.Context) {
	var req debugRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID := c.MustGet("user_id").(uint)

	// ================================================================
	// SSE 连接建立
	// ================================================================
	// 设置必要的 SSE 头。SSE 是基于 HTTP 长连接的单向推送协议，
	// 前端通过 EventSource API 消费。
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Status(http.StatusOK)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return
	}

	// writeSSE 发送一个 SSE 事件。
	// event 参数指定事件类型（如 "status", "token", "error", "done"），
	// data 参数是事件的数据内容。
	// 使用空 event 发送未命名事件（data: xxx\n\n）。
	writeSSE := func(event, data string) {
		if event != "" {
			_, _ = fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, data)
		} else {
			_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", data)
		}
		flusher.Flush()
	}

	// 带超时的 context，整个调试流程不能超过 2 分钟
	ctx, cancel := context.WithTimeout(c.Request.Context(), debugStreamTimeout)
	defer cancel()

	// ================================================================
	// Step 1: 加载题目信息
	// ================================================================
	writeSSE("status", "正在加载题目信息...")

	var problem model.Problem
	if err := database.DB.Preload("Tags").First(&problem, req.ProblemID).Error; err != nil {
		writeSSE("error", `{"message":"题目未找到"}`)
		writeSSE("done", "")
		return
	}

	sampleCasesStr := formatSampleCases(problem.SampleCases)

	// ================================================================
	// Step 2: 加载用户最近的提交记录
	// ================================================================
	writeSSE("status", "正在加载提交记录...")

	var recentSubs []model.Submission
	database.DB.Where("user_id = ? AND problem_id = ?", userID, req.ProblemID).
		Order("id DESC").Limit(5).Find(&recentSubs)

	recentSubsStr := formatRecentSubmissions(recentSubs)

	// ================================================================
	// Step 3: 加载测试数据
	// ================================================================
	// 按优先级依次尝试：S3/MinIO → 本地磁盘 → MySQL test_cases 表
	writeSSE("status", "正在加载测试数据...")

	tcs, err := loadTestCasesForDebug(req.ProblemID)
	if err != nil {
		writeSSE("error", fmt.Sprintf(`{"message":"加载测试数据失败: %s"}`, err.Error()))
		writeSSE("done", "")
		return
	}

	// 限制测试点数量（最多 10 个），防止调试耗时过长
	if len(tcs) > maxDebugCases {
		tcs = tcs[:maxDebugCases]
	}

	if len(tcs) == 0 {
		writeSSE("error", `{"message":"没有找到测试数据"}`)
		writeSSE("done", "")
		return
	}

	// ================================================================
	// Step 4: 运行用户代码
	// ================================================================
	writeSSE("status", fmt.Sprintf("正在评测用户代码 (%d 个测试点)...", len(tcs)))

	var testResults []debugTestResult
	for i, tc := range tcs {
		select {
		case <-ctx.Done():
			writeSSE("error", `{"message":"调试超时"}`)
			writeSSE("done", "")
			return
		default:
		}

		// 在沙箱中运行用户代码
		result := judge.Run(req.Language, req.Code, tc.Input, problem.TimeLimit, problem.MemoryLimit)

		// 判定结果：状态必须为 Accepted 且输出匹配期望
		passed := false
		if result.Status == judge.StatusAccepted {
			if err := judge.CompareOutput(tc.Expected, result.Output); err == nil {
				passed = true
			}
		}

		testResults = append(testResults, debugTestResult{
			CaseID:   i + 1,
			Input:    truncateForDisplay(tc.Input, 500),
			Expected: truncateForDisplay(tc.Expected, 500),
			Actual:   truncateForDisplay(result.Output, 500),
			Passed:   passed,
			Status:   result.Status,
			TimeUsed: result.TimeUsed,
			ErrorMsg: result.ErrorMsg,
		})
	}

	// 统计通过数
	passedCount := 0
	for _, tr := range testResults {
		if tr.Passed {
			passedCount++
		}
	}

	// 发送测试结果给前端（用于展示每个测试点的详细情况）
	testCtxJSON, _ := json.Marshal(gin.H{
		"total":        len(testResults),
		"passed":       passedCount,
		"test_results": testResults,
	})
	writeSSE("test_results", string(testCtxJSON))

	// 如果全部通过，提前结束（不需要 AI 分析）
	if passedCount == len(testResults) {
		writeSSE("status", "所有测试点已通过！无需修复。")
		writeSSE("done", "")
		return
	}

	// ================================================================
	// Step 5: AI 分析错误原因
	// ================================================================
	writeSSE("status", "AI 正在分析错误原因...")

	// 组装 LLM 上下文，包含题目信息、代码、测试结果
	promptCtx := ai.PromptContext{
		AgentType:          "debug",
		ProblemTitle:       problem.Title,
		ProblemDescription: problem.Description,
		ProblemTimeLimit:   problem.TimeLimit,
		ProblemMemoryLimit: problem.MemoryLimit,
		SampleCases:        sampleCasesStr,
		SubmissionLanguage: req.Language,
		SubmissionCode:     req.Code,
		SubmissionStatus:   "Failed",
		PassedCount:        passedCount,
		TotalCases:         len(testResults),
		RecentSubmissions:  recentSubsStr,
	}

	// 填充每个测试点的详细结果
	for _, tr := range testResults {
		promptCtx.TestCaseResults = append(promptCtx.TestCaseResults, ai.TestCaseResult{
			CaseID:   tr.CaseID,
			Input:    tr.Input,
			Expected: tr.Expected,
			Actual:   tr.Actual,
			Passed:   tr.Passed,
			Status:   tr.Status,
			TimeUsed: tr.TimeUsed,
			ErrorMsg: tr.ErrorMsg,
		})
	}

	systemPrompt := ai.BuildSystemPrompt(promptCtx)
	messages := []ai.ChatMessage{
		{Role: "user", Content: fmt.Sprintf(
			"我的 %s 代码通过了 %d/%d 个测试点。请分析失败原因并生成修复后的完整代码。",
			req.Language, passedCount, len(testResults))},
	}

	// 调用 LLM（带 60 秒超时）
	llmCtx, llmCancel := context.WithTimeout(ctx, 60*time.Second)
	defer llmCancel()

	ch := ai.StreamChat(llmCtx, systemPrompt, messages)

	var fullResponse strings.Builder
	for chunk := range ch {
		if chunk.Error != "" {
			log.Printf("[debug] LLM error: %s", chunk.Error)
			writeSSE("error", fmt.Sprintf(`{"message":"AI 分析失败: %s"}`, chunk.Error))
			writeSSE("done", "")
			return
		}
		if chunk.Done {
			break
		}
		fullResponse.WriteString(chunk.Token)
		writeSSE("token", chunk.Token)
	}

	responseText := fullResponse.String()

	// ================================================================
	// Step 6: 从 LLM 回复中提取修复后的代码
	// ================================================================
	writeSSE("status", "正在提取修复后的代码...")

	// extractCodeBlock 从 markdown 中提取 ```language ... ``` 代码块
	fixedCode := extractCodeBlock(responseText, req.Language)
	if fixedCode == "" {
		writeSSE("status", "AI 未生成修复代码，显示分析结果。")
		writeSSE("done", "")
		return
	}

	// 将修复代码推送给前端（用于在编辑器中显示差异）
	writeSSE("fix", fixedCode)

	// ================================================================
	// Step 7: 验证修复代码
	// ================================================================
	writeSSE("status", "正在验证修复后的代码...")

	var verificationResults []debugTestResult
	for i, tc := range tcs {
		select {
		case <-ctx.Done():
			writeSSE("error", `{"message":"验证超时"}`)
			writeSSE("done", "")
			return
		default:
		}

		// 运行修复后的代码
		result := judge.Run(req.Language, fixedCode, tc.Input, problem.TimeLimit, problem.MemoryLimit)

		passed := false
		if result.Status == judge.StatusAccepted {
			if err := judge.CompareOutput(tc.Expected, result.Output); err == nil {
				passed = true
			}
		}

		verificationResults = append(verificationResults, debugTestResult{
			CaseID:   i + 1,
			Input:    truncateForDisplay(tc.Input, 500),
			Expected: truncateForDisplay(tc.Expected, 500),
			Actual:   truncateForDisplay(result.Output, 500),
			Passed:   passed,
			Status:   result.Status,
			TimeUsed: result.TimeUsed,
			ErrorMsg: result.ErrorMsg,
		})
	}

	// 统计修复结果
	verifyPassed := 0
	for _, vr := range verificationResults {
		if vr.Passed {
			verifyPassed++
		}
	}

	verifyJSON, _ := json.Marshal(gin.H{
		"total":        len(verificationResults),
		"passed":       verifyPassed,
		"test_results": verificationResults,
	})
	writeSSE("verification", string(verifyJSON))

	if verifyPassed == len(verificationResults) {
		writeSSE("status", fmt.Sprintf("✅ 修复成功！所有 %d 个测试点已通过。", verifyPassed))
	} else {
		writeSSE("status", fmt.Sprintf("⚠️ 修复后通过了 %d/%d 个测试点，仍有问题需要继续修复。", verifyPassed, len(verificationResults)))
	}

	writeSSE("done", "")
}

// ============================================================================
// 测试数据加载（handler 包本地版本）
// ============================================================================

// loadTestCasesForDebug 加载测试数据，供 Debug Agent 使用。
// 优先级：S3/MinIO → 本地文件系统 → MySQL test_cases 表（降级）
func loadTestCasesForDebug(problemID uint) ([]localTestCase, error) {
	if storage.Default != nil {
		if tcs, err := readTestCasesFromStorage(problemID); err == nil && len(tcs) > 0 {
			return tcs, nil
		}
	}
	// 尝试从本地文件系统读取
	tcs, err := readTestCasesFromFilesystem(problemID)
	if err == nil && len(tcs) > 0 {
		return tcs, nil
	}
	// 降级到 MySQL 旧表
	return readTestCasesFromDB(problemID)
}

// readTestCasesFromDB 从 MySQL test_cases 表读取测试数据（降级方案）。
func readTestCasesFromDB(problemID uint) ([]localTestCase, error) {
	type dbCase struct {
		Input    string
		Expected string
	}
	var cases []dbCase
	if err := database.DB.Table("test_cases").
		Where("problem_id = ?", problemID).
		Order("id ASC").
		Find(&cases).Error; err != nil {
		return nil, err
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("no test cases found for problem %d", problemID)
	}
	result := make([]localTestCase, len(cases))
	for i, tc := range cases {
		result[i] = localTestCase{Input: tc.Input, Expected: tc.Expected}
	}
	return result, nil
}

// readTestCasesFromStorage 从 S3/MinIO 读取测试数据。
func readTestCasesFromStorage(problemID uint) ([]localTestCase, error) {
	files, err := storage.Default.ListTestCases(problemID)
	if err != nil || len(files) == 0 {
		return nil, fmt.Errorf("no files for problem %d", problemID)
	}

	caseNums := make(map[int]bool)
	for _, f := range files {
		name := f.Name
		if strings.HasSuffix(name, ".in") {
			if num, err := strconv.Atoi(strings.TrimSuffix(name, ".in")); err == nil {
				caseNums[num] = true
			}
		}
	}

	if len(caseNums) == 0 {
		return nil, fmt.Errorf("no .in files found for problem %d", problemID)
	}

	sorted := make([]int, 0, len(caseNums))
	for num := range caseNums {
		sorted = append(sorted, num)
	}
	sort.Ints(sorted)

	result := make([]localTestCase, 0, len(sorted))
	for _, num := range sorted {
		inName := strconv.Itoa(num) + ".in"
		outName := strconv.Itoa(num) + ".out"

		inData, err := storage.Default.ReadTestCase(problemID, inName)
		if err != nil {
			return nil, fmt.Errorf("failed to read case %d input: %v", num, err)
		}
		outData, err := storage.Default.ReadTestCase(problemID, outName)
		if err != nil {
			return nil, fmt.Errorf("failed to read case %d output: %v", num, err)
		}

		result = append(result, localTestCase{
			Input:    string(inData),
			Expected: string(outData),
		})
	}

	return result, nil
}

// readTestCasesFromFilesystem 从本地文件系统读取测试数据。
func readTestCasesFromFilesystem(problemID uint) ([]localTestCase, error) {
	testDataPath := config.Load().TestDataPath

	testDir := filepath.Join(testDataPath, strconv.FormatUint(uint64(problemID), 10))

	entries, err := os.ReadDir(testDir)
	if err != nil {
		return nil, err
	}

	caseNums := make(map[int]bool)
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".in") {
			if num, err := strconv.Atoi(strings.TrimSuffix(name, ".in")); err == nil {
				caseNums[num] = true
			}
		}
	}

	if len(caseNums) == 0 {
		return nil, fmt.Errorf("no test cases found for problem %d", problemID)
	}

	sorted := make([]int, 0, len(caseNums))
	for num := range caseNums {
		sorted = append(sorted, num)
	}
	sort.Ints(sorted)

	result := make([]localTestCase, 0, len(sorted))
	for _, num := range sorted {
		inPath := filepath.Join(testDir, strconv.Itoa(num)+".in")
		outPath := filepath.Join(testDir, strconv.Itoa(num)+".out")

		inData, err := os.ReadFile(inPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read case %d input: %v", num, err)
		}
		outData, err := os.ReadFile(outPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read case %d output: %v", num, err)
		}

		result = append(result, localTestCase{
			Input:    string(inData),
			Expected: string(outData),
		})
	}

	return result, nil
}

// ============================================================================
// 辅助函数
// ============================================================================

// formatSampleCases 将题目中的 sample_cases JSON 格式化为可读文本。
func formatSampleCases(sampleCases json.RawMessage) string {
	if sampleCases == nil {
		return "（无）"
	}
	var cases []map[string]string
	if err := json.Unmarshal(sampleCases, &cases); err != nil {
		return "（无）"
	}
	var b strings.Builder
	for i, tc := range cases {
		b.WriteString(fmt.Sprintf("样例 %d:\n输入:\n%s\n输出:\n%s\n\n", i+1, tc["input"], tc["output"]))
	}
	if b.Len() == 0 {
		return "（无）"
	}
	return b.String()
}

// formatRecentSubmissions 将最近的提交记录格式化为文本（供 LLM 上下文使用）。
func formatRecentSubmissions(subs []model.Submission) string {
	if len(subs) == 0 {
		return "（无最近提交）"
	}
	var b strings.Builder
	for _, s := range subs {
		b.WriteString(fmt.Sprintf("- 提交 #%d | %s | %s | %d/%d 测试点 | %s\n",
			s.ID, s.Language, s.Status, s.PassedCount, s.TotalCases, s.CreatedAt.Format("2006-01-02 15:04:05")))
	}
	return b.String()
}

// extractCodeBlock 从 LLM 的 markdown 回复中提取代码块。
// 匹配模式：```language ... ``` 或 ``` ... ```
// 先尝试匹配指定语言（如 ```go），再尝试通用 ```。
func extractCodeBlock(response string, language string) string {
	markers := []string{
		"```" + language,
		"```",
	}

	for _, marker := range markers {
		start := strings.Index(response, marker)
		if start == -1 {
			continue
		}
		start += len(marker)
		if start < len(response) && response[start] == '\n' {
			start++
		}
		end := strings.Index(response[start:], "```")
		if end == -1 {
			continue
		}
		code := response[start : start+end]
		code = strings.TrimSpace(code)
		if code != "" {
			return code
		}
	}

	return ""
}

// truncateForDisplay 截断字符串用于显示。
// 长文本只保留前 maxLen 个字符，后面加上截断提示。
func truncateForDisplay(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\n...(truncated)"
}
