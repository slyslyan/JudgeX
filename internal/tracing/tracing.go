package tracing

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

// ============================================================================
// 分布式追踪 — OpenTelemetry
// ============================================================================
//
// tracing 包实现了基于 OpenTelemetry 的分布式追踪。
// 追踪系统帮助理解请求在微服务间的完整调用链。
//
// 在 JudgeX 中，一个判题请求经过的路径：
//   HTTP 请求 → API Server → 消息队列 (NSQ/Redis) → Judge Worker → 沙箱 → 数据库
//
// 通过在关键路径上创建 Span，我们可以追踪每个请求经过的服务和耗时。
//
// 两种导出方式：
//   1. OTLP gRPC（生产环境）：设置 OTEL_EXPORTER_OTLP_ENDPOINT=jaeger:4317
//      将追踪数据发送到 Jaeger、Tempo 等兼容后端
//   2. stdout（开发环境）：不设置环境变量，打印到标准错误输出
//
// W3C TraceContext 传播：
//   通过 HTTP 头（traceparent）在服务间传递追踪上下文。
//   API 服务器生成 traceparent → 放入消息队列消息 →
//   Worker 从中提取 → 创建子 Span。
//   这样在 Jaeger 中能看到：HTTP 请求 → 判题 Worker 的完整调用链。

// Tracer 是全局的追踪器实例。
// 其他包通过 tracing.Tracer 创建 Span。
var Tracer trace.Tracer

const ServiceName = "judgex"

// Init 初始化 OpenTelemetry 追踪系统。
//
// 环境变量：
//
//	OTEL_EXPORTER_OTLP_ENDPOINT — OTLP gRPC 端点（如 "jaeger:4317"）
//	                                如果未设置，使用 stdout 导出器
//
// 返回一个关闭函数（cleanup），应在 main 中 defer 调用。
//
// 配置说明：
//   - 使用 W3C TraceContext 和 Baggage 传播器
//   - 批次导出（5 秒一批，每批最多 256 条 Span）
//   - 始终采样（AlwaysSample）：开发阶段打印所有请求
func Init() func() {
	// 设置文本传播器（W3C TraceContext 标准）
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	var exporter sdktrace.SpanExporter
	var err error

	// 判断使用 OTLP gRPC 还是 stdout
	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint != "" {
		// 生产环境：OTLP gRPC 导出
		exporter, err = otlptracegrpc.New(context.Background(),
			otlptracegrpc.WithEndpoint(endpoint),
			otlptracegrpc.WithInsecure(),
		)
		if err != nil {
			log.Printf("[tracing] OTLP exporter failed: %v, falling back to stdout", err)
		}
	}

	if exporter == nil {
		// 开发环境：标准输出导出（人类可读格式）
		exporter, err = stdouttrace.New(stdouttrace.WithPrettyPrint())
		if err != nil {
			log.Printf("[tracing] stdout exporter failed: %v", err)
			return func() {}
		}
	}

	// 创建服务资源（标识服务名称和版本）
	res, _ := resource.Merge(resource.Default(),
		resource.NewWithAttributes(
			"https://opentelemetry.io/schemas/1.29.0",
			attribute.String("service.name", ServiceName),
			attribute.String("service.version", "1.0.0"),
		),
	)

	// 创建 TraceProvider
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter,
			sdktrace.WithBatchTimeout(5*time.Second), // 每 5 秒批量导出一次
			sdktrace.WithMaxExportBatchSize(256),     // 每批最多 256 条 Span
		),
		sdktrace.WithSampler(sdktrace.AlwaysSample()), // 采样率 100%（开发环境）
		sdktrace.WithResource(res),
	)

	otel.SetTracerProvider(tp)
	Tracer = tp.Tracer(ServiceName)

	fmt.Fprintf(os.Stderr, "[tracing] initialized, endpoint=%q\n", endpoint)
	return func() {
		// 关闭函数：优雅关闭 TraceProvider，确保待导出的 Span 被发送
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tp.Shutdown(ctx); err != nil {
			log.Printf("[tracing] shutdown: %v", err)
		}
	}
}
