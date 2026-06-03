package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"judgex/internal/database"
)

// ============================================================================
// 排行榜控制器 — 全局 Leaderboard
// ============================================================================
//
// 全局排行榜显示所有用户按照 AC 题目数量排序的榜单。
// 与比赛排行榜不同，全局排行榜是简单的 SQL 聚合查询。
//
// 比赛排行榜（contest_rank.go）使用 Redis ZSet 实现实时排序，
// 而全局排行榜每次请求直接查数据库，因为数据量不大且不需要实时性。
//
// SQL 逻辑：
//   SELECT user_id, username, COUNT(DISTINCT problem_id) AS solved
//   FROM submissions JOIN users
//   WHERE status = 'Accepted'
//   GROUP BY user_id
//   ORDER BY solved DESC
//   LIMIT 50
//
// 使用 DISTINCT 确保每个用户每道题只计数一次（重复 AC 不重复计数）。

type LeaderboardHandler struct{}

func NewLeaderboardHandler() *LeaderboardHandler {
	return &LeaderboardHandler{}
}

// rankEntry 是排行榜中的一个条目。
type rankEntry struct {
	UserID   uint   `json:"user_id"`
	Username string `json:"username"`
	Solved   int64  `json:"solved"` // 解答题目数
}

// Get 处理 GET /api/leaderboard。
// 返回 AC 题目数最多的前 50 名用户。
//
// 排行榜逻辑：
//   - 只统计状态为 "Accepted" 的提交
//   - 每个用户每道题只计数一次（DISTINCT）
//   - 按解题数降序排列
//   - 取前 50 名
func (h *LeaderboardHandler) Get(c *gin.Context) {
	var entries []rankEntry
	database.DB.Raw(`
		SELECT s.user_id, u.username, COUNT(DISTINCT s.problem_id) AS solved
		FROM submissions s
		JOIN users u ON u.id = s.user_id
		WHERE s.status = 'Accepted'
		GROUP BY s.user_id, u.username
		ORDER BY solved DESC
		LIMIT 50
	`).Scan(&entries)

	// 确保返回空数组而非 nil（前端 JSON 解析友好）
	if entries == nil {
		entries = []rankEntry{}
	}

	c.JSON(http.StatusOK, gin.H{"leaderboard": entries})
}
