package ai

import (
	"regexp"
	"strings"
)

// ============================================================================
// AI 提示注入防护
// ============================================================================
//
// 提示注入（Prompt Injection）是 AI 系统的常见攻击方式：
// 用户试图通过恶意输入覆盖系统的预设指令，让 AI 执行非预期行为。
//
// JudgeX 的防护策略：
//
// 1. 15 个正则表达式模式，分为三类：
//    - 系统指令覆盖（让 AI 忽略预设角色）
//    - 答案提取（直接要求给出 AC 代码）
//    - 测试数据探测（询问隐藏测试用例）
//
// 2. 三级威胁响应：
//    - "none"：无风险，正常处理
//    - "low"：潜在风险，继续处理但 LLM prompt 中标注警告
//    - "high"：高风险，拦截请求，返回教育性回复
//
// 匹配阈值：
//   - 匹配 >= 3 个模式 → "high"
//   - 匹配 1-2 个模式 → "low"
//   - 匹配 0 个模式 → "none"

// injectionPatterns 是用于检测提示注入的正则表达式列表。
// 所有模式不区分大小写（(?i) 标志）。
var injectionPatterns = []*regexp.Regexp{
	// ======== 系统指令覆盖攻击 ========
	// 试图让 AI 忽略预设的"辅导角色"限制
	regexp.MustCompile(`(?i)(ignore|forget|disregard)\s+(all\s+)?(previous|above|prior|your)\s+(instructions?|rules?|prompts?|guidelines?)`),
	// 试图让 AI 扮演不同的角色（绕过诊断引导限制）
	regexp.MustCompile(`(?i)(you\s+are\s+now|your\s+new\s+role\s+is|act\s+as\s+a)`),
	// 试图读取系统提示词
	regexp.MustCompile(`(?i)system\s*(prompt|message|instruction)s?\s*[:=]`),
	// GPT-4o 特殊分隔符（ChatML 格式）
	regexp.MustCompile(`(?i)<\|.*\|>`),
	// 已知的越狱关键词
	regexp.MustCompile(`(?i)DAN\s+mode|jailbreak|developer\s+mode`),

	// ======== 答案提取攻击 ========
	// 直接要求给出完整代码（诊断模式不允许直接给代码）
	regexp.MustCompile(`(?i)(give|write|output|show|provide|tell)\s+(me\s+)?(the\s+)?(complete|full|entire|working|correct|accepted|ac)\s+(solution|code|answer|program|implementation)`),
	// 简洁的命令式请求
	regexp.MustCompile(`(?i)(just\s+give\s+me\s+the\s+(code|answer|solution))`),
	// 询问完整代码
	regexp.MustCompile(`(?i)(what\s+is\s+the\s+(full|complete)\s+(code|solution))`),
	// "写代码"类请求
	regexp.MustCompile(`(?i)(write\s+(the\s+)?code\s+(to\s+solve|for\s+this))`),

	// ======== 测试数据探测 ========
	// 试图获取隐藏测试用例
	regexp.MustCompile(`(?i)(what\s+are\s+the\s+(hidden\s+)?test\s*cases?)`),
	// 要求显示测试数据
	regexp.MustCompile(`(?i)(show\s+(me\s+)?(the\s+)?(hidden\s+)?(test\s*(case|data|input|output)))`),
}

// ScanForInjection 扫描用户输入中的提示注入。
// 返回威胁等级和原因描述。
//
// 使用方式：
//
//	threat, reason := ScanForInjection(userMessage)
//	switch threat {
//	case "high":
//	    return GuardResponse()  // 拦截
//	case "low":
//	    // 继续处理，但在 prompt 中加入警告
//	case "none":
//	    // 正常处理
//	}
func ScanForInjection(userMessage string) (threat string, reason string) {
	msg := strings.TrimSpace(userMessage)
	if msg == "" {
		return "none", ""
	}

	totalMatches := 0
	for _, pattern := range injectionPatterns {
		totalMatches += len(pattern.FindAllString(msg, -1))
	}

	if totalMatches >= 3 {
		return "high", "Multiple injection/answer-extraction patterns detected. Request blocked."
	}
	if totalMatches >= 1 {
		return "low", "Potential prompt injection detected. Proceeding with guard active."
	}
	return "none", ""
}

// SanitizeUserMessage 清理用户输入，去除多余的空白和空行。
// 防止通过特殊空白字符进行注入。
func SanitizeUserMessage(msg string) string {
	lines := strings.Split(msg, "\n")
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		cleaned = append(cleaned, line)
	}
	return strings.Join(cleaned, "\n")
}

// GuardResponse 在检测到高风险注入时返回安全响应。
// 返回教育性回复，不透露任何题目或系统信息，
// 并引导用户通过正当方式学习。
func GuardResponse() string {
	return `I'm here to help you learn, not to give you answers.

If you're stuck on this problem, try:
- Breaking it down into smaller steps
- Thinking about edge cases
- Reviewing the problem constraints carefully

What specific concept would you like help understanding?`
}
