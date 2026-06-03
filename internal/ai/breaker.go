package ai

import (
	"log"
	"sync"
	"time"
)

// ============================================================================
// 断路器 — Circuit Breaker
// ============================================================================
//
// 断路器是一种防止级联故障（cascading failure）的模式。
// 当 LLM API 连续出错时，断路器会自动"跳闸"，后续请求快速返回降级响应，
// 而不是继续调用已经不可用的外部 API，浪费资源和时间。
//
// 三种状态：
//
//   CLOSED（关闭） — 正常状态，所有请求正常发出。
//     连续 N 次失败后 → OPEN。
//
//   OPEN（断开） — 所有请求立即返回降级消息，不调用外部 API。
//     经过 resetTimeout 时间后 → HALF_OPEN。
//
//   HALF_OPEN（半开） — 允许一个探测请求通过。
//     成功 → CLOSED（恢复正常）
//     失败 → OPEN（继续保持断开）
//
// 断路器 vs 重试：
//   重试（retry）在失败时再试一次，适合瞬态故障。
//   断路器在连续失败时彻底停用，适合长时间不可用的情况。
//   两者是互补关系（本项目中断路器在前，重试在队列消息中）。
//
// 降级响应：
//   当断路器断开时，调用方收到 DegradedMessage() 的友好提示，
//   而不是 HTTP 500 或连接超时。

// CircuitState 表示断路器的当前状态。
type CircuitState int

const (
	CircuitClosed   CircuitState = iota // CLOSED — 正常通行
	CircuitOpen                         // OPEN — 断开，请求被阻断
	CircuitHalfOpen                     // HALF_OPEN — 半开，允许探测请求
)

// CircuitBreaker 是 LLM API 的断路器实现。
// 使用 sync.Mutex 保证并发安全（API 请求和判题 Worker 可能同时调用）。
type CircuitBreaker struct {
	mu sync.Mutex

	state            CircuitState // 当前状态
	consecutiveFails int          // 连续失败次数
	lastFailureTime  time.Time    // 最近一次失败的时间
	lastStateChange  time.Time    // 最近一次状态变更的时间

	// 配置参数
	maxFails     int           // 连续失败多少次后跳闸（默认 5）
	resetTimeout time.Duration // 在 OPEN 状态等待多久后进入 HALF_OPEN（默认 30 秒）
}

// NewCircuitBreaker 创建一个新的断路器。
//
//	maxFails: 连续失败多少次后断开
//	resetTimeout: 断开后等待多久再尝试恢复
func NewCircuitBreaker(maxFails int, resetTimeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		state:           CircuitClosed,
		maxFails:        maxFails,
		resetTimeout:    resetTimeout,
		lastStateChange: time.Now(),
	}
}

// Allow 判断是否允许请求通过。
//
//	CLOSED → 允许
//	OPEN → 如果已经到了 resetTimeout，转为 HALF_OPEN 并允许一个探测请求
//	HALF_OPEN → 允许（仅一个，后续通过 RecordSuccess/Failure 决定状态）
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	now := time.Now()

	switch cb.state {
	case CircuitClosed:
		return true

	case CircuitOpen:
		// 检查是否超过了重置超时时间
		if now.Sub(cb.lastStateChange) >= cb.resetTimeout {
			cb.state = CircuitHalfOpen
			cb.lastStateChange = now
			log.Printf("[breaker] circuit HALF_OPEN — allowing probe request")
			return true
		}
		return false

	case CircuitHalfOpen:
		// 半开状态下允许请求（由 RecordSuccess/Failure 决定后续）
		return true

	default:
		return true
	}
}

// RecordSuccess 记录一次成功。
// 重置连续失败计数，将状态恢复为 CLOSED。
// 这通常意味着 LLM API 已恢复健康。
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.consecutiveFails = 0
	if cb.state != CircuitClosed {
		log.Printf("[breaker] circuit CLOSED — LLM API recovered")
	}
	cb.state = CircuitClosed
	cb.lastStateChange = time.Now()
}

// RecordFailure 记录一次失败。
// 增加连续失败计数，如果超过阈值则跳闸（OPEN）。
// 如果在 HALF_OPEN 状态失败，探测请求失败，回到 OPEN。
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.consecutiveFails++
	cb.lastFailureTime = time.Now()

	if cb.consecutiveFails >= cb.maxFails {
		if cb.state != CircuitOpen {
			log.Printf("[breaker] circuit OPEN (%d consecutive failures) — degrading LLM requests",
				cb.consecutiveFails)
		}
		cb.state = CircuitOpen
		cb.lastStateChange = time.Now()
	} else if cb.state == CircuitHalfOpen {
		// 探测请求失败 → 恢复 OPEN
		cb.state = CircuitOpen
		cb.lastStateChange = time.Now()
		log.Printf("[breaker] circuit OPEN — probe request failed")
	}
}

// State 返回当前状态的字符串表示（用于日志和监控）。
func (cb *CircuitBreaker) State() string {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	switch cb.state {
	case CircuitClosed:
		return "closed"
	case CircuitOpen:
		return "open"
	case CircuitHalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}

// Stats 返回断路器的诊断信息（用于 SRE 仪表盘）。
func (cb *CircuitBreaker) Stats() map[string]interface{} {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return map[string]interface{}{
		"state":             cb.stateString(),
		"consecutive_fails": cb.consecutiveFails,
		"last_failure":      cb.lastFailureTime.Format(time.RFC3339),
		"last_state_change": cb.lastStateChange.Format(time.RFC3339),
	}
}

func (cb *CircuitBreaker) stateString() string {
	switch cb.state {
	case CircuitClosed:
		return "closed"
	case CircuitOpen:
		return "open"
	case CircuitHalfOpen:
		return "half_open"
	default:
		return "unknown"
	}
}

// DegradedMessage 返回 AI 不可用时的降级提示消息。
// 当断路器断开时，所有 AI 请求返回此友好提示，
// 引导用户自行思考或者稍后重试。
func DegradedMessage() string {
	return `AI assistant is temporarily unavailable. You can still submit code and view results. Please try again in a moment.

If you're stuck on this problem, try:
- Breaking it down into smaller steps
- Thinking about edge cases
- Reviewing the problem constraints carefully
- Checking the sample cases for patterns

What specific concept would you like help understanding?`
}

// LLCircuitBreaker 是全局默认的 LLM API 断路器单例。
// 配置：连续 5 次失败跳闸，30 秒后尝试恢复。
var LLCircuitBreaker = NewCircuitBreaker(
	5,              // 连续 5 次失败后断开
	30*time.Second, // 30 秒后尝试半开
)
