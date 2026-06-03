package middleware

import (
	"log/slog"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ============================================================================
// 结构化日志中间件
// ============================================================================
//
// 提供两个功能：
//
// 1. 全局日志配置（InitLogger）
//    配置 Go 1.21 标准库 log/slog 包，使用 JSON 格式输出，
//    日志级别由 LOG_LEVEL 环境变量控制。
//
// 2. 请求日志中间件（StructuredLogger + RequestID）
//    每个 HTTP 请求输出一条 JSON 格式的结构化日志，
//    包含：请求 ID、方法、路径、状态码、延迟、客户端 IP。
//
// 日志级别根据状态码自动选择：
//   2xx → INFO
//   4xx → WARN
//   5xx → ERROR
//
// 请求 ID 链路：
//   如果客户端在请求头中带了 X-Request-ID，则沿用；
//   否则由服务端生成 UUID。
//   响应头也返回 X-Request-ID，方便追踪。

// InitLogger 配置全局结构化 JSON 日志。
// 日志级别由 LOG_LEVEL 环境变量控制：
//
//	"debug" → 全部输出
//	"info"  → 默认
//	"warn"  → 仅警告和错误
//	"error" → 仅错误
func InitLogger() {
	level := slog.LevelInfo
	switch os.Getenv("LOG_LEVEL") {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// 将 "msg" 重命名为 "message"，兼容日志采集系统
			if a.Key == slog.MessageKey {
				a.Key = "message"
			}
			return a
		},
	})
	slog.SetDefault(slog.New(handler))
}

// RequestID 中间件注入或延续请求 ID。
//
// 如果客户端请求头中有 X-Request-ID，则沿用（适用于微服务间调用）；
// 否则生成一个 UUID v4。
// 响应头也会设置 X-Request-ID，方便客户端追踪。
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.GetHeader("X-Request-ID")
		if rid == "" {
			rid = uuid.New().String()
		}
		c.Set("request_id", rid)
		c.Header("X-Request-ID", rid)
		c.Next()
	}
}

// StructuredLogger 返回一个 Gin 中间件，为每个请求输出结构化日志。
//
// 日志格式（JSON）：
//
//	{"level":"info","message":"request completed",
//	 "request_id":"abc123","method":"GET","path":"/api/problems",
//	 "status":200,"latency":"45ms","client_ip":"10.0.0.1"}
//
// 错误信息（如果有）也会包含在日志中。
func StructuredLogger() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		latency := time.Since(start)
		status := c.Writer.Status()
		rid, _ := c.Get("request_id")

		attrs := []slog.Attr{
			slog.String("request_id", rid.(string)),
			slog.String("method", c.Request.Method),
			slog.String("path", c.Request.URL.Path),
			slog.Int("status", status),
			slog.String("latency", latency.String()),
			slog.String("client_ip", c.ClientIP()),
		}

		// 如果有 Gin 错误，追加到日志
		if len(c.Errors) > 0 {
			attrs = append(attrs, slog.String("error", c.Errors.String()))
		}

		// 根据状态码选择日志级别
		if status >= 500 {
			slog.LogAttrs(c.Request.Context(), slog.LevelError, "request completed", attrs...)
		} else if status >= 400 {
			slog.LogAttrs(c.Request.Context(), slog.LevelWarn, "request completed", attrs...)
		} else {
			slog.LogAttrs(c.Request.Context(), slog.LevelInfo, "request completed", attrs...)
		}
	}
}
