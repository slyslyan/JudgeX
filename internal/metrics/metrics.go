package metrics

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime"
	"sync/atomic"
	"time"
)

// ============================================================================
// 监控指标 & 结构化日志
// ============================================================================
//
// metrics 包提供轻量级的 Prometheus 兼容指标收集和结构化日志输出。
//
// 为什么不用 Prometheus Go SDK？
//   为了最小化外部依赖。本项目使用原子操作（sync/atomic）实现线程安全的计数器，
//   自行输出 Prometheus 文本格式，同样可以被 Prometheus Server 抓取。
//
// 监控指标（在 /metrics 端点暴露）：
//
//   Counter（计数器，只增不减）：
//     - judgex_submissions_total          — 总提交数
//     - judgex_submissions_accepted       — AC 数
//     - judgex_submissions_wrong_answer   — WA 数
//     - judgex_submissions_tle           — TLE 数
//     - judgex_submissions_runtime_error  — RE/CE 数
//     - judgex_api_requests_total         — API 总请求数
//     - judgex_api_errors_total           — API 错误数
//     - judgex_api_latency_total          — 延迟采样总数
//     - judgex_api_latency_sum_ms        — 延迟总和（ms）
//     - judgex_api_latency_bucket{le=""} — 延迟分布直方图
//
//   Gauge（可增可减的当前值）：
//     - judgex_queue_depth               — 判题队列深度
//     - judgex_active_judgements          — 正在判题数
//     - judgex_uptime_seconds            — 服务运行时长
//     - judgex_go_goroutines             — Go 协程数
//     - judgex_go_mem_alloc_bytes        — 内存分配量
//     - judgex_disk_free_percent         — 磁盘剩余百分比
//
// 结构化日志：
//   提供 JSON 格式的日志输出（StructuredLogger），
//   包含 level、msg、time 和自定义 fields。
//   便于日志收集系统（如 ELK、Loki）解析。

// ============================================================================
// 原子计数器
// ============================================================================
// 使用 int64 和 atomic 包保证并发安全。
// 计数器分布在不同的变量中，避免缓存行伪共享问题。

var (
	SubmissionTotal  int64 // 所有提交的总数
	SubmissionAC     int64 // Accepted 的数量
	SubmissionWA     int64 // Wrong Answer 的数量
	SubmissionTLE    int64 // Time Limit Exceeded 的数量
	SubmissionRE     int64 // Runtime Error / Compile Error 的数量
	QueueDepth       int64 // 判题队列的当前积压数量
	ActiveJudgements int64 // 正在执行的判题任务数
	APITotalRequests int64 // API 总请求数
	APIErrorRequests int64 // API 错误响应数

	// 延迟分布直方图（毫秒级桶）
	// 桶边界：5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000
	latencyBuckets = []float64{5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000}
	latencyCounts  = make([]int64, len(latencyBuckets)) // 每个桶的计数
	latencyTotal   int64                                // 总采样数
	latencySumMs   int64                                // 总延迟（用于计算平均值）
)

// 系统级指标
var (
	startTime       = time.Now() // 服务启动时间
	diskFreePercent int64        // 磁盘剩余百分比（SRE 监控定时更新）
	nodeRestarts    int64        // 节点重启计数
)

// SetDiskFreePercent 设置磁盘剩余空间百分比（由 SRE 监控定时写入）。
func SetDiskFreePercent(pct int) { atomic.StoreInt64(&diskFreePercent, int64(pct)) }

// GetDiskFreePercent 读取磁盘剩余空间百分比。
func GetDiskFreePercent() int { return int(atomic.LoadInt64(&diskFreePercent)) }

// IncNodeRestart 增加节点重启计数（用于 SRE 监控）。
func IncNodeRestart(nodeID string) {
	atomic.AddInt64(&nodeRestarts, 1)
	log.Printf("[metrics] node restart: %s (total: %d)", nodeID, atomic.LoadInt64(&nodeRestarts))
}

// IncSubmission 增加提交计数器，并按状态分类统计。
// 状态分类：
//
//	Accepted → AC
//	Wrong Answer → WA
//	Time Limit Exceeded → TLE
//	Runtime Error / Compile Error → RE
//	其他状态 → 只增加总数，不分类
func IncSubmission(status string) {
	atomic.AddInt64(&SubmissionTotal, 1)
	switch status {
	case "Accepted":
		atomic.AddInt64(&SubmissionAC, 1)
	case "Wrong Answer":
		atomic.AddInt64(&SubmissionWA, 1)
	case "Time Limit Exceeded":
		atomic.AddInt64(&SubmissionTLE, 1)
	case "Runtime Error", "Compile Error":
		atomic.AddInt64(&SubmissionRE, 1)
	}
}

// SetQueueDepth 设置判题队列深度（由 queue.Stats() 定时更新）。
func SetQueueDepth(n int64) { atomic.StoreInt64(&QueueDepth, n) }

// IncActiveJudge 增加活跃判题计数（开始判题时调用）。
func IncActiveJudge() { atomic.AddInt64(&ActiveJudgements, 1) }

// DecActiveJudge 减少活跃判题计数（判题完成时调用）。
func DecActiveJudge() { atomic.AddInt64(&ActiveJudgements, -1) }

// IncAPIRequest 增加 API 总请求计数。
func IncAPIRequest() { atomic.AddInt64(&APITotalRequests, 1) }

// IncAPIError 增加 API 错误计数。
func IncAPIError() { atomic.AddInt64(&APIErrorRequests, 1) }

// ObserveLatency 记录一次 API 请求延迟。
// 同时记录到总和（用于计算平均延迟）和直方图桶（用于分布分析）。
func ObserveLatency(d time.Duration) {
	ms := d.Milliseconds()
	atomic.AddInt64(&latencyTotal, 1)
	atomic.AddInt64(&latencySumMs, ms)
	for i, bucket := range latencyBuckets {
		if float64(ms) <= bucket {
			atomic.AddInt64(&latencyCounts[i], 1)
		}
	}
}

// ============================================================================
// Prometheus /metrics 端点
// ============================================================================

// Handler 返回一个 HTTP handler，暴露 Prometheus 格式的指标。
// 在 cmd/server/main.go 中注册到 GET /metrics。
//
// Prometheus 文本格式说明：
//
//	# HELP judgex_submissions_total Total number of submissions
//	# TYPE judgex_submissions_total counter
//	judgex_submissions_total 42
func Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")
		var m runtime.MemStats
		runtime.ReadMemStats(&m)

		write := func(name, typ, help string, value interface{}) {
			w.Write([]byte("# HELP " + name + " " + help + "\n"))
			w.Write([]byte("# TYPE " + name + " " + typ + "\n"))
			b, _ := json.Marshal(value)
			w.Write([]byte(name + " " + string(b) + "\n"))
		}

		write("judgex_submissions_total", "counter", "Total number of submissions",
			atomic.LoadInt64(&SubmissionTotal))
		write("judgex_submissions_accepted", "counter", "Accepted submissions",
			atomic.LoadInt64(&SubmissionAC))
		write("judgex_submissions_wrong_answer", "counter", "Wrong Answer submissions",
			atomic.LoadInt64(&SubmissionWA))
		write("judgex_submissions_tle", "counter", "Time Limit Exceeded submissions",
			atomic.LoadInt64(&SubmissionTLE))
		write("judgex_submissions_runtime_error", "counter", "Runtime/Compile Error submissions",
			atomic.LoadInt64(&SubmissionRE))
		write("judgex_queue_depth", "gauge", "Current judge queue depth",
			atomic.LoadInt64(&QueueDepth))
		write("judgex_active_judgements", "gauge", "Currently running judgements",
			atomic.LoadInt64(&ActiveJudgements))
		write("judgex_api_requests_total", "counter", "Total API requests",
			atomic.LoadInt64(&APITotalRequests))
		write("judgex_api_errors_total", "counter", "API error responses",
			atomic.LoadInt64(&APIErrorRequests))
		write("judgex_uptime_seconds", "gauge", "Server uptime in seconds",
			int64(time.Since(startTime).Seconds()))
		write("judgex_go_goroutines", "gauge", "Number of goroutines",
			runtime.NumGoroutine())
		write("judgex_go_mem_alloc_bytes", "gauge", "Memory allocated (bytes)",
			m.Alloc)

		write("judgex_disk_free_percent", "gauge", "Free disk space on test data path (%)",
			atomic.LoadInt64(&diskFreePercent))
		// 延迟直方图
		total := atomic.LoadInt64(&latencyTotal)
		sumMs := atomic.LoadInt64(&latencySumMs)
		write("judgex_api_latency_total", "counter", "Total API requests measured for latency",
			total)
		write("judgex_api_latency_sum_ms", "counter", "Sum of API latencies in milliseconds",
			sumMs)
		for i, b := range latencyBuckets {
			write(fmt.Sprintf("judgex_api_latency_bucket{le=%q}", fmt.Sprintf("%g", b)),
				"counter", fmt.Sprintf("API requests with latency <= %g ms", b),
				atomic.LoadInt64(&latencyCounts[i]))
		}
	})
}

// ============================================================================
// 结构化日志
// ============================================================================

// StructuredLogger 提供 JSON 格式的结构化日志输出。
// 与标准 log 包不同，每条日志输出为一行 JSON，便于日志收集系统解析。
//
// 示例输出：
//
//	{"level":"info","msg":"server started","time":"2026-05-31T12:00:00Z","port":8080}
type StructuredLogger struct {
	logger *log.Logger
}

// NewStructuredLogger 创建一个新的结构化日志记录器。
func NewStructuredLogger() *StructuredLogger {
	return &StructuredLogger{logger: log.New(os.Stdout, "", 0)}
}

// Info 输出一条 INFO 级别的结构化日志。
func (l *StructuredLogger) Info(fields map[string]interface{}, msg string) {
	fields["level"] = "info"
	fields["msg"] = msg
	fields["time"] = time.Now().UTC().Format(time.RFC3339)
	b, _ := json.Marshal(fields)
	l.logger.Println(string(b))
}

// Error 输出一条 ERROR 级别的结构化日志。
func (l *StructuredLogger) Error(fields map[string]interface{}, msg string) {
	fields["level"] = "error"
	fields["msg"] = msg
	fields["time"] = time.Now().UTC().Format(time.RFC3339)
	b, _ := json.Marshal(fields)
	l.logger.Println(string(b))
}

// DefaultJSONLog 输出一条 JSON 格式的日志到标准输出。
// 这是包级别的快捷函数，不需要创建 Logger 实例。
func DefaultJSONLog(level, msg string, fields map[string]interface{}) {
	fields["level"] = level
	fields["msg"] = msg
	fields["time"] = time.Now().UTC().Format(time.RFC3339)
	b, _ := json.Marshal(fields)
	log.Println(string(b))
}
