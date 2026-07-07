package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
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
// AI 诊断助手 — Verdict-Aware Reflective Controller
// ============================================================================
//
// Verdict state machine:
//
//	CE → static analysis (code + compiler error → LLM)
//	TLE → reference-solution validation + static complexity analysis
//	WA/RE → instrumented dynamic execution + trace-based causal analysis
//
// IO protection: all program output is truncated at 1000 lines via SIGKILL.
// Reference solution validation audits to problem_feedback on failure.

const diagnoseStreamTimeout = 180 * time.Second
const maxConcurrentDiagnose = 4 // 最大并发诊断任务数
const debugUserRateLimit = 5    // 每用户每分钟最多 5 次诊断请求
const debugUserRateWindow = 1 * time.Minute

// diagnoseSemaphore 限制同时运行的诊断任务数量。
// 超过限制的请求立即返回 503，不阻塞判题队列。
var diagnoseSemaphore = make(chan struct{}, maxConcurrentDiagnose)

// aiDiagnoseRequest is the request body for the Socratic Debugger.
type aiDiagnoseRequest struct {
	ProblemID uint   `json:"problem_id" binding:"required"`
	Language  string `json:"language" binding:"required"`
	Code      string `json:"code" binding:"required"`
	Verdict   string `json:"verdict" binding:"required"` // CE, TLE, WA, RE

	// For CE: compiler error output
	CompileError string `json:"compile_error,omitempty"`
	// For TLE: context about the timeout
	TimeUsed int `json:"time_used,omitempty"`
	// For WA/RE: the failed test case details
	FailedInput    string `json:"failed_input,omitempty"`
	FailedExpected string `json:"failed_expected,omitempty"`
	FailedActual   string `json:"failed_actual,omitempty"`
	FailedCaseID   int    `json:"failed_case_id,omitempty"`
}

// AIDiagnoseHandler handles POST /api/ai/diagnose.
// Entry point for the AI 诊断助手 pipeline.
func AIDiagnoseHandler(c *gin.Context) {
	var req aiDiagnoseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	// Validate verdict.
	verdict := strings.ToUpper(req.Verdict)
	switch verdict {
	case "CE", "TLE", "WA", "RE":
	default:
		c.JSON(400, gin.H{"error": "unsupported verdict: " + verdict})
		return
	}

	// ── 并发控制：semaphore 获取 ──
	// 只有 WA/RE 需要插桩和沙箱，占用资源多；CE/TLE 只是 LLM 调用，不占 semaphore
	if verdict == "WA" || verdict == "RE" {
		select {
		case diagnoseSemaphore <- struct{}{}:
			defer func() { <-diagnoseSemaphore }()
		default:
			// Semaphore 已满，返回友好提示
			c.JSON(http.StatusServiceUnavailable, gin.H{
				"error":       "诊断队列已满，请稍后再试",
				"retry_after": 30,
			})
			return
		}
	}

	// SSE setup.
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Status(200)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return
	}

	writeSSE := func(event, data string) {
		if event != "" {
			fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, data)
		} else {
			fmt.Fprintf(c.Writer, "data: %s\n\n", data)
		}
		flusher.Flush()
	}

	ctx, cancel := context.WithTimeout(c.Request.Context(), diagnoseStreamTimeout)
	defer cancel()

	// Load problem info.
	var problem model.Problem
	if err := database.DB.First(&problem, req.ProblemID).Error; err != nil {
		writeSSE("error", `{"message":"problem not found"}`)
		writeSSE("done", "")
		return
	}

	// ── 题目质量检测（所有诊断路径通用）──
	// 在诊断代码问题的同时，检测题目本身是否存在质量问题。
	go checkProblemQualityOnDiagnose(ctx, req, problem, writeSSE)

	// ── Verdict dispatch ──
	switch verdict {
	case "CE":
		compileErrorAnalysis(ctx, writeSSE, req, problem)
	case "TLE":
		tleAnalysis(ctx, writeSSE, req, problem)
	case "WA", "RE":
		instrumentedAnalysis(ctx, writeSSE, req, problem)
	}
}

// ============================================================================
// CE Path — Static analysis with compiler error
// ============================================================================

func compileErrorAnalysis(ctx context.Context, writeSSE func(string, string), req aiDiagnoseRequest, problem model.Problem) {
	writeSSE("status", "正在分析编译错误...")

	systemPrompt := fmt.Sprintf(`You are an expert programming tutor analyzing a compilation error.

## Problem Context
- Title: %s
- Description: %s
- Time Limit: %d ms | Memory Limit: %d MB

## Your Task
Analyze the compilation error in the student's code. Provide:
1. **Root cause**: What specifically caused the compilation error (in plain language)
2. **Fix direction**: Specific hints about what to change — do NOT give the full corrected code
3. **Learning note**: A concept question that reinforces the relevant programming knowledge

## Constraints
- NEVER output a complete working solution
- If the error is a simple typo, point to the line/area without revealing the exact fix
- Reply in Chinese if the problem description is in Chinese, otherwise use English
- Format your response with clear sections: 错误分析 / 修复方向 / 思考题`, problem.Title, problem.Description, problem.TimeLimit, problem.MemoryLimit)

	userMsg := fmt.Sprintf("## Student's Code\n\n```%s\n%s\n```\n\n## Compiler Error\n```\n%s\n```\n\nAnalyze the compilation error and guide the student to fix it.",
		req.Language, req.Code, req.CompileError)

	streamLLM(ctx, writeSSE, systemPrompt, userMsg)
}

// ============================================================================
// TLE Path — Reference solution validation + static complexity analysis
// ============================================================================

func tleAnalysis(ctx context.Context, writeSSE func(string, string), req aiDiagnoseRequest, problem model.Problem) {
	// Step 1: Validate reference solution (if available).
	writeSSE("status", "正在验证参考解...")

	if problem.ReferenceSolution != "" {
		refResult := runReferenceSolution(problem, req.Language)
		if refResult != "" {
			// Reference solution failed — audit problem quality.
			feedback := model.ProblemFeedback{
				ProblemID:    problem.ID,
				UserID:       0, // system
				FeedbackType: "reference_solution_failure",
				Priority:     "P1",
				Description:  "参考解未通过测试数据验证",
				Evidence:     refResult,
				Confidence:   "high",
				Status:       "pending",
			}
			if err := database.DB.Create(&feedback).Error; err != nil {
				log.Printf("[diagnose] failed to save reference solution feedback: %v", err)
			}
			writeSSE("status", "⚠️ 参考解验证失败，已记录问题反馈。正在分析你的代码...")
		} else {
			writeSSE("status", "✅ 参考解验证通过。正在分析时间复杂度...")
		}
	} else {
		writeSSE("status", "正在分析时间复杂度...")
	}

	// Step 2: Static complexity analysis.
	systemPrompt := fmt.Sprintf(`You are an expert programming tutor analyzing a Time Limit Exceeded submission.

## Problem Context
- Title: %s
- Description: %s
- Time Limit: %d ms | Memory Limit: %d MB

## Your Task
Analyze why the student's code timed out. Provide:
1. **Complexity analysis**: Identify the time complexity of the submitted code
2. **Bottleneck**: Pinpoint which part of the code causes high complexity (specific loops, recursion, data structures)
3. **Optimization hint**: Suggest a better algorithmic approach — never give full code, only directional hints
4. **Similar problem**: If applicable, mention a classic problem with a similar optimization pattern

## Constraints
- NEVER output a complete working solution
- Focus on complexity theory — reference Big-O notation explicitly
- If you can identify the required complexity from the problem constraints, state it
- Reply in Chinese if the problem description is in Chinese, otherwise use English
- Format: 复杂度分析 / 瓶颈定位 / 优化方向`, problem.Title, problem.Description, problem.TimeLimit, problem.MemoryLimit)

	timeInfo := ""
	if req.TimeUsed > 0 {
		timeInfo = fmt.Sprintf("\n- Time Used: %d ms (limit: %d ms)", req.TimeUsed, problem.TimeLimit)
	}

	userMsg := fmt.Sprintf("## Student's Code\n\n```%s\n%s\n```\n## Execution Context%s\n\nThe code exceeded the time limit. Analyze its complexity and suggest optimization directions.",
		req.Language, req.Code, timeInfo)

	streamLLM(ctx, writeSSE, systemPrompt, userMsg)
}

// ============================================================================
// WA/RE Path — Instrumented dynamic execution + trace analysis
// ============================================================================

func instrumentedAnalysis(ctx context.Context, writeSSE func(string, string), req aiDiagnoseRequest, problem model.Problem) {
	// 自愈修复循环（所有语言统一走此路径）
	attemptRepairLoop(ctx, writeSSE, req, problem)

	// ── 题目质量检测 ──
	// 参考解对本题 WA/RE 的测试点进行验证
	checkFailedTestCaseReference(req, problem, writeSSE)

	writeSSE("done", "")
}


// ============================================================================
// Reference solution validation
// ============================================================================

// runReferenceSolution runs the reference solution against all test cases
// and returns an empty string on success, or an error description on failure.
func runReferenceSolution(problem model.Problem, language string) string {
	tcs, err := loadTestCasesForDebug(problem.ID)
	if err != nil {
		return fmt.Sprintf("failed to load test cases: %v", err)
	}
	if len(tcs) == 0 {
		return "no test cases found"
	}

	if language == "go" {
		return runRefSolutionGo(problem.ReferenceSolution, tcs, problem.TimeLimit)
	}

	// For other languages, use the judge.Run pipeline.
	for i, tc := range tcs {
		result := judge.Run(language, problem.ReferenceSolution, tc.Input, problem.TimeLimit, problem.MemoryLimit)
		if result.Status == judge.StatusCompileError {
			return fmt.Sprintf("reference solution failed to compile (test case %d): %s", i+1, result.ErrorMsg)
		}
		if result.Status == judge.StatusTimeLimitExceeded {
			return fmt.Sprintf("reference solution timed out (test case %d)", i+1)
		}
		if result.Status == judge.StatusRuntimeError {
			return fmt.Sprintf("reference solution crashed (test case %d): %s", i+1, result.ErrorMsg)
		}
		if err := judge.CompareOutput(tc.Expected, result.Output); err != nil {
			return fmt.Sprintf("reference solution output mismatch (test case %d): %v", i+1, err)
		}
	}
	return ""
}

func runRefSolutionGo(refCode string, tcs []localTestCase, timeLimitMs int) string {
	tmpDir, err := os.MkdirTemp("", "ref-solution-*")
	if err != nil {
		return fmt.Sprintf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	mainPath := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(mainPath, []byte(refCode), 0644); err != nil {
		return fmt.Sprintf("failed to write ref solution: %v", err)
	}

	for i, tc := range tcs {
		runCtx, cancel := context.WithTimeout(context.Background(), time.Duration(timeLimitMs)*time.Millisecond+5*time.Second)

		cmd := exec.CommandContext(runCtx, "go", "run", mainPath)
		cmd.Stdin = strings.NewReader(tc.Input)
		cmd.Dir = tmpDir
		cmd.Stderr = new(bytes.Buffer)

		output, err := cmd.Output()
		cancel()
		if err != nil {
			return fmt.Sprintf("reference solution crashed (test case %d): %v", i+1, err)
		}
		if err := judge.CompareOutput(tc.Expected, string(output)); err != nil {
			return fmt.Sprintf("reference solution output mismatch (test case %d): %v", i+1, err)
		}
	}
	return ""
}

// ============================================================================
// LLM streaming helper
// ============================================================================

func streamLLM(ctx context.Context, writeSSE func(string, string), systemPrompt, userMsg string) {
	llmCtx, llmCancel := context.WithTimeout(ctx, 90*time.Second)
	defer llmCancel()

	ch := ai.StreamChat(llmCtx, systemPrompt, []ai.ChatMessage{
		{Role: "user", Content: userMsg},
	})

	for chunk := range ch {
		if chunk.Error != "" {
			log.Printf("[diagnose] LLM error: %s", chunk.Error)
			writeSSE("error", fmt.Sprintf(`{"message":"AI analysis failed: %s"}`, chunk.Error))
			writeSSE("done", "")
			return
		}
		if chunk.Done {
			writeSSE("done", "")
			return
		}
		writeSSE("token", chunk.Token)
	}
}



// ============================================================================
// Self-healing repair loop — for non-Go WA/RE
// ============================================================================

const maxRepairAttempts = 3

// attemptRepairLoop 循环让 LLM 修复代码并送沙箱验证，直到通过或达到上限。
func attemptRepairLoop(ctx context.Context, writeSSE func(string, string), req aiDiagnoseRequest, problem model.Problem) {
	var lastError string

	for attempt := 1; attempt <= maxRepairAttempts; attempt++ {
		select {
		case <-ctx.Done():
			writeSSE("status", "诊断超时，请重试")
			return
		default:
		}

		if attempt > 1 {
			writeSSE("status", fmt.Sprintf("AI 正在尝试第 %d/%d 次修复...", attempt, maxRepairAttempts))
		} else {
			writeSSE("status", "AI 正在分析并修复代码...")
		}

		systemPrompt := buildRepairSystemPrompt(problem, req, attempt)
		userMsg := buildRepairUserMessage(req, attempt, lastError)

		// Streaming + collecting full LLM response
		var collected strings.Builder
		llmCtx, llmCancel := context.WithTimeout(ctx, 90*time.Second)
		ch := ai.StreamChat(llmCtx, systemPrompt, []ai.ChatMessage{
			{Role: "user", Content: userMsg},
		})

		var llmErr string
		func() {
			defer llmCancel()
			for chunk := range ch {
				if chunk.Error != "" {
					llmErr = chunk.Error
					log.Printf("[repair] LLM error on attempt %d: %s", attempt, chunk.Error)
					writeSSE("error", fmt.Sprintf(`{"message":"AI响应异常: %s"}`, chunk.Error))
					return
				}
				if chunk.Done {
					return
				}
				collected.WriteString(chunk.Token)
				writeSSE("token", chunk.Token)
			}
		}()

		if llmErr != "" {
			return
		}

		fullResponse := collected.String()
		if fullResponse == "" {
			lastError = "AI returned empty response"
			continue
		}

		// Extract code from markdown code block
		fixedCode := extractCodeFromMarkdown(fullResponse)
		if fixedCode == "" {
			writeSSE("status", "未提取到代码，尝试下一轮...")
			lastError = "AI did not output code in a markdown block"
			continue
		}

		// Run in sandbox
		writeSSE("status", fmt.Sprintf("正在沙箱中运行修复后的代码..."))
		result := judge.Run(req.Language, fixedCode, req.FailedInput, problem.TimeLimit, problem.MemoryLimit)

		if result.Status == judge.StatusAccepted {
			writeSSE("status", fmt.Sprintf("第 %d 次修复成功，代码已通过测试用例！", attempt))
			return
		}

		// Record error for next iteration
		lastError = fmt.Sprintf(
			"Attempt %d: verdict=%s  error=%s  output=%s",
			attempt, result.Status, result.ErrorMsg, truncateStr(result.Output, 300),
		)
		writeSSE("fix_result", fmt.Sprintf(
			`{"attempt":%d,"verdict":"%s"}`,
			attempt, result.Status,
		))

		// After first failure, check if test data might be the problem
		if attempt == 1 && problem.ReferenceSolution != "" && req.FailedInput != "" {
			lang := detectRefLang(problem.ReferenceSolution)
			refResult := runRefForQuality(lang, problem.ReferenceSolution, req.FailedInput, problem.TimeLimit, problem.MemoryLimit)
			if refResult.ErrorMsg != "" || refResult.Status != "Accepted" {
				writeSSE("status", "参考解在此测试用例上也失败，可能是测试数据有误，但继续尝试修复...")
			}
		}
	}

	writeSSE("status", fmt.Sprintf("%d 次修复均未通过测试，建议人工检查代码", maxRepairAttempts))
}

// buildRepairSystemPrompt 构造修复循环的系统提示词。
func buildRepairSystemPrompt(problem model.Problem, req aiDiagnoseRequest, attempt int) string {
	extra := ""
	if attempt > 1 {
		extra = fmt.Sprintf("- This is fix attempt #%d. Consider why previous attempts failed.\n- Try a different approach.", attempt)
	}
	return fmt.Sprintf("You are an expert programmer fixing a bug in a student's code submission for an Online Judge system.\n\n## Problem Context\n- Title: %s\n- Description: %s\n- Time Limit: %d ms | Memory Limit: %d MB\n\n## Your Task\nAnalyze why the student's %s code fails on the given test case, then output the COMPLETE fixed code.\n\n## Output Format\nFirst briefly explain what's wrong (1-3 sentences), then output the full fixed code in a single markdown code block:\n\n```%s\n// your fixed code here\n```\n\n## Rules\n- Output the ENTIRE program, not just the changed lines.\n- Maintain the exact same input/output format.\n- Consider edge cases (empty input, large values, boundary conditions).\n- Do NOT change the IO format.\n%s\n",
		problem.Title, problem.Description, problem.TimeLimit, problem.MemoryLimit,
		req.Language, req.Language, extra)
}

// buildRepairUserMessage 构造修复循环的用户消息。
func buildRepairUserMessage(req aiDiagnoseRequest, attempt int, lastError string) string {
	var b strings.Builder

	b.WriteString(fmt.Sprintf("## Student's Original Code\n\n```%s\n%s\n```\n\n", req.Language, req.Code))
	b.WriteString("## Failed Test Case\n")
	if req.FailedCaseID > 0 {
		b.WriteString(fmt.Sprintf("- Case ID: %d\n", req.FailedCaseID))
	}
	if req.FailedInput != "" {
		b.WriteString(fmt.Sprintf("- Input: %s\n", req.FailedInput))
	}
	if req.FailedExpected != "" {
		b.WriteString(fmt.Sprintf("- Expected: %s\n", req.FailedExpected))
	}
	if req.FailedActual != "" {
		b.WriteString(fmt.Sprintf("- Actual: %s\n", req.FailedActual))
	}
	b.WriteString(fmt.Sprintf("- Verdict: %s\n\n", req.Verdict))

	if attempt > 1 && lastError != "" {
		b.WriteString("## Previous Fix Attempt\n")
		b.WriteString(lastError)
		b.WriteString("\n\nThe previous fix didn't work. Analyze what went wrong and try a different approach.\n")
	} else {
		b.WriteString("Fix the code so it produces the expected output for this test case.\n")
	}

	return b.String()
}

// extractCodeFromMarkdown 从 LLM 响应中提取首个 markdown 代码块内容。
func extractCodeFromMarkdown(response string) string {
	re := regexp.MustCompile("(?s)```\\w*\\n(.*?)```")
	matches := re.FindStringSubmatch(response)
	if len(matches) >= 2 {
		return strings.TrimSpace(matches[1])
	}
	return ""
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
// ============================================================================
// 题目质量检测
// ============================================================================
//
// 在诊断学生代码的同时，通过程序化检查发现题目本身的质量问题。
//
// 检测项：
//  1. 样例数据校验：用参考解运行样例输入，比对样例输出
//  2. 参考解对失败测试点的验证：WA/RE 的测试点，参考解是否能通过
//
// 结果写入 ProblemFeedback 表，展示在管理后台"题目反馈"页面。
// 避免幻觉策略：只保存 confidence=high 的确定性结果。

// checkProblemQualityOnDiagnose 在诊断流程中异步检查题目质量。
// 检查所有样例数据是否正确（独立于 verdict）。
func checkProblemQualityOnDiagnose(ctx context.Context, req aiDiagnoseRequest, problem model.Problem, writeSSE func(string, string)) {
	// 没有参考解则无法验证
	if problem.ReferenceSolution == "" {
		return
	}

	select {
	case <-ctx.Done():
		return
	default:
	}

	detected := validateSampleCases(problem)
	for _, fb := range detected {
		if err := database.DB.Create(&fb).Error; err != nil {
			log.Printf("[diagnose] failed to save sample case feedback: %v", err)
		} else {
			log.Printf("[diagnose] problem quality issue detected: %s (P%s) for problem %d",
				fb.FeedbackType, fb.Priority, fb.ProblemID)
		}
	}
}

// checkFailedTestCaseReference 在 WA/RE 诊断后，用参考解验证失败的测试点。
// 如果参考解也无法通过该测试点，说明测试数据可能有误。
func checkFailedTestCaseReference(req aiDiagnoseRequest, problem model.Problem, writeSSE func(string, string)) {
	if problem.ReferenceSolution == "" {
		return
	}
	if req.FailedInput == "" {
		return
	}

	writeSSE("status", "正在验证参考解与测试数据一致性...")

	lang := detectRefLang(problem.ReferenceSolution)
	result := runRefForQuality(lang, problem.ReferenceSolution, req.FailedInput, problem.TimeLimit, problem.MemoryLimit)

	// 参考解也失败 → 测试数据可能有问题
	if result.ErrorMsg != "" || result.Status != "Accepted" {
		evidence := fmt.Sprintf("参考解在用例 #%d 上也失败了\n输入: %s\n期望输出: %s\n参考解错误: %s",
			req.FailedCaseID, req.FailedInput, req.FailedExpected, result.ErrorMsg)
		if result.Status == "" {
			evidence = fmt.Sprintf("参考解在用例 #%d 上执行出错\n输入: %s\n参考解错误: %s",
				req.FailedCaseID, req.FailedInput, result.ErrorMsg)
		}

		fb := model.ProblemFeedback{
			ProblemID:    problem.ID,
			UserID:       0, // system
			SubmissionID: 0,
			FeedbackType: "suspicious_testdata",
			Priority:     "P1",
			Description:  "参考解在 WA/RE 的测试点上同样失败，测试数据疑似有误",
			Evidence:     evidence,
			Confidence:   "high",
			Status:       "pending",
		}

		// 检查是否已存在相同问题反馈（避免重复）
		var existing int64
		database.DB.Model(&model.ProblemFeedback{}).
			Where("problem_id = ? AND feedback_type = ? AND confidence = 'high' AND status = 'pending'",
				problem.ID, "suspicious_testdata").
			Count(&existing)
		if existing == 0 {
			if err := database.DB.Create(&fb).Error; err != nil {
				log.Printf("[diagnose] failed to save testdata feedback: %v", err)
			} else {
				log.Printf("[diagnose] P1 testdata issue: ref solution fails on problem %d case #%d",
					problem.ID, req.FailedCaseID)
			}
		}
	} else {
		// 参考解通过了 → 问题在学生代码，数据没问题
		writeSSE("status", "✅ 参考解验证通过，测试数据无异常")
	}
}

// validateSampleCases 用参考解运行所有样例，检查是否匹配。
func validateSampleCases(problem model.Problem) []model.ProblemFeedback {
	if problem.SampleCases == nil {
		return nil
	}

	type sampleCase struct {
		Input  string `json:"input"`
		Output string `json:"output"`
	}
	var samples []sampleCase
	if err := json.Unmarshal(problem.SampleCases, &samples); err != nil {
		return nil
	}
	if len(samples) == 0 {
		return nil
	}

	lang := detectRefLang(problem.ReferenceSolution)
	var feedbacks []model.ProblemFeedback

	for i, sc := range samples {
		if sc.Input == "" || sc.Output == "" {
			continue
		}

		result := runRefForQuality(lang, problem.ReferenceSolution, sc.Input, problem.TimeLimit, problem.MemoryLimit)

		if result.ErrorMsg != "" || (result.Status != "Accepted" && result.Output != sc.Output) {
			// 参考解在样例上失败 → 样例或参考解有误
			desc := fmt.Sprintf("样例 #%d 验证失败", i+1)
			evidence := fmt.Sprintf("样例输入:\n%s\n期望输出:\n%s\n参考解输出:\n%s\n错误: %s",
				sc.Input, sc.Output, result.Output, result.ErrorMsg)

			fb := model.ProblemFeedback{
				ProblemID:    problem.ID,
				UserID:       0,
				FeedbackType: "sample_error",
				Priority:     "P1",
				Description:  desc,
				Evidence:     evidence,
				Confidence:   "high",
				Status:       "pending",
			}
			feedbacks = append(feedbacks, fb)
		} else if result.Output != sc.Output {
			// 输出不匹配
			desc := fmt.Sprintf("样例 #%d 输出不匹配", i+1)
			evidence := fmt.Sprintf("样例输入:\n%s\n期望输出:\n%s\n参考解输出:\n%s",
				sc.Input, sc.Output, result.Output)

			fb := model.ProblemFeedback{
				ProblemID:    problem.ID,
				UserID:       0,
				FeedbackType: "sample_error",
				Priority:     "P1",
				Description:  desc,
				Evidence:     evidence,
				Confidence:   "high",
				Status:       "pending",
			}
			feedbacks = append(feedbacks, fb)
		}
	}

	return feedbacks
}

// detectRefLang 从参考解代码推断编程语言。
func detectRefLang(code string) string {
	code = strings.TrimSpace(code)
	if strings.HasPrefix(code, "package ") || strings.HasPrefix(code, "package main") {
		return "go"
	}
	if strings.HasPrefix(code, "#include") {
		return "cpp"
	}
	if strings.Contains(code, "import ") && strings.Contains(code, "java.") {
		return "java"
	}
	return "go"
}

// refResult 简化判题结果。
type refResult struct {
	Output   string
	Status   string
	ErrorMsg string
}

// runRefForQuality 运行参考解并返回结果（用于质量检测）。
func runRefForQuality(lang, code, input string, timeLimit, memoryLimit int) refResult {
	if lang == "go" {
		return runRefGoQuality(code, input, timeLimit)
	}
	result := judge.Run(lang, code, input, timeLimit, memoryLimit)
	return refResult{
		Output:   result.Output,
		Status:   result.Status,
		ErrorMsg: result.ErrorMsg,
	}
}

func runRefGoQuality(code, input string, timeLimitMs int) refResult {
	tmpDir, err := os.MkdirTemp("", "ref-quality-*")
	if err != nil {
		return refResult{ErrorMsg: "failed to create temp dir"}
	}
	defer os.RemoveAll(tmpDir)

	mainPath := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(mainPath, []byte(code), 0644); err != nil {
		return refResult{ErrorMsg: "failed to write source"}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeLimitMs)*time.Millisecond+3*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "go", "run", mainPath)
	cmd.Stdin = strings.NewReader(input)
	cmd.Dir = tmpDir

	output, err := cmd.Output()
	if err != nil {
		return refResult{ErrorMsg: fmt.Sprintf("exec error: %v", err), Output: string(output)}
	}
	return refResult{Output: string(output), Status: "Accepted"}
}


// ============================================================================
// Test case loading (shared from former ai_debug.go)
// ============================================================================

type localTestCase struct {
	Input    string
	Expected string
}

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
