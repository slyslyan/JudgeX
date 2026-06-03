package diagnostics

import (
	"fmt"
	"os"
	"runtime"
	"time"

	"judgex/internal/bpf"
	"judgex/internal/database"
	"judgex/internal/model"
	"judgex/internal/sandbox"
)

// ============================================================================
// 系统诊断 — SRE 监控数据采集器
// ============================================================================
//
// diagnostics 包提供系统快照（SystemSnapshot）采集功能，用于 SRE 监控面板。
// 它聚合了多个子系统的状态数据，形成系统全貌。
//
// 采集的数据维度：
//   - Queue（队列）— 后端类型、积压长度、Worker 数量
//   - Submissions（提交）— 近 1 小时提交量、AC 率、状态分布、错误聚合
//   - Sandbox（沙箱）— cgroup 状态、运行模式
//   - Database（数据库）— 连接池状态、连通性
//   - Runtime（运行时）— Go 协程数、内存使用、GC 次数
//   - BPF（eBPF 追踪）— 网络延迟异常、缓解措施
//   - RecentErrors（最近错误）— 按题目/状态聚合的错误统计

// SystemSnapshot 是系统当前状态的完整快照。
type SystemSnapshot struct {
	Timestamp    time.Time       `json:"timestamp"`     // 采集时间
	Uptime       string          `json:"uptime"`        // 服务运行时间（如 "2d 3h 15m"）
	Queue        QueueStatus     `json:"queue"`         // 队列状态
	Submissions  SubmissionStats `json:"submissions"`   // 提交统计
	Sandbox      SandboxStatus   `json:"sandbox"`       // 沙箱状态
	Database     DatabaseStatus  `json:"database"`      // 数据库状态
	Runtime      RuntimeInfo     `json:"runtime"`       // Go 运行时信息
	RecentErrors []ErrorSummary  `json:"recent_errors"` // 最近错误汇总
	BPF          bpf.Metrics     `json:"bpf"`           // eBPF 追踪器指标
}

// QueueStatus 消息队列状态。
type QueueStatus struct {
	Backend     string `json:"backend"`       // 队列后端类型（"nsq" / "redis" / "local_channel"）
	LocalBufLen int    `json:"local_buf_len"` // 本地缓冲长度
	WorkerCount int    `json:"worker_count"`  // Worker 数量
	Status      string `json:"status"`        // 健康状态
}

// SubmissionStats 近 1 小时的提交统计。
type SubmissionStats struct {
	LastHourTotal      int64            `json:"last_hour_total"`     // 提交总数
	LastHourAccepted   int64            `json:"last_hour_accepted"`  // AC 数
	AcceptRate         float64          `json:"accept_rate"`         // AC 率（%）
	StatusDistribution map[string]int64 `json:"status_distribution"` // 按状态分布
}

// SandboxStatus 沙箱状态。
type SandboxStatus struct {
	CgroupPath   string `json:"cgroup_path"`   // cgroup v2 路径
	CgroupExists bool   `json:"cgroup_exists"` // cgroup 目录是否存在
	Status       string `json:"status"`        // 健康状态
}

// DatabaseStatus 数据库连接池状态。
type DatabaseStatus struct {
	Connected bool   `json:"connected"`      // 是否已连接
	Status    string `json:"status"`         // 健康状态
	MaxOpen   int    `json:"max_open_conns"` // 最大连接数
	Open      int    `json:"open_conns"`     // 当前连接数
	InUse     int    `json:"in_use_conns"`   // 正在使用的连接数
	Idle      int    `json:"idle_conns"`     // 空闲连接数
}

// RuntimeInfo Go 运行时信息。
type RuntimeInfo struct {
	Goroutines  int    `json:"goroutines"`      // 当前协程数
	MemoryAlloc string `json:"memory_alloc_mb"` // 已分配内存（MB）
	NumGC       uint32 `json:"num_gc_cycles"`   // GC 周期次数
}

// ErrorSummary 按题目/状态聚合的错误摘要。
type ErrorSummary struct {
	Status      string `json:"status"`       // 错误状态（TLE/WA/RE 等）
	ProblemID   uint   `json:"problem_id"`   // 题目 ID
	Language    string `json:"language"`     // 编程语言
	ErrorSample string `json:"error_sample"` // 错误消息样本（截断到 200 字符）
	Count       int64  `json:"count"`        // 该错误出现次数
}

// Collect 采集当前系统的完整快照。
//
// 参数：
//
//	localBufLen — 本地队列缓冲长度（由 queue.Stats() 提供）
//	workerCount — 当前 Worker 数量（由 queue.Stats() 提供）
//
// 返回值：
//
//	SystemSnapshot — 包含所有子系统的状态信息
func Collect(localBufLen int, workerCount int) SystemSnapshot {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	snap := SystemSnapshot{
		Timestamp: time.Now(),
		BPF:       bpf.FetchMetrics(),
		Uptime:    formatUptime(),
		Runtime: RuntimeInfo{
			Goroutines:  runtime.NumGoroutine(),
			MemoryAlloc: fmt.Sprintf("%.1f", float64(m.Alloc)/1024/1024),
			NumGC:       m.NumGC,
		},
		Queue: QueueStatus{
			LocalBufLen: localBufLen,
			WorkerCount: workerCount,
		},
		Sandbox: SandboxStatus{
			CgroupPath: "/sys/fs/cgroup/judgex/",
		},
	}

	// ================================================================
	// 沙箱状态（根据模式区别检查）
	// ================================================================
	// gVisor 模式不需要 cgroup v2，直接标记为 healthy。
	// native 模式需要检查 cgroup 目录是否存在。
	if sandbox.Mode() == "gvisor" {
		snap.Sandbox.Status = "healthy"
	} else if _, err := os.Stat("/sys/fs/cgroup/judgex/"); err == nil {
		snap.Sandbox.CgroupExists = true
		snap.Sandbox.Status = "healthy"
	} else {
		snap.Sandbox.Status = "not_initialized"
	}

	// ================================================================
	// 队列状态
	// ================================================================
	// 本地缓冲超过 768 为 degraded，超过 1000 为 down
	if localBufLen > 768 {
		snap.Queue.Status = "degraded"
	} else if localBufLen > 1000 {
		snap.Queue.Status = "down"
	} else {
		snap.Queue.Status = "healthy"
	}

	// ================================================================
	// 数据库状态
	// ================================================================
	snap.Database.Connected = database.DB != nil
	if database.DB != nil {
		snap.Database.MaxOpen, snap.Database.Open, snap.Database.InUse, snap.Database.Idle = database.PoolStats()
		sqlDB, err := database.DB.DB()
		if err == nil {
			if err := sqlDB.Ping(); err == nil {
				snap.Database.Status = "healthy"
			} else {
				snap.Database.Status = "error"
			}
		} else {
			snap.Database.Status = "healthy"
		}
	} else {
		snap.Database.Status = "down"
	}

	// ================================================================
	// 提交统计（近 1 小时）
	// ================================================================
	if database.DB != nil {
		hourAgo := time.Now().Add(-1 * time.Hour)
		database.DB.Model(&model.Submission{}).Where("created_at > ?", hourAgo).Count(&snap.Submissions.LastHourTotal)
		database.DB.Model(&model.Submission{}).Where("created_at > ? AND status = ?", hourAgo, "Accepted").Count(&snap.Submissions.LastHourAccepted)
		if snap.Submissions.LastHourTotal > 0 {
			snap.Submissions.AcceptRate = float64(snap.Submissions.LastHourAccepted) / float64(snap.Submissions.LastHourTotal) * 100
		}

		// 状态分布（按 status 分组统计）
		snap.Submissions.StatusDistribution = make(map[string]int64)
		type statusCount struct {
			Status string
			Count  int64
		}
		var sc []statusCount
		database.DB.Model(&model.Submission{}).
			Select("status, count(*) as count").
			Where("created_at > ?", hourAgo).
			Group("status").Find(&sc)
		for _, s := range sc {
			snap.Submissions.StatusDistribution[s.Status] = s.Count
		}

		// 最近错误汇总（排除 Accepted 和 pending）
		type errRow struct {
			Status       string
			ProblemID    uint
			Language     string
			ErrorMessage string
			Count        int64
		}
		var errs []errRow
		database.DB.Model(&model.Submission{}).
			Select("status, problem_id, language, error_message, count(*) as count").
			Where("created_at > ? AND status NOT IN (?, ?)", hourAgo, "Accepted", "pending").
			Group("status, problem_id, language, error_message").
			Order("count DESC").
			Limit(10).
			Find(&errs)

		for _, e := range errs {
			sample := e.ErrorMessage
			if len(sample) > 200 {
				sample = sample[:200] + "..."
			}
			snap.RecentErrors = append(snap.RecentErrors, ErrorSummary{
				Status:      e.Status,
				ProblemID:   e.ProblemID,
				Language:    e.Language,
				ErrorSample: sample,
				Count:       e.Count,
			})
		}
	}

	return snap
}

// startTime 记录进程启动时间，用于计算运行时长。
var startTime = time.Now()

// formatUptime 将运行时长格式化为人类可读的字符串。
// 例如：2d 3h 15m 或 5h 30m（不足 1 天时不显示天数）。
func formatUptime() string {
	d := time.Since(startTime)
	days := int(d.Hours()) / 24
	hours := int(d.Hours()) % 24
	mins := int(d.Minutes()) % 60
	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, mins)
	}
	return fmt.Sprintf("%dh %dm", hours, mins)
}
