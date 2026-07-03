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
const maxTraceLines = 1000
const maxConcurrentDiagnose = 4 // 最大并发插桩诊断任务数
const debugUserRateLimit = 5    // 每用户每分钟最多 5 次诊断请求
const debugUserRateWindow = 1 * time.Minute

// diagnoseSemaphore 限制同时运行的插桩诊断任务数量。
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
	// ── 降级链 ──
	// Level 0: 插桩 + trace + LLM 分析（最精准）
	// Level 1: 无插桩 + 静态代码 + 测试用例 + LLM 分析（LLM 依然工作）
	// Level 2: 模板化诊断（LLM 不可用时兜底）
	//
	// 各级别之间独立失败，前一级别失败自动尝试下一级。

	var traceOutput string

	// ── Level 0: 插桩执行 ──
	{
		writeSSE("status", "正在插桩代码...")
		instr := ai.NewGoInstrumenter()
		modified, err := instr.Instrument(req.Code, req.Language)
		if err == nil && modified != req.Code {
			writeSSE("status", "正在运行插桩代码...")
			traceOutput = runInstrumented(ctx, req.Language, modified, req.FailedInput, problem.TimeLimit, problem.MemoryLimit)
			writeSSE("trace", jsonEscape(traceOutput))
		} else {
			writeSSE("status", "代码插桩不支持此语言，使用静态分析模式...")
		}
	}

	// ── Level 1: LLM 分析 ──
	writeSSE("status", "AI 正在分析...")
	{
		systemPrompt := buildWreSystemPrompt(req, problem)
		userMsg := buildWreUserMessage(req, traceOutput)

		llmOK := make(chan bool, 1)
		go func() {
			streamLLM(ctx, writeSSE, systemPrompt, userMsg)
			llmOK <- true
		}()

		select {
		case <-llmOK:
		case <-ctx.Done():
		}
	}

	// ── 题目质量检测 ──
	// 参考解对本题 WA/RE 的测试点进行验证
	checkFailedTestCaseReference(req, problem, writeSSE)

	writeSSE("done", "")
}

func buildWreSystemPrompt(req aiDiagnoseRequest, problem model.Problem) string {
	verdict := req.Verdict
	return fmt.Sprintf(`You are an expert programming debugger performing causal analysis on a %s submission.

## Role
You are a professor who helps students understand why their code failed. You analyze execution traces with surgical precision.

## Problem Context
- Title: %s
- Description: %s
- Time Limit: %d ms | Memory Limit: %d MB

## Your Task
Analyze the failure and provide:
1. **Observation**: What does the code do vs what it should do? Reference specific variable values from the trace.
2. **Root cause question**: A pointed question that leads the student to the exact bug location.
3. **Hint**: If they're stuck, a gentle nudge about the pattern or edge case they missed.

## Constraints
- NEVER output a complete working solution
- Base your analysis on the trace output — reference specific variable values and line numbers
- If no trace is available (static mode), explain the likely failure based on code structure
- Reply in Chinese if the problem description is in Chinese, otherwise use English
- Format: 现象观察 / 根因提问 / 提示`, verdict, problem.Title, problem.Description, problem.TimeLimit, problem.MemoryLimit)
}

// ============================================================================
// Instrumented code execution with I/O truncation
// ============================================================================

// lineLimitWriter tracks output lines and cancels the context when the limit is exceeded.
type lineLimitWriter struct {
	maxLines  int
	lineCount int
	cancel    context.CancelFunc
	buf       bytes.Buffer
}

func (w *lineLimitWriter) Write(p []byte) (int, error) {
	for _, b := range p {
		w.buf.WriteByte(b)
		if b == '\n' {
			w.lineCount++
			if w.lineCount > w.maxLines && w.cancel != nil {
				w.cancel()
			}
		}
	}
	return len(p), nil
}

func (w *lineLimitWriter) String() string {
	return w.buf.String()
}

// runInstrumented compiles and runs instrumented code, capturing output with
// line-count truncation. Returns the captured output or an error message.
func runInstrumented(ctx context.Context, language, code, input string, timeLimitMs, memoryLimitMB int) string {
	if language == "go" {
		return runInstrumentedGo(ctx, code, input, timeLimitMs)
	}
	// For other languages, use the sandbox-based judge.Run.
	result := judge.Run(language, code, input, timeLimitMs, memoryLimitMB)
	return formatTraceOutput(result.Output, result.ErrorMsg)
}

// runInstrumentedGo compiles instrumented Go code with "go run" and captures output.
func runInstrumentedGo(ctx context.Context, code, input string, timeLimitMs int) string {
	tmpDir, err := os.MkdirTemp("", "diagnose-instr-*")
	if err != nil {
		return "[instrumentation error: failed to create temp dir]"
	}
	defer os.RemoveAll(tmpDir)

	mainPath := filepath.Join(tmpDir, "main.go")
	if err := os.WriteFile(mainPath, []byte(code), 0644); err != nil {
		return "[instrumentation error: failed to write source]"
	}

	runCtx, cancel := context.WithTimeout(ctx, time.Duration(timeLimitMs)*time.Millisecond+5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "go", "run", mainPath)
	cmd.Stdin = strings.NewReader(input)
	cmd.Dir = tmpDir

	var stdout lineLimitWriter
	stdout.maxLines = maxTraceLines
	stdout.cancel = cancel

	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Check if it was our line-limit cancellation.
		if stdout.lineCount > maxTraceLines {
			output := stdout.String()
			if len(output) > 200 {
				output = output[:len(output)-200] + "\n... [TRUNCATED: output exceeded 1000 lines]"
			}
			return output
		}
		// Real error.
		return fmt.Sprintf("[exit: %v]\n%s\n%s", err, stdout.String(), stderr.String())
	}

	return stdout.String()
}

// formatTraceOutput combines stdout and stderr into a single trace string.
func formatTraceOutput(stdout, stderr string) string {
	var b strings.Builder
	if stdout != "" {
		b.WriteString(stdout)
	}
	if stderr != "" {
		if b.Len() > 0 {
			b.WriteString("\n--- stderr ---\n")
		}
		stderr = truncateLines(stderr, maxTraceLines)
		b.WriteString(stderr)
	}
	if b.Len() == 0 {
		return "[no output produced]"
	}
	return b.String()
}

// truncateLines truncates the string to at most maxLines lines.
func truncateLines(s string, maxLines int) string {
	if maxLines <= 0 {
		return ""
	}
	lines := strings.SplitN(s, "\n", maxLines+1)
	if len(lines) > maxLines {
		lines[maxLines-1] = lines[maxLines-1] + "\n... [TRUNCATED]"
		return strings.Join(lines[:maxLines], "\n")
	}
	return s
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

func buildWreUserMessage(req aiDiagnoseRequest, traceOutput string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("## Student's Code\n\n```%s\n%s\n```\n\n", req.Language, req.Code))

	if req.FailedInput != "" || req.FailedExpected != "" {
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
	}

	if traceOutput != "" {
		b.WriteString("## Execution Trace (Instrumented)\n")
		b.WriteString("```\n")
		b.WriteString(truncateLines(traceOutput, maxTraceLines))
		b.WriteString("\n```\n\n")
	}

	b.WriteString("Analyze the failure. Identify the root cause and guide the student to fix it.")
	return b.String()
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
			log.Printf("[diagnose] problem quality issue detected: %s (P%d) for problem %d",
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
// Utilities
// ============================================================================

func jsonEscape(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
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
