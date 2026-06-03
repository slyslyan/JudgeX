package handler

import (
	"net/http"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"

	"judgex/internal/cache"
	"judgex/internal/database"
	"judgex/internal/queue"
)

// ============================================================================
// 健康检查控制器 — Health / Readiness Probes
// ============================================================================
//
// K8s 和 Docker Compose 使用两类探针来检查服务健康状态：
//
//   Liveness（存活探针）— 检测进程是否还活着
//     `GET /health` — 进程启动就返回 200，不考虑依赖是否就绪
//     如果 Liveness 失败，K8s 会重启容器
//
//   Readiness（就绪探针）— 检测服务是否准备好接收流量
//     `GET /ready` — 检查 MySQL、Redis、队列、磁盘等所有依赖
//     如果 Readiness 失败，K8s 会从 Service 中移除该 Pod
//
// 就绪检查项目：
//   - MySQL：数据库连接是否正常
//   - Redis：缓存服务是否在线（降级也接受）
//   - 队列：消息队列是否正常
//   - 磁盘：测试数据路径磁盘空间是否充足
//   - Goroutine：协程数量是否异常

type HealthHandler struct {
	testDataPath string // 测试数据路径（用于检查磁盘空间）
}

func NewHealthHandler(testDataPath string) *HealthHandler {
	return &HealthHandler{testDataPath: testDataPath}
}

// Liveness 处理 GET /health。
// 简单的存活探针，进程活着就返回 200。
// 不检查任何依赖服务（这是 Readiness 的职责）。
func (h *HealthHandler) Liveness(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
		"ts":     time.Now().UTC().Format(time.RFC3339),
	})
}

// Readiness 处理 GET /ready。
// 检查所有关键依赖的健康状态，只有全部正常才返回 200。
// 任何依赖不正常都返回 503 Service Unavailable。
func (h *HealthHandler) Readiness(c *gin.Context) {
	checks := make(map[string]string)
	allHealthy := true

	// MySQL 数据库检查
	sqlDB, err := database.DB.DB()
	if err != nil || sqlDB.Ping() != nil {
		checks["mysql"] = "unhealthy"
		allHealthy = false
	} else {
		checks["mysql"] = "healthy"
	}

	// Redis 缓存检查（允许降级到内存）
	if cache.Ping() {
		checks["redis"] = "healthy"
	} else {
		checks["redis"] = "degraded"
	}

	// 消息队列检查
	backend, bufLen, nsqOK, _ := queue.Stats()
	if nsqOK {
		checks["queue"] = "healthy (nsq)"
	} else {
		checks["queue"] = "degraded (local, buf=" + strconv.Itoa(bufLen) + ")"
	}

	// 磁盘空间检查（测试数据路径）
	if free, err := DiskFreePercent(h.testDataPath); err == nil {
		if free < 10 {
			// 低于 10% 为严重
			checks["disk"] = "critical (" + strconv.Itoa(free) + "% free)"
			allHealthy = false
		} else if free < 20 {
			// 低于 20% 为警告
			checks["disk"] = "degraded (" + strconv.Itoa(free) + "% free)"
		} else {
			checks["disk"] = "healthy (" + strconv.Itoa(free) + "% free)"
		}
	} else {
		checks["disk"] = "unknown"
	}

	// Goroutine 数量检查（超过 5000 表示可能有泄漏）
	if goroutines := runtime.NumGoroutine(); goroutines > 5000 {
		checks["goroutines"] = "degraded (" + strconv.Itoa(goroutines) + ")"
	} else {
		checks["goroutines"] = "healthy (" + strconv.Itoa(goroutines) + ")"
	}

	status := http.StatusOK
	if !allHealthy {
		status = http.StatusServiceUnavailable
	}

	c.JSON(status, gin.H{
		"status":  map[bool]string{true: "ok", false: "degraded"}[allHealthy],
		"backend": backend,
		"checks":  checks,
		"ts":      time.Now().UTC().Format(time.RFC3339),
	})
}

// DiskFreePercent 获取指定路径所在文件系统的剩余空间百分比。
// 使用 syscall.Statfs 获取 Linux 文件系统统计信息。
//
//	total = blocks × block_size
//	free  = avail_blocks × block_size（普通用户的可用空间，非 root 预留）
//	pct   = free × 100 / total
func DiskFreePercent(path string) (int, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	total := stat.Blocks * uint64(stat.Bsize)
	free := stat.Bavail * uint64(stat.Bsize)
	if total == 0 {
		return 0, nil
	}
	return int(free * 100 / total), nil
}
