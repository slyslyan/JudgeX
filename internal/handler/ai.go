package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"judgex/internal/diagnostics"
	"judgex/internal/queue"
)

// SRESnapshot 返回系统的实时快照（不调用 AI）。
// 包含队列状态、沙箱状态、数据库连接、最近错误等。
func SRESnapshot(c *gin.Context) {
	_, bufLen, _, workerCount := queue.Stats()
	snap := diagnostics.Collect(bufLen, workerCount)
	c.JSON(http.StatusOK, snap)
}
