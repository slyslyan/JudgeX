package middleware

import (
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"

	"judgex/internal/tracing"
)

// ============================================================================
// 分布式追踪中间件 — OpenTelemetry Gin 集成
// ============================================================================
//
// 为每个 HTTP 请求创建一个 OpenTelemetry Span，实现请求级别的追踪。
// 追踪数据最终发送到 Jaeger/Tempo 等后端，用于可视化请求调用链。
//
// 工作流程：
//   1. 从请求头中提取 W3C TraceContext（如果有上游服务调用）
//   2. 创建一个名为 "{METHOD} {PATH}" 的 Span（如 "POST /api/submissions"）
//   3. 设置 HTTP 相关属性（方法、URL、路由、状态码）
//   4. 将 Span 上下文存入 Gin 上下文，供下游 handler 传播到消息队列
//
// 上下文传播：
//   API Server 创建的 Span → TraceParent 字符串 → NSQ/Redis 消息 →
//   Judge Worker 提取 → 创建子 Span
//   这样在 Jaeger 中可以看到完整的：HTTP 请求 → 判题 调用链。

// Tracing 创建一个 Gin 中间件，为每个 HTTP 请求启动一个追踪 Span。
// 如果 tracing.Tracer 为 nil（未初始化），则返回空操作中间件。
//
// Span 命名规则："{HTTP方法} {路由模式}"
//
//	例如：GET /api/problems → "GET /api/problems"
//	      POST /api/submissions → "POST /api/submissions"
//
// 如果路由模式为空（如 404），回退到实际路径。
func Tracing() gin.HandlerFunc {
	if tracing.Tracer == nil {
		return func(c *gin.Context) { c.Next() }
	}

	propagator := propagation.TraceContext{}

	return func(c *gin.Context) {
		// 从传入请求头中提取追踪上下文（W3C TraceContext）
		ctx := propagator.Extract(c.Request.Context(),
			propagation.HeaderCarrier(c.Request.Header))

		spanName := c.Request.Method + " " + c.FullPath()
		if spanName == " " {
			spanName = c.Request.Method + " " + c.Request.URL.Path
		}

		ctx, span := tracing.Tracer.Start(ctx, spanName)
		defer span.End()

		span.SetAttributes(
			attribute.String("http.method", c.Request.Method),
			attribute.String("http.url", c.Request.URL.String()),
			attribute.String("http.route", c.FullPath()),
		)

		// 将追踪上下文存入 Gin 上下文，供下游 handler 使用
		c.Set("trace_ctx", ctx)
		c.Request = c.Request.WithContext(ctx)

		c.Next()

		span.SetAttributes(
			attribute.Int("http.status_code", c.Writer.Status()),
		)
		if len(c.Errors) > 0 {
			span.SetAttributes(attribute.String("gin.errors", c.Errors.String()))
		}
	}
}

// TraceContextFromGin 从 Gin 上下文中提取追踪上下文。
// 用于在 handler 中将追踪上下文传播到消息队列（NSQ/Redis）。
func TraceContextFromGin(c *gin.Context) interface{} {
	if raw, exists := c.Get("trace_ctx"); exists {
		return raw
	}
	return c.Request.Context()
}
