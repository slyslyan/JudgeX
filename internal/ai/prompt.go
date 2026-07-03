package ai

import (
	"fmt"

	"judgex/internal/database"
	"judgex/internal/model"
)

// PromptContext 保存组装提示词所需的所有上下文数据。
// AssembleContext() 从数据库加载数据填充此结构体，
// BuildSystemPrompt() 使用它生成最终的 prompt。
type PromptContext struct {
	AgentType string // "socratic"

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
}

// ============================================================================
// 上下文加载和提示词组装
// ============================================================================

// AssembleContext 从数据库加载题目和提交信息，构造 PromptContext。
func AssembleContext(agentType string, problemID uint, submissionID *int64) PromptContext {
	ctx := PromptContext{AgentType: agentType}

	var problem model.Problem
	if err := database.DB.First(&problem, problemID).Error; err == nil {
		ctx.ProblemTitle = problem.Title
		ctx.ProblemDescription = truncateText(problem.Description, 4000)
		ctx.ProblemTimeLimit = problem.TimeLimit
		ctx.ProblemMemoryLimit = problem.MemoryLimit
		if problem.SampleCases != nil {
			ctx.SampleCases = string(problem.SampleCases)
		}
	}

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

// BuildSystemPrompt 构建系统提示词。
// 当前仅支持 "diagnose" 类型。
func BuildSystemPrompt(ctx PromptContext) string {
	switch ctx.AgentType {
	default:
		return baseSystemPrompt() + diagnosePrompt(ctx)
	}
}

// ============================================================================
// 各 Agent 的提示词模板
// ============================================================================

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

func diagnosePrompt(ctx PromptContext) string {
	p := fmt.Sprintf(`## Role: AI Diagnosis Assistant
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

// ============================================================================
// 辅助函数
// ============================================================================

func truncateText(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "\n...[truncated]"
}
