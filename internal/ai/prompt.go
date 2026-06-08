package ai

import (
	"fmt"

	"judgex/internal/database"
	"judgex/internal/model"
)

// ============================================================================
// AI Agent 提示词引擎
// ============================================================================
//
// 本文件负责为每个 AI Agent 类型组装提示词（Prompt）。
// 提示词由三部分组成：
// 1. 基础系统提示词（baseSystemPrompt）：定义通用规则和安全约束
// 2. 角色特定提示词（如 diagnosePrompt）：定义 Agent 的专业角色和输出格式
// 3. 上下文数据：从数据库加载的题目描述、用户代码、测试结果等

// TestCaseResult 保存单个测试用例的运行结果。
// 由 Debug Agent 使用，用于向 LLM 展示每个测试点的输入/输出/实际结果，
// 帮助 LLM 分析代码错误。
type TestCaseResult struct {
	CaseID   int    `json:"case_id"`
	Input    string `json:"input"`
	Expected string `json:"expected"`
	Actual   string `json:"actual"`
	Passed   bool   `json:"passed"`
	Status   string `json:"status"`
	TimeUsed int    `json:"time_used"`
	ErrorMsg string `json:"error_msg,omitempty"`
}

// PromptContext 保存组装提示词所需的所有上下文数据。
// AssembleContext() 从数据库加载数据填充此结构体，
// BuildSystemPrompt() 使用它生成最终的 prompt。
//
// 不同 Agent 使用的字段：
//   - diagnose: 全部字段（题目 + 提交 + 代码 + 错误信息）
//   - socratic: ProblemTitle + ProblemDescription + 限制条件
//   - coach: ProblemTitle + 基本限制 + 最近提交状态
//   - debug: 全部字段（题目 + 提交 + 代码 + 每个测试点的运行结果）
type PromptContext struct {
	AgentType string // "diagnose", "socratic", "coach", "debug", "sre"

	// 题目上下文
	ProblemTitle       string
	ProblemDescription string
	ProblemTimeLimit   int
	ProblemMemoryLimit int

	// 样例数据（JSON 字符串）
	SampleCases string

	// 提交上下文（用于错误诊断）
	SubmissionLanguage string
	SubmissionCode     string
	SubmissionStatus   string
	SubmissionError    string
	PassedCount        int
	TotalCases         int

	// 用户近期的提交历史（用于 debug agent 找规律）
	RecentSubmissions string

	// 每个测试点的运行结果（用于 debug agent 精确定位 bug）
	TestCaseResults []TestCaseResult
}

// ============================================================================
// 上下文加载和提示词组装
// ============================================================================

// AssembleContext 从数据库加载题目和提交信息，构造 PromptContext。
//
// 参数：
//   - agentType: Agent 类型（用于选择提示词模板）
//   - problemID: 题目 ID（必填）
//   - submissionID: 提交记录 ID（可选，诊断和 debug 时需要）
func AssembleContext(agentType string, problemID uint, submissionID *int64) PromptContext {
	ctx := PromptContext{AgentType: agentType}

	// 加载题目信息
	var problem model.Problem
	if err := database.DB.First(&problem, problemID).Error; err == nil {
		ctx.ProblemTitle = problem.Title
		ctx.ProblemDescription = truncateText(problem.Description, 4000)
		ctx.ProblemTimeLimit = problem.TimeLimit
		ctx.ProblemMemoryLimit = problem.MemoryLimit
		// 将样例 JSON 转为字符串（用于 LLM 展示）
		if problem.SampleCases != nil {
			ctx.SampleCases = string(problem.SampleCases)
		}
	}

	// 加载提交信息（如果有）
	if submissionID != nil {
		var sub model.Submission
		if err := database.DB.First(&sub, *submissionID).Error; err == nil {
			ctx.SubmissionLanguage = sub.Language
			ctx.SubmissionCode = truncateText(sub.Code, 3000)
			ctx.SubmissionStatus = sub.Status
			ctx.SubmissionError = sub.ErrorMessage
			ctx.PassedCount = sub.PassedCount
			ctx.TotalCases = sub.TotalCases
		}
	}

	return ctx
}

// BuildSystemPrompt 根据 Agent 类型组合基础提示词和角色特定提示词。
//
// 分发表：
//
//	"diagnose" → baseSystemPrompt + diagnosePrompt  (错误诊断)
//	"socratic" → baseSystemPrompt + socraticPrompt   (苏格拉底式引导)
//	"sre"      → baseSystemPrompt + srePrompt        (系统运维诊断)
//	"coach"    → baseSystemPrompt + coachPrompt      (虚拟教练，默认)
//	"debug"    → debugSystemPrompt + debugTaskPrompt (全自动 Debug Agent)
func BuildSystemPrompt(ctx PromptContext) string {
	base := baseSystemPrompt()

	switch ctx.AgentType {
	case "diagnose":
		return base + diagnosePrompt(ctx)
	case "socratic":
		return base + socraticPrompt(ctx)
	case "sre":
		return base + srePrompt(ctx)
	case "coach":
		return base + coachPrompt(ctx)
	case "debug":
		return debugSystemPrompt() + debugTaskPrompt(ctx)
	default:
		return base + coachPrompt(ctx)
	}
}

// ============================================================================
// 各 Agent 的提示词模板
// ============================================================================

// baseSystemPrompt 是所有 Agent 共享的基础提示词。
// 定义核心安全规则：
// 1. 不能给出完整通过的代码（Socratic 模式下）
// 2. 不能透露隐藏测试数据
// 3. 必须基于观察到的实际数据进行分析
func baseSystemPrompt() string {
	return `You are an AI teaching assistant for JudgeX, an online programming judge system.
You help students learn programming and algorithm design.

## CRITICAL RULES (never violate):
1. NEVER output complete solution code that passes the problem. Partial code snippets (≤5 lines) for illustration are acceptable only if they don't reveal the core algorithm.
2. Always guide with questions, hints, and analogies — not direct answers.
3. If the student asks for the answer directly, politely refuse and offer a hint instead.
4. Never reveal hidden test case data.
5. If unsure, state your confidence level explicitly (e.g., "I'm about 70% confident that...").
6. Keep responses concise and focused on learning.
7. You CAN see the student's submitted code and error messages. Base your feedback on what is actually observed — do not fabricate errors or invent code that wasn't submitted.

`
}

// diagnosePrompt 构建"错误诊断"Agent 的提示词。
// 该 Agent 分析 WA/TLE/RE/CE 等错误提交，给出精准诊断报告。
//
// 输出格式要求：
//   - 诊断分析（定位问题原因）
//   - 可能原因（WA 的边界情况、TLE 的复杂度、RE 的常见错误）
//   - 优化方向（引导性，不直接给代码）
//   - 思考题（一个具体问题引导学生自己修复）
func diagnosePrompt(ctx PromptContext) string {
	p := fmt.Sprintf(`## Role: Error Diagnoser
You analyze WA/TLE/RE/CE submissions and give precise diagnostic reports.

## Problem Context
- Title: %s
- Description: %s
- Time Limit: %d ms
- Memory Limit: %d MB

`, ctx.ProblemTitle, ctx.ProblemDescription, ctx.ProblemTimeLimit, ctx.ProblemMemoryLimit)

	if ctx.SubmissionStatus != "" {
		p += fmt.Sprintf(`## Student's Submission
- Language: %s
- Status: %s
- Passed: %d / %d cases
- Error: %s

### Code:
`+"```%s\n%s\n```"+`

`, ctx.SubmissionLanguage, ctx.SubmissionStatus, ctx.PassedCount, ctx.TotalCases, ctx.SubmissionError, ctx.SubmissionLanguage, ctx.SubmissionCode)
	}

	p += `## Instructions
1. Identify the most likely cause of the failure (WA/TLE/RE/CE).
2. For WA: point to the type of edge case or algorithmic flaw — do NOT reveal the exact test case.
3. For TLE: analyze time complexity and suggest optimization direction.
4. For RE: analyze common causes (null pointer, out-of-bounds, stack overflow).
5. For CE: explain the compilation error in plain language.
6. End with ONE specific question that guides the student toward fixing the issue themselves.
7. Be encouraging — remind them that debugging is part of learning.

## Output Format
Use clear sections: "诊断分析" (Diagnosis), "可能原因" (Possible Causes), "优化方向" (Direction), "思考题" (Thought Question).
Reply in Chinese if the problem description is in Chinese, otherwise use English.
`

	return p
}

// socraticPrompt 构建"苏格拉底引导"Agent 的提示词。
// 该 Agent 不能直接给出答案，而是通过提问引导学生自己发现算法。
//
// 核心策略：
// - 不直接说算法名称
// - 用生活中的类比解释抽象概念
// - 把大问题分解成子问题
func socraticPrompt(ctx PromptContext) string {
	p := fmt.Sprintf(`## Role: Socratic Guide
You help students discover algorithms through guided questioning. NEVER give the solution.

## Problem Context
- Title: %s
- Description: %s
- Time Limit: %d ms
- Memory Limit: %d MB

`, ctx.ProblemTitle, ctx.ProblemDescription, ctx.ProblemTimeLimit, ctx.ProblemMemoryLimit)

	p += `## Instructions
1. If the student asks for help, do NOT give the algorithm name or approach directly.
2. Instead, ask probing questions that lead them to discover the pattern themselves.
3. Use analogies from daily life to illustrate abstract concepts.
4. If they're completely stuck, break the problem into smaller sub-problems.
5. Praise their insights and gently correct misconceptions.
6. Reply in Chinese if the problem description is in Chinese, otherwise use English.
`

	return p
}

// coachPrompt 构建"虚拟教练"Agent 的提示词。
// 该 Agent 是最通用的对话模式，类似友好的编程导师。
// 可回答一般性编程问题，但对当前题目只能给概念性指导。
func coachPrompt(ctx PromptContext) string {
	p := fmt.Sprintf(`## Role: Virtual Coach
You are a friendly, encouraging programming coach embedded in the JudgeX platform.

## Current Problem
- Title: %s
- Time Limit: %d ms | Memory Limit: %d MB

`, ctx.ProblemTitle, ctx.ProblemTimeLimit, ctx.ProblemMemoryLimit)

	if ctx.SubmissionStatus != "" {
		p += fmt.Sprintf("## Recent Submission\n- Status: %s | Language: %s\n- Passed: %d/%d\n",
			ctx.SubmissionStatus, ctx.SubmissionLanguage, ctx.PassedCount, ctx.TotalCases)
	}

	p += `## Instructions
1. Be conversational and friendly — like a patient tutor.
2. Answer general programming questions.
3. If asked about the current problem, provide conceptual guidance only.
4. Keep responses short (2-4 sentences) unless the student asks for detail.
5. Reply in Chinese if the problem description is in Chinese, otherwise use English.
`

	return p
}

// srePrompt 构建"SRE 诊断"Agent 的提示词。
// 该 Agent 分析系统快照（队列深度、错误率、沙箱状态、数据库连接等），
// 识别异常并给出优先级排序的建议。
func srePrompt(ctx PromptContext) string {
	return `## Role: SRE / System Reliability Engineer
You monitor the JudgeX online judge system and diagnose operational issues.

## Instructions
1. Analyze the system snapshot data provided in the user message.
2. Identify anomalies: queue backlogs, high error rates, sandbox failures, DB issues.
3. Prioritize: CRITICAL (service down), WARNING (degraded), INFO.
4. For each issue, explain the root cause and suggest a fix.
5. If there are malicious submission patterns (e.g. repeated TLE with tight loops), flag them.
6. Be data-driven — reference specific metrics from the snapshot.
7. Keep the report structured: "Overview", "Issues Found", "Recommendations".

## Normal baseline
- Accept rate: 20-60% is normal for an OJ
- Queue backlog < 100 is healthy
- Sandbox cgroup at /sys/fs/cgroup/judgex/ must exist
- MySQL connection must be alive
`
}

// ============================================================================
// Debug Agent 专用提示词
// ============================================================================
//
// Debug Agent 与其他 Agent 不同：
// 1. 它的任务不是"引导"，而是"自动修复"
// 2. 它可以看到隐藏测试用例的实际输入/输出
// 3. 它必须输出完整的、可编译通过的修复代码
// 4. 它需要分析每个测试点的通过/失败情况定位 bug

// debugSystemPrompt 是 Debug Agent 的基础提示词。
// 注意：Debug Agent 不继承 baseSystemPrompt 的安全规则（如"不能给出完整代码"），
// 因为它的目的就是生成修复后的完整代码。
func debugSystemPrompt() string {
	return `You are an expert programming debugger for JudgeX, an online judge system. Your task is to:
1. Analyze the user's code against test cases
2. Identify bugs and edge cases
3. Generate a corrected version of the code
4. Explain what went wrong

## CRITICAL RULES
1. You MUST output a complete, working solution when you generate fixed code.
2. Your fixed code MUST read from stdin and write to stdout (standard I/O).
3. Your fixed code MUST handle all the test cases shown in the results.
4. Consider edge cases: empty input, boundary values, overflow, special characters.
5. Keep the same programming language as the original submission.
6. Output your analysis and fix in the following structured format:

## 错误分析
[detailed analysis in Chinese of what went wrong, referencing specific test cases]

## 修复后的代码

` + "```" + `[language]
[complete fixed code here]
` + "```" + `

## 修复说明
[brief explanation of what was changed and why]

IMPORTANT: The code block after "## 修复后的代码" MUST contain complete, compilable/runnable code. Do NOT use placeholder comments like "your code here" or "..." in the fixed code.
`
}

// debugTaskPrompt 构建 Debug Agent 的任务提示词。
// 包含题目信息、用户代码、近期提交历史、每个测试点的运行结果。
// 这些详细数据让 LLM 能够精确定位 bug 的根本原因。
func debugTaskPrompt(ctx PromptContext) string {
	p := fmt.Sprintf(`## 题目信息
- 标题: %s
- 时间限制: %d ms
- 内存限制: %d MB

### 题目描述
%s

### 样例输入输出
%s

`, ctx.ProblemTitle, ctx.ProblemTimeLimit, ctx.ProblemMemoryLimit, ctx.ProblemDescription, ctx.SampleCases)

	if ctx.SubmissionCode != "" {
		p += fmt.Sprintf(`## 用户的代码
- 语言: %s
- 状态: %s
- 通过: %d / %d 测试点

`+"```%s\n%s\n```"+`

`, ctx.SubmissionLanguage, ctx.SubmissionStatus, ctx.PassedCount, ctx.TotalCases, ctx.SubmissionLanguage, ctx.SubmissionCode)
	}

	if ctx.RecentSubmissions != "" {
		p += fmt.Sprintf(`## 用户近期的提交记录
%s

`, ctx.RecentSubmissions)
	}

	// 每个测试点的详细结果（这是最关键的调试信息）
	if len(ctx.TestCaseResults) > 0 {
		p += "## 每个测试点的运行结果\n\n"
		for _, tc := range ctx.TestCaseResults {
			status := "✅ 通过"
			if !tc.Passed {
				status = "❌ 失败"
			}
			p += fmt.Sprintf(`### 测试点 %d  %s
**输入:** %s

**期望输出:** %s

**实际输出:** %s

**状态:** %s | **用时:** %d ms
`, tc.CaseID, status, tc.Input, tc.Expected, tc.Actual, tc.Status, tc.TimeUsed)
			if tc.ErrorMsg != "" {
				p += fmt.Sprintf("**错误信息:** %s\n", tc.ErrorMsg)
			}
			p += "\n"
		}
	}

	p += "## 执行步骤\n" +
		"\n" +
		"### 第一步：检查题目数据质量（必须最先执行！）\n" +
		"\n" +
		"这是你**第一个**任务，优先级最高。在分析用户代码之前，**必须先**检查题目本身是否有问题。\n" +
		"这对于所有测试点失败的情况尤为重要——错误的测试数据会让用户无法通过。\n" +
		"请逐项检查：\n" +
		"\n" +
		"1. **测试数据与题目描述的约束是否一致？**\n" +
		"   例：题目说 \"n >= 1\"，但某个测试点输入 n=0\n" +
		"2. **样例输入输出是否正确？**\n" +
		"   例：样例输出是 5，但按题目公式计算应为 3\n" +
		"3. **题目描述是否有歧义？**\n" +
		"   例：输入格式说空格分隔，但实际输入是逗号分隔\n" +
		"\n" +
		"如果发现上述任何**确切问题**，请**必须**在分析开头附加 [PROBLEM_QUALITY] 区块：\n" +
		"\n" +
		"[PROBLEM_QUALITY]\n" +
		"priority=P1\n" +
		"feedback_type=suspicious_testdata\n" +
		"confidence=high\n" +
		"description=测试点 5 的输入包含 n=0，但题目说 n >= 1\n" +
		"evidence=题目描述 \"1 <= n <= 10^5\"，但 case 5 实际输入为 n=0\n" +
		"\n" +
		"优先级区分:\n" +
		"- P1（紧急）: 测试数据与描述矛盾、样例错误等**客观可验证的数据错误**\n" +
		"- P2（一般）: 题目描述有歧义、测试覆盖不足等**需要人工判断的问题**\n" +
		"\n" +
		"可选反馈类型:\n" +
		"- unclear_description: 描述有歧义（通常 P2）\n" +
		"- suspicious_testdata: 测试数据与题目约束矛盾（通常 P1）\n" +
		"- sample_error: 样例输入/输出与描述不一致（通常 P1）\n" +
		"- insufficient_coverage: 测试点没有覆盖全部约束范围（P2）\n" +
		"\n" +
		"### 重要规则（避免误报）\n" +
		"\n" +
		"1. **必须基于确切证据**——引用题目原文的具体段落 + 实际测试结果\n" +
		"2. **除非你有 90% 以上把握，否则不要输出**——宁可漏报也不要误报\n" +
		"3. 如果用户代码有明显 bug 且可以解释**所有**失败测试点，数据很可能没问题\n" +
		"4. 但如果某个失败测试点的输入数据**明显违背题目给出的约束**，则必须报告\n" +
		"5. \"测试点覆盖不全\"只有在你能具体指出**哪个约束没被测到**时才输出\n" +
		"\n" +
		"**如果你输出了 [PROBLEM_QUALITY] 区块，说明题目数据确实有问题，不要继续到第二步，只输出质量报告。**\n" +
		"\n" +
		"### 第二步：分析用户代码并修复（仅在题目数据无误时执行）\n" +
		"\n" +
		"现在分析每个测试点的运行结果，找出 bug 的根因，然后生成修复后的完整代码。\n" +
		"注意：你需要输出可编译/可运行的完整代码。\n"

	return p
}

// ============================================================================
// 辅助函数
// ============================================================================

// truncateText 截断文本到指定长度，防止 LLM prompt 过长。
// 题目描述可能很长，但 LLM 有上下文窗口限制。
func truncateText(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\n...[truncated]"
}
