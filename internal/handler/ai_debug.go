package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
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
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Status(http.StatusOK)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return
	}

	writeSSE := func(event, data string) {
		if event != "" {
			_, _ = fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, data)
		} else {
			_, _ = fmt.Fprintf(c.Writer, "data: %s\n\n", data)
		}
		flusher.Flush()
	}

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
	writeSSE("status", "正在加载测试数据...")

	tcs, err := loadTestCasesForDebug(req.ProblemID)
	if err != nil {
		writeSSE("error", fmt.Sprintf(`{"message":"加载测试数据失败: %s"}`, err.Error()))
		writeSSE("done", "")
		return
	}

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

		result := judge.Run(req.Language, req.Code, tc.Input, problem.TimeLimit, problem.MemoryLimit)

		passed := false
		if result.Status == judge.StatusAccepted {
			if err := judge.CompareOutput(tc.Expected, result.Output); err == nil {
				passed = true
			}
		}

		displayStatus := result.Status
		if !passed && displayStatus == judge.StatusAccepted {
			displayStatus = judge.StatusWrongAnswer
		}

		testResults = append(testResults, debugTestResult{
			CaseID:   i + 1,
			Input:    truncateForDisplay(tc.Input, 500),
			Expected: truncateForDisplay(tc.Expected, 500),
			Actual:   truncateForDisplay(result.Output, 500),
			Passed:   passed,
			Status:   displayStatus,
			TimeUsed: result.TimeUsed,
			ErrorMsg: result.ErrorMsg,
		})
	}

	passedCount := 0
	for _, tr := range testResults {
		if tr.Passed {
			passedCount++
		}
	}

	testCtxJSON, _ := json.Marshal(gin.H{
		"total":        len(testResults),
		"passed":       passedCount,
		"test_results": testResults,
	})
	writeSSE("test_results", string(testCtxJSON))

	if passedCount == len(testResults) {
		writeSSE("status", "所有测试点已通过！无需修复。")
		writeSSE("done", "")
		return
	}

	// ================================================================
	// Step 5: AI 分析错误原因
	// ================================================================
	writeSSE("status", "AI 正在分析错误原因...")

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
	}

	responseText := fullResponse.String()

	// 清理分析文本：去掉 [PROBLEM_QUALITY] 区块，不展示给用户
	cleanedText := stripProblemQualityBlock(responseText)
	writeSSE("token", cleanedText)

	// ================================================================
	// AI 题目质量反馈（从 LLM 回复中解析 [PROBLEM_QUALITY] 区块）
	// ================================================================
	if saveProblemFeedback(responseText, req.ProblemID, userID, 0) {
		// AI 确认了题目数据有问题 → 停止修复流程，直接通知用户和管理员
		writeSSE("status", "AI 分析发现题目测试数据或描述可能有问题，已自动通知管理员。请等待修正后重新提交。")
		writeSSE("done", "")
		return
	}

	// ================================================================
	// Step 6: 从 LLM 回复中提取修复后的代码
	// ================================================================
	writeSSE("status", "正在提取修复后的代码...")

	fixedCode := extractCodeBlock(responseText, req.Language)
	if fixedCode == "" {
		writeSSE("status", "AI 分析完成，但未能提取出修复代码。请查看上方「AI 分析报告」中的错误原因和建议。")
		writeSSE("done", "")
		return
	}

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

		result := judge.Run(req.Language, fixedCode, tc.Input, problem.TimeLimit, problem.MemoryLimit)

		passed := false
		if result.Status == judge.StatusAccepted {
			if err := judge.CompareOutput(tc.Expected, result.Output); err == nil {
				passed = true
			}
		}

		displayStatus := result.Status
		if !passed && displayStatus == judge.StatusAccepted {
			displayStatus = judge.StatusWrongAnswer
		}

		verificationResults = append(verificationResults, debugTestResult{
			CaseID:   i + 1,
			Input:    truncateForDisplay(tc.Input, 500),
			Expected: truncateForDisplay(tc.Expected, 500),
			Actual:   truncateForDisplay(result.Output, 500),
			Passed:   passed,
			Status:   displayStatus,
			TimeUsed: result.TimeUsed,
			ErrorMsg: result.ErrorMsg,
		})
	}

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

func loadTestCasesForDebug(problemID uint) ([]localTestCase, error) {
	if storage.Default != nil {
		if tcs, err := readTestCasesFromStorage(problemID); err == nil && len(tcs) > 0 {
			return tcs, nil
		}
	}
	tcs, err := readTestCasesFromFilesystem(problemID)
	if err == nil && len(tcs) > 0 {
		return tcs, nil
	}
	return readTestCasesFromDB(problemID)
}

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

func formatRecentSubmissions(subs []model.Submission) string {
	if len(subs) == 0 {
		return "（无最近提交）"
	}
	var b strings.Builder
	for _, s := range subs {
		failInfo := ""
		if s.Status != "Accepted" && s.Status != "Partial Score" && s.PassedCount < s.TotalCases {
			failInfo = fmt.Sprintf(" (止于 case %d)", s.PassedCount+1)
		}
		b.WriteString(fmt.Sprintf("- 提交 #%d | %s | %s | %d/%d 测试点%s | %s\n",
			s.ID, s.Language, s.Status, s.PassedCount, s.TotalCases, failInfo, s.CreatedAt.Format("2006-01-02 15:04:05")))
	}
	return b.String()
}

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

func truncateForDisplay(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\n...(truncated)"
}

// stripProblemQualityBlock 从 AI 回复中去掉 [PROBLEM_QUALITY] 区块及其内容，
// 避免用户看到 k=v 格式的内部标记。
func stripProblemQualityBlock(text string) string {
	// [PROBLEM_QUALITY]\nkey=value\n... 直到遇到双换行或结尾
	re := regexp.MustCompile(`(?s)\n?\[PROBLEM_QUALITY\]\n.*?(?:\n\n|$)`)
	result := re.ReplaceAllString(text, "")
	return strings.TrimSpace(result)
}

// saveProblemFeedback 从 LLM 回复中解析 [PROBLEM_QUALITY] 区块，
// 如果发现 high confidence 的题目质量问题，自动保存到数据库并返回 true。
func saveProblemFeedback(response string, problemID uint, userID uint, submissionID int64) bool {
	re := regexp.MustCompile(`(?s)\[PROBLEM_QUALITY\]\n(.*?)(?:\n\n|$)`)
	matches := re.FindStringSubmatch(response)
	if len(matches) < 2 {
		return false
	}

	body := strings.TrimSpace(matches[1])
	if body == "" {
		return false
	}

	fields := make(map[string]string)
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if idx := strings.Index(line, "="); idx > 0 {
			key := strings.TrimSpace(line[:idx])
			val := strings.TrimSpace(line[idx+1:])
			fields[key] = val
		}
	}

	if fields["confidence"] != "high" {
		return false
	}

	feedbackType := fields["feedback_type"]
	description := fields["description"]
	evidence := fields["evidence"]

	if feedbackType == "" || description == "" || evidence == "" {
		return false
	}

	feedback := model.ProblemFeedback{
		ProblemID:    problemID,
		UserID:       userID,
		SubmissionID: submissionID,
		FeedbackType: feedbackType,
		Priority:     fields["priority"],
		Description:  description,
		Evidence:     evidence,
		Confidence:   "high",
		Status:       "pending",
	}
	if feedback.Priority != "P1" {
		feedback.Priority = "P2"
	}

	if err := database.DB.Create(&feedback).Error; err != nil {
		log.Printf("[debug] failed to save problem feedback: %v", err)
		return false
	}

	log.Printf("[debug] problem feedback saved: problem=%d type=%s desc=%s",
		problemID, feedbackType, description)
	return true
}
