package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"judgex/internal/cache"
	"judgex/internal/database"
	"judgex/internal/model"
)

// ============================================================================
// 比赛排名系统
// ============================================================================
//
// 比赛排名使用 Redis 有序集合（ZSet）存储，支持实时更新和查询。
// 与 MySQL 存储的永久数据不同，Redis ZSet 提供了高效的排名查询和更新。
//
// Redis Key 设计：
//
//	contest:<id>:rank          ZSet — 排名表（score = 复合分数）
//	contest:<id>:solved:<uid>  Hash — 用户已解决的题目（ACM 模式）
//	contest:<id>:wrong         Hash — 用户对各题目的错误次数（ACM 模式）
//	contest:<id>:score:<uid>:<pid>  Key-Value — 用户某题得分（IOI 模式）
//	contest:<id>:time:<uid>:<pid>   Key-Value — 用户某题用时（IOI 模式）
//
// 排名推送：
// 每次排名更新时通过 Redis Pub/Sub 发布消息到 "contest:<id>:rank_update" 频道，
// SSE 客户端监听该频道，收到消息后自动刷新排行榜。

// UpdateContestRanking 在 judgeWorker 完成判题后被调用。
// 根据比赛规则类型（ACM / IOI）选择不同的排名更新策略。
//
// 调用链：
//
//	submissionHandler.Submit → queue.Publish → worker.JudgeTask
//	→ judge.Run → judgeWorker 更新数据库 → UpdateContestRanking
func UpdateContestRanking(contestID uint, userID uint, problemID uint, status string, createdAt time.Time) {
	var contest model.Contest
	if err := database.DB.First(&contest, contestID).Error; err != nil {
		return
	}

	if contest.RuleType == "IOI" {
		updateIOIRanking(contestID, userID, problemID, status, contest.StartTime)
	} else {
		updateACMRanking(contestID, userID, problemID, status, createdAt, contest.StartTime)
	}
}

// ============================================================================
// ACM 排名算法
// ============================================================================
//
// ACM 模式（ICPC 标准）：
// - 二进制判题：通过得满分，不通过 0 分
// - 罚时：从比赛开始到 AC 的时间 + 20分钟 × 该题错误提交次数
// - 排名规则：按解题数降序 → 同解题数按总罚时升序
//
// 复合分数计算：
//
//	score = solved × 1,000,000 − total_penalty
//
// 使用 1,000,000 作为乘数确保解题数的权重远大于罚时。
// 例如：3 题 45 分钟 → score = 3,000,000 - 45 = 2,999,955
// Redis ZRevRange 按 score 降序排列，得分最高的排第一。

// updateACMRanking 更新 ACM 模式排名。
//
// 流程：
// 1. 如果提交不是 Accepted，错误次数 +1，不更新排名
// 2. 如果已经 AC 过此题，忽略（防止多 AC 刷分）
// 3. 计算罚时 = 解题时间（秒）+ 20min × 错误次数
// 4. 更新 ZSet 中的复合分数
// 5. 发布排名变更通知
func updateACMRanking(contestID uint, userID uint, problemID uint, status string, createdAt time.Time, startTime time.Time) {
	wrongKey := fmt.Sprintf("contest:%d:wrong", contestID)
	wrongField := fmt.Sprintf("%d:%d", userID, problemID)

	// 如果没 AC，增加错误计数，不更新排名
	if status != "Accepted" {
		cache.HIncrBy(wrongKey, wrongField, 1)
		return
	}

	// 检查是否已经 AC 过这道题（防止重复计算）
	solvedKey := fmt.Sprintf("contest:%d:solved:%d", contestID, userID)
	problemStr := strconv.FormatUint(uint64(problemID), 10)
	if _, exists := cache.HGet(solvedKey, problemStr); exists {
		return
	}
	cache.HIncrBy(solvedKey, problemStr, 1)

	// 计算罚时 = 解题用时（秒）+ 错误次数 × 20 分钟
	wrongStr, _ := cache.HGet(wrongKey, wrongField)
	wrongCount, _ := strconv.Atoi(wrongStr)

	solveTime := int(createdAt.Sub(startTime).Seconds())
	if solveTime < 0 {
		solveTime = 0
	}
	penalty := solveTime + wrongCount*20*60

	// 读取用户当前的排名数据
	rankKey := fmt.Sprintf("contest:%d:rank", contestID)
	userStr := strconv.FormatUint(uint64(userID), 10)
	var prevSolved, prevPenalty int
	for _, m := range cache.ZRevRangeWithScores(rankKey, 0, -1) {
		if m.Member == userStr {
			prevSolved = (int(m.Score) + 999999) / 1000000
			prevPenalty = prevSolved*1000000 - int(m.Score)
			break
		}
	}

	// 计算新的复合分数
	totalSolved := prevSolved + 1
	totalPenalty := prevPenalty + penalty
	score := float64(totalSolved*1000000 - totalPenalty)
	cache.ZAdd(rankKey, score, userStr)

	// 通过 Redis Pub/Sub 通知 SSE 客户端刷新排行榜
	cache.Publish(fmt.Sprintf("contest:%d:rank_update", contestID), "changed")
}

// ============================================================================
// IOI 排名算法
// ============================================================================
//
// IOI 模式（国际信息学奥林匹克）：
// - 部分得分：每个测试用例有分值，按通过比例给分
// - 多次提交取最高分
// - 罚时：所有已通过测试用例的运行时间总和（时间越少越好）
//
// 复合分数计算：
//
//	score = totalPassed × 10,000,000 − totalTimeMs
//
// 使用 10,000,000 作为乘数（比 ACM 大），因为 IOI 的分数范围更广。
// 例如：总通过 15 个测试点，总用时 500ms → score = 150,000,000 - 500 = 149,999,500

// updateIOIRanking 更新 IOI 模式排名。
//
// 流程：
// 1. 从数据库获取最新提交的 passed_count
// 2. 如果当前得分低于历史最高分，忽略（保留最高分）
// 3. 更新该用户该题的得分和用时
// 4. 重新计算该用户的总分
func updateIOIRanking(contestID uint, userID uint, problemID uint, status string, startTime time.Time) {
	scoreKey := fmt.Sprintf("contest:%d:score:%d:%d", contestID, userID, problemID)
	timeKey := fmt.Sprintf("contest:%d:time:%d:%d", contestID, userID, problemID)

	// 读取最新提交的 passed_count
	var latestSub model.Submission
	if err := database.DB.Where("user_id = ? AND problem_id = ? AND contest_id = ?",
		userID, problemID, contestID).Order("id DESC").First(&latestSub).Error; err != nil {
		return
	}

	passed := latestSub.PassedCount
	total := latestSub.TotalCases
	if total == 0 {
		return
	}

	// 只保留最高分
	var bestPassed int
	cache.Get(scoreKey, &bestPassed)
	if passed <= bestPassed && bestPassed > 0 {
		return
	}

	cache.Set(scoreKey, passed, 48*time.Hour)
	cache.Set(timeKey, latestSub.TimeUsed, 48*time.Hour)

	// 重新计算用户所有题目的总分
	recomputeIOIRank(contestID, userID)
}

// recomputeIOIRank 重新计算用户在 IOI 比赛中的总分。
// 遍历比赛的所有题目，累加每题的最佳得分和总用时。
func recomputeIOIRank(contestID uint, userID uint) {
	var cps []model.ContestProblem
	database.DB.Where("contest_id = ?", contestID).Find(&cps)

	totalPassed := 0
	totalTimeMs := 0

	for _, cp := range cps {
		scoreKey := fmt.Sprintf("contest:%d:score:%d:%d", contestID, userID, cp.ProblemID)
		timeKey := fmt.Sprintf("contest:%d:time:%d:%d", contestID, userID, cp.ProblemID)

		var passed int
		if cache.Get(scoreKey, &passed) {
			totalPassed += passed
		}
		var t int
		if cache.Get(timeKey, &t) {
			totalTimeMs += t
		}
	}

	if totalPassed > 0 {
		rankKey := fmt.Sprintf("contest:%d:rank", contestID)
		userStr := strconv.FormatUint(uint64(userID), 10)
		score := float64(totalPassed*10000000 - totalTimeMs)
		cache.ZAdd(rankKey, score, userStr)
		cache.Publish(fmt.Sprintf("contest:%d:rank_update", contestID), "changed")
	}
}

// ============================================================================
// 排行榜查询和 SSE 推送
// ============================================================================

// GetLeaderboard 处理 GET /api/contests/:id/leaderboard。
// 从 Redis ZSet 读取排名前 50 的数据，解析复合分数并返回。
//
// ACM 模式解码：
//
//	score = solved × 1,000,000 − penalty
//	→ solved = (score + 999,999) / 1,000,000
//	→ penalty = solved × 1,000,000 − score
//
// IOI 模式解码：
//
//	score = totalPassed × 10,000,000 − totalTimeMs
//	→ totalPassed = score / 10,000,000
//	→ totalTimeMs = score % 10,000,000
func (h *ContestHandler) GetLeaderboard(c *gin.Context) {
	contestID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid contest id"})
		return
	}

	var contest model.Contest
	if database.DB.Select("rule_type").First(&contest, uint(contestID)).Error != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "contest not found"})
		return
	}

	rankKey := fmt.Sprintf("contest:%d:rank", uint(contestID))
	entries := cache.ZRevRangeWithScores(rankKey, 0, 49)

	type RankEntry struct {
		Rank     int    `json:"rank"`
		UserID   uint   `json:"user_id"`
		Username string `json:"username"`
		Solved   int    `json:"solved"`
		Penalty  int    `json:"penalty"`
		Score    int    `json:"score,omitempty"`
	}

	leaderboard := make([]RankEntry, 0, len(entries))
	for i, entry := range entries {
		uid, err := strconv.ParseUint(entry.Member, 10, 64)
		if err != nil {
			continue
		}

		username := ""
		var user model.User
		if database.DB.Select("username").First(&user, uid).Error == nil {
			username = user.Username
		}

		if contest.RuleType == "IOI" {
			score := int(entry.Score) / 10000000
			penalty := int(entry.Score) % 10000000
			if penalty < 0 {
				penalty = 0
			}
			leaderboard = append(leaderboard, RankEntry{
				Rank:     i + 1,
				UserID:   uint(uid),
				Username: username,
				Score:    score,
				Penalty:  penalty,
			})
		} else {
			solved := (int(entry.Score) + 999999) / 1000000
			penalty := solved*1000000 - int(entry.Score)
			if penalty < 0 {
				penalty = 0
			}
			leaderboard = append(leaderboard, RankEntry{
				Rank:     i + 1,
				UserID:   uint(uid),
				Username: username,
				Solved:   solved,
				Penalty:  penalty,
			})
		}
	}

	c.JSON(http.StatusOK, gin.H{"leaderboard": leaderboard, "rule_type": contest.RuleType})
}

// StreamLeaderboard 处理 GET /api/contests/:id/leaderboard/events。
// SSE 端点：当排名发生变化时推送 "update" 事件，客户端收到后调用
// GetLeaderboard 刷新数据。
//
// 工作原理：
// 1. 订阅 Redis Pub/Sh 频道 "contest:<id>:rank_update"
// 2. 当 UpdateContestRanking 发布消息时，SSE 连接收到 "update" 事件
// 3. 客户端 JS 监听 message 事件，检测到 "update" 则重新 fetch 排行榜
//
// 这种"通知 + 拉取"模式避免了直接推送全量排行数据（每次都推送 50 条数据很低效）
func (h *ContestHandler) StreamLeaderboard(c *gin.Context) {
	contestID, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid contest id"})
		return
	}

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.WriteHeader(http.StatusOK)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return
	}

	channel := fmt.Sprintf("contest:%d:rank_update", contestID)
	ch, unsub := cache.Subscribe(channel)
	defer unsub()

	// 发送初始连接确认
	writeSSE := func(data string) {
		fmt.Fprintf(c.Writer, "data: %s\n\n", data)
		flusher.Flush()
	}
	writeSSE("connected")

	// 监听排名变更通知或客户端断开
	ctx := c.Request.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-ch:
			if !ok {
				return
			}
			writeSSE("update")
		}
	}
}
