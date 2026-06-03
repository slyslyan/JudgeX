package ai

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ============================================================================
// LLM 流式客户端
// ============================================================================
//
// 本文件实现了兼容 OpenAI Chat Completion API 的流式（SSE）客户端。
// 支持任何兼容 OpenAI 格式的 LLM 服务（DeepSeek、OpenAI、Anthropic 等）。
//
// 核心接口：
//   StreamChat(ctx, systemPrompt, messages) → <-chan StreamChunk
//
// 使用 channel 模式使调用方可以方便地用 range 循环消费流式响应：
//
//   ch := StreamChat(ctx, systemPrompt, messages)
//   for chunk := range ch {
//       if chunk.Error != "" { ... }
//       if chunk.Done { break }
//       processToken(chunk.Token)
//   }
//
// 安全特性：
// - 集成断路器（Circuit Breaker）：LLM API 异常时自动降级
// - 上下文控制：调用方通过 context 控制超时和取消
// - 请求体大小限制：MaxTokens = 2048

// ChatMessage 表示 LLM 对话中的一条消息。
// Role 可以是 "system"（系统指令）、"user"（用户）、"assistant"（AI回复）。
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// chatRequest 是 OpenAI Chat Completion API 的请求体结构。
// Stream 固定为 true，始终使用流式模式。
type chatRequest struct {
	Model     string        `json:"model"`
	Messages  []ChatMessage `json:"messages"`
	Stream    bool          `json:"stream"`
	MaxTokens int           `json:"max_tokens,omitempty"`
}

// StreamChunk 是流式响应的一个数据块。
// 每个块可能包含：一个 token、一个错误、或一个完成信号。
//
// 使用方式：
//
//	Token:  有内容表示这是 AI 回复的一部分
//	Error:  非空表示发生了错误
//	Done:   true 表示流已结束
type StreamChunk struct {
	Token string `json:"token"`
	Error string `json:"error,omitempty"`
	Done  bool   `json:"done"`
}

// StreamChat 发送一个流式对话请求并返回 token channel。
//
// 参数说明：
//
//	ctx          — 控制请求的生命周期（超时、取消），必须持续到流结束
//	systemPrompt — 系统级提示词（设定 AI 角色和行为规则）
//	messages     — 历史对话消息列表
//
// 返回值是一个只读的 StreamChunk 通道，调用方需要：
// 1. 用 range 循环读取
// 2. 检查 chunk.Error
// 3. 检查 chunk.Done
// 4. 处理 chunk.Token
//
// 断路器集成：
//   - 如果 LLCircuitBreaker.Allow() 返回 false，说明电路已断开
//     （之前发生了过多错误），直接返回降级响应
//   - RecordSuccess() / RecordFailure() 在成功/失败时调用
//     断路器会根据这些数据自动切换开/关/半开状态
func StreamChat(ctx context.Context, systemPrompt string, messages []ChatMessage) <-chan StreamChunk {
	ch := make(chan StreamChunk, 64)

	go func() {
		defer close(ch)

		// ================================================================
		// 断路器检查 — 快速失败
		// ================================================================
		// 当 LLM API 连续出错时，断路器断开（open），
		// 后续请求直接返回降级消息，不发起实际 HTTP 调用。
		// 这样可以防止雪崩效应（cascading failure）。
		if !LLCircuitBreaker.Allow() {
			ch <- StreamChunk{Token: DegradedMessage()}
			ch <- StreamChunk{Done: true}
			return
		}

		// ================================================================
		// 构造请求体
		// ================================================================
		// system prompt 作为第一条 system 角色的消息发送，
		// 用户的 messages 紧随其后。
		allMsgs := make([]ChatMessage, 0, len(messages)+1)
		allMsgs = append(allMsgs, ChatMessage{Role: "system", Content: systemPrompt})
		allMsgs = append(allMsgs, messages...)

		body := chatRequest{
			Model:     Cfg.Model,
			Messages:  allMsgs,
			Stream:    true,
			MaxTokens: 2048,
		}

		payload, err := json.Marshal(body)
		if err != nil {
			LLCircuitBreaker.RecordFailure()
			ch <- StreamChunk{Error: "failed to marshal request"}
			return
		}

		// ================================================================
		// 发送 HTTP 请求
		// ================================================================
		url := strings.TrimRight(Cfg.BaseURL, "/") + "/chat/completions"
		req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(payload)))
		if err != nil {
			LLCircuitBreaker.RecordFailure()
			ch <- StreamChunk{Error: "failed to create request"}
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+Cfg.APIKey)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			LLCircuitBreaker.RecordFailure()
			ch <- StreamChunk{Error: fmt.Sprintf("LLM API error: %v", err)}
			return
		}
		defer resp.Body.Close()

		// ================================================================
		// 错误处理
		// ================================================================
		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
			LLCircuitBreaker.RecordFailure()
			ch <- StreamChunk{Error: fmt.Sprintf("LLM API returned %d: %s", resp.StatusCode, string(bodyBytes))}
			return
		}

		// 成功收到响应 → 通知断路器
		LLCircuitBreaker.RecordSuccess()

		// ================================================================
		// 解析 SSE 流
		// ================================================================
		// OpenAI SSE 格式：
		//   data: {"choices":[{"delta":{"content":"Hello"}}]}
		//   data: {"choices":[{"delta":{"content":" world"}}]}
		//   data: [DONE]
		//
		// 每一行以 "data: " 开头，后面是 JSON。
		// 流的结束标记是 "data: [DONE]"。
		reader := bufio.NewReader(resp.Body)
		for {
			select {
			case <-ctx.Done():
				// 客户端取消或超时
				return
			default:
			}

			line, err := reader.ReadString('\n')
			if err != nil {
				if err == io.EOF {
					ch <- StreamChunk{Done: true}
					return
				}
				ch <- StreamChunk{Error: "stream read error"}
				return
			}

			line = strings.TrimSpace(line)
			if line == "" || !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				ch <- StreamChunk{Done: true}
				return
			}

			// 解析 SSE data JSON
			var sseData struct {
				Choices []struct {
					Delta struct {
						Content string `json:"content"`
					} `json:"delta"`
				} `json:"choices"`
			}
			if err := json.Unmarshal([]byte(data), &sseData); err != nil {
				continue
			}

			// 提取 token 内容并发送到 channel
			for _, choice := range sseData.Choices {
				if choice.Delta.Content != "" {
					ch <- StreamChunk{Token: choice.Delta.Content}
				}
			}
		}
	}()

	return ch
}
