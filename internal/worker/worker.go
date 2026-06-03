package worker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"gorm.io/gorm"

	"judgex/internal/cache"
	"judgex/internal/database"
	"judgex/internal/handler"
	"judgex/internal/judge"
	"judgex/internal/metrics"
	"judgex/internal/model"
	"judgex/internal/queue"
	"judgex/internal/storage"
	"judgex/internal/tracing"
)

// ============================================================================
// 判题工作单元 — Judge Worker
// ============================================================================
//
// worker 包是 JudgeX 的判题执行引擎。它从消息队列中消费 JudgeTask，
// 执行以下核心流程：
//
//   1. 从磁盘 / S3 / MySQL（降级）加载测试数据
//   2. 逐条执行测试用例，调用 judge.Run() 进入沙箱运行用户代码
//   3. 比较输出结果，按 ACM（立刻返回）或 IOI（全部运行）规则判定
//   4. 更新数据库中的提交记录（状态、耗时、内存、通过数）
//   5. 通过 Redis Pub/Sub 推送状态到前端的 SSE 端点
//   6. 更新比赛中排行榜（Redis ZSet）
//   7. 缓存去重结果（3秒内相同提交直接返回缓存）
//
// 判题结果状态流转：
//   pending → (judge.Run) → Accepted / Wrong Answer / TLE / MLE / Runtime Error / Compile Error
//   IOI 模式额外支持 "Partial Score"（部分通过）
//
// 测试数据加载优先级：
//   1. S3/MinIO（如果配置了 storage.Default）
//   2. 本地文件系统（TestDataPath/{problemID}/）
//   3. MySQL test_cases 表（降级，兼容旧数据）
//
// accepted_count 更新策略：
//   只有在用户首次 AC 该题目时才 +1，重复 AC 不重复计数（事务中判断）

// TestDataPath 是测试数据文件在本地磁盘的根目录。
// 子目录结构：{TestDataPath}/{problemID}/{num}.in / {num}.out
var TestDataPath string

// JudgeTask 处理单个判题任务，由消息队列消费者调用。
//
// 执行流程：
// 1. 分布式追踪：从队列消息中提取 W3C TraceContext，建立新的 tracing span
// 2. 加载测试数据（磁盘 → MySQL 降级）
// 3. 检测比赛模式（ACM立即失败 / IOI继续运行）
// 4. 逐条运行测试用例，记录最大耗时/内存和错误信息
// 5. 写入数据库并发布 SSE 状态推送
// 6. 更新比赛排行榜
// 7. 缓存去重结果
func JudgeTask(task queue.JudgeTask) {
	metrics.IncActiveJudge()
	defer metrics.DecActiveJudge()

	// ====================================================================
	// 分布式追踪：从消息队列恢复 TraceContext
	// ====================================================================
	// NSQ/Redis 消息中携带 traceparent 头，这里提取后创建子 span，
	// 实现从 API 请求 → 队列 → Worker 的端到端追踪。
	ctx := context.Background()
	if task.TraceParent != "" && tracing.Tracer != nil {
		carrier := propagation.MapCarrier{"traceparent": task.TraceParent}
		ctx = propagation.TraceContext{}.Extract(ctx, carrier)
	}
	ctx, span := tracing.Tracer.Start(ctx, "judge.submission",
		trace.WithAttributes(
			attribute.Int64("submission_id", task.SubmissionID),
			attribute.Int64("problem_id", int64(task.ProblemID)),
			attribute.Int64("user_id", int64(task.UserID)),
			attribute.String("language", task.Language),
		),
	)
	defer span.End()
	_ = ctx

	// ====================================================================
	// 加载测试数据
	// ====================================================================
	// 优先从磁盘读取（支持 Redis 版本缓存加速），
	// 如果磁盘不可用，降级到 MySQL test_cases 表。
	tcs, err := loadTestCasesFromDisk(task.ProblemID)
	if err != nil {
		slog.Error("judge disk load failed, trying MySQL fallback", "submission_id", task.SubmissionID, "error", err)
		tcs, err = loadTestCasesFromMySQL(task.ProblemID)
		if err != nil {
			// 没有测试数据 → 标记为 "No Test Cases" 并结束
			database.DB.Model(&model.Submission{}).Where("id = ?", task.SubmissionID).
				Updates(map[string]interface{}{"status": "No Test Cases"})
			metrics.IncSubmission("No Test Cases")
			return
		}
	}

	// ====================================================================
	// 检测比赛规则类型
	// ====================================================================
	// ACM 模式：遇到第一个失败的测试点就返回（快速失败）
	// IOI 模式：运行全部测试点，计算部分得分
	isIOI := false
	if task.ContestID != nil {
		var contest model.Contest
		if database.DB.Select("rule_type").First(&contest, *task.ContestID).Error == nil {
			isIOI = contest.RuleType == "IOI"
		}
	}

	// 判题循环的累加器
	maxTime := 0     // 所有测试点中的最大耗时（ms）
	maxMem := 0      // 所有测试点中的最大内存（KB）
	passedCount := 0 // 通过的测试点数量
	totalCases := len(tcs)
	firstErrMsg := "" // 第一个错误的详细信息（用于反馈给用户）

	for _, tc := range tcs {
		// ================================================================
		// 在沙箱中运行用户代码
		// ================================================================
		// judge.Run 会：编译代码 → 创建 cgroup → 创建 namespace/chroot →
		// 应用 seccomp BPF → 执行并收集结果。
		result := judge.Run(task.Language, task.Code, tc.Input, task.TimeLimit, task.MemoryLimit)

		// 记录最大值
		if result.TimeUsed > maxTime {
			maxTime = result.TimeUsed
		}
		if result.MemoryUsed > maxMem {
			maxMem = result.MemoryUsed
		}

		// ================================================================
		// 处理非 Accepted 状态（运行异常）
		// ================================================================
		// 可能的状态：TLE（超时）、MLE（超内存）、Runtime Error、Compile Error
		if result.Status != judge.StatusAccepted {
			if firstErrMsg == "" {
				firstErrMsg = result.ErrorMsg
			}
			if !isIOI {
				// ACM 模式：立即返回失败结果
				database.DB.Model(&model.Submission{}).Where("id = ?", task.SubmissionID).
					Updates(map[string]interface{}{
						"status":        result.Status,
						"time_used":     result.TimeUsed,
						"memory_used":   result.MemoryUsed,
						"passed_count":  passedCount,
						"total_cases":   totalCases,
						"error_message": result.ErrorMsg,
					})
				publishSubmissionStatus(task.SubmissionID, result.Status, result.TimeUsed, result.MemoryUsed)
				metrics.IncSubmission(result.Status)
				cacheDedupResult(task.UserID, task.ProblemID, task.Language, task.Code, result.Status)
				slog.Info("judge result", "submission_id", task.SubmissionID, "status", result.Status, "case", fmt.Sprintf("%d/%d", passedCount+1, totalCases))
				if task.ContestID != nil {
					handler.UpdateContestRanking(*task.ContestID, task.UserID, task.ProblemID, result.Status, time.Now())
				}
				return
			}
			// IOI 模式：继续运行剩余测试点
			continue
		}

		// ================================================================
		// 输出比较：运行正常但结果不对
		// ================================================================
		// CompareOutput 做标准化对比：去除每行首尾空白、忽略末尾空行差异。
		if err := judge.CompareOutput(tc.Expected, result.Output); err != nil {
			if firstErrMsg == "" {
				firstErrMsg = fmt.Sprintf("Case %d:\nExpected:\n%s\n\nGot:\n%s", passedCount+1, tc.Expected, result.Output)
			}
			if !isIOI {
				// ACM 模式：立即返回 WA
				database.DB.Model(&model.Submission{}).Where("id = ?", task.SubmissionID).
					Updates(map[string]interface{}{
						"status":        judge.StatusWrongAnswer,
						"time_used":     result.TimeUsed,
						"memory_used":   result.MemoryUsed,
						"passed_count":  passedCount,
						"total_cases":   totalCases,
						"error_message": firstErrMsg,
					})
				publishSubmissionStatus(task.SubmissionID, judge.StatusWrongAnswer, result.TimeUsed, result.MemoryUsed)
				metrics.IncSubmission(judge.StatusWrongAnswer)
				cacheDedupResult(task.UserID, task.ProblemID, task.Language, task.Code, judge.StatusWrongAnswer)
				slog.Info("judge result", "submission_id", task.SubmissionID, "status", judge.StatusWrongAnswer, "case", fmt.Sprintf("%d/%d", passedCount+1, totalCases))
				if task.ContestID != nil {
					handler.UpdateContestRanking(*task.ContestID, task.UserID, task.ProblemID, judge.StatusWrongAnswer, time.Now())
				}
				return
			}
			// IOI 模式：继续运行
			continue
		}

		// 这个测试点通过了
		passedCount++
	}

	// ====================================================================
	// 最终状态判定
	// ====================================================================
	finalStatus := judge.StatusAccepted
	if passedCount == 0 {
		// 所有测试点都失败了 → 根据错误类型推断状态
		finalStatus = judge.StatusRuntimeError
		if firstErrMsg != "" {
			dbStatus := judge.StatusRuntimeError
			if strings.Contains(firstErrMsg, "Time Limit") {
				dbStatus = judge.StatusTimeLimitExceeded
			} else if strings.Contains(firstErrMsg, "Memory Limit") {
				dbStatus = judge.StatusMemoryLimitExceeded
			}
			finalStatus = dbStatus
		}
	} else if passedCount < totalCases && isIOI {
		// IOI 模式：部分通过
		finalStatus = "Partial Score"
	}

	// ====================================================================
	// 事务：更新数据库 + 维护 accepted_count
	// ====================================================================
	// accepted_count 是题目表中的计数器，表示有多少用户 AC 了此题。
	// 事务确保：只有当该用户之前没有 AC 过时，计数器才 +1。
	// 这样同一个用户重复 AC 不会导致计数虚高。
	err = database.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&model.Submission{}).Where("id = ?", task.SubmissionID).
			Updates(map[string]interface{}{
				"status":        finalStatus,
				"time_used":     maxTime,
				"memory_used":   maxMem,
				"passed_count":  passedCount,
				"total_cases":   totalCases,
				"error_message": firstErrMsg,
			}).Error; err != nil {
			return err
		}
		if finalStatus == judge.StatusAccepted {
			// 只有用户首次 AC 才增加 accepted_count
			var existingCount int64
			tx.Model(&model.Submission{}).
				Where("user_id = ? AND problem_id = ? AND status = ? AND id != ?",
					task.UserID, task.ProblemID, judge.StatusAccepted, task.SubmissionID).
				Count(&existingCount)
			if existingCount == 0 {
				tx.Model(&model.Problem{}).Where("id = ?", task.ProblemID).
					Update("accepted_count", gorm.Expr("accepted_count + 1"))
			}
		}
		return nil
	})
	if err != nil {
		slog.Error("judge transaction failed", "submission_id", task.SubmissionID, "error", err)
	}

	// ====================================================================
	// 结果发布 & 赛后处理
	// ====================================================================
	publishSubmissionStatus(task.SubmissionID, finalStatus, maxTime, maxMem)
	metrics.IncSubmission(finalStatus)
	cacheDedupResult(task.UserID, task.ProblemID, task.Language, task.Code, finalStatus)
	slog.Info("judge result", "submission_id", task.SubmissionID, "status", finalStatus, "time_ms", maxTime, "passed", fmt.Sprintf("%d/%d", passedCount, totalCases))

	if task.ContestID != nil {
		handler.UpdateContestRanking(*task.ContestID, task.UserID, task.ProblemID, finalStatus, time.Now())
	}

	// 清除题目缓存，使前端列表反映最新的 AC 计数
	cache.Del("problem:" + strconv.FormatUint(uint64(task.ProblemID), 10))
}

// ============================================================================
// 测试数据结构 & 加载
// ============================================================================

// testCaseDisk 表示一个从磁盘或存储后端加载的测试点。
// 每个测试点包含输入数据和期望输出。
type testCaseDisk struct {
	Input    string
	Expected string
}

// loadTestCasesFromDisk 从磁盘加载测试数据，带 Redis 版本缓存加速。
//
// 版本缓存原理：
//   - problems.test_case_version 字段在每次上传测试数据时 +1
//   - Redis key "tcversion:{problemID}" 缓存该版本号（1小时 TTL）
//   - 如果版本号匹配，直接从文件系统读取（跳过数据库查询）
//   - 如果不匹配，读取后更新缓存中的版本号
//
// 这样避免每次判题都查询数据库，同时确保测试数据更新后能及时生效。
func loadTestCasesFromDisk(problemID uint) ([]testCaseDisk, error) {
	var problem model.Problem
	if err := database.DB.Select("test_case_version").First(&problem, problemID).Error; err != nil {
		return nil, fmt.Errorf("problem %d not found", problemID)
	}

	cacheVersionKey := "tcversion:" + strconv.FormatUint(uint64(problemID), 10)
	var cachedVersion int
	versionHit := cache.Get(cacheVersionKey, &cachedVersion)

	// 版本匹配 → 直接读文件系统，跳过 DB 查询
	if versionHit && cachedVersion == problem.TestCaseVersion {
		return readTestCasesFromDir(problemID)
	}

	// 版本不匹配 → 重新读取并更新缓存
	tcs, err := readTestCasesFromDir(problemID)
	if err != nil {
		return nil, err
	}

	cache.Set(cacheVersionKey, problem.TestCaseVersion, 1*time.Hour)
	return tcs, nil
}

// readTestCasesFromDir 从存储后端或本地文件系统读取测试数据。
// 优先使用 S3/MinIO（如果配了 storage.Default），否则使用本地磁盘。
func readTestCasesFromDir(problemID uint) ([]testCaseDisk, error) {
	if storage.Default != nil {
		if tcs, err := readTestCasesFromStorage(problemID); err == nil && len(tcs) > 0 {
			return tcs, nil
		}
	}
	return readTestCasesFromFilesystem(problemID)
}

// readTestCasesFromStorage 从 S3/MinIO 存储读取测试数据。
// 文件命名格式：{num}.in / {num}.out（如 1.in, 1.out, 2.in, 2.out...）
func readTestCasesFromStorage(problemID uint) ([]testCaseDisk, error) {
	files, err := storage.Default.ListTestCases(problemID)
	if err != nil || len(files) == 0 {
		return nil, fmt.Errorf("no files for problem %d", problemID)
	}

	// 收集所有 .in 文件的编号
	caseNums := make(map[int]bool)
	for _, f := range files {
		name := f.Name
		if strings.HasSuffix(name, ".in") {
			if num, err := strconv.Atoi(strings.TrimSuffix(name, ".in")); err == nil {
				caseNums[num] = true
			}
		}
	}

	if len(caseNums) == 0 {
		return nil, fmt.Errorf("no .in files found for problem %d", problemID)
	}

	// 按编号排序，保证测试点顺序稳定
	sorted := make([]int, 0, len(caseNums))
	for num := range caseNums {
		sorted = append(sorted, num)
	}
	sort.Ints(sorted)

	// 逐对读取 .in / .out 文件
	result := make([]testCaseDisk, 0, len(sorted))
	for _, num := range sorted {
		inName := strconv.Itoa(num) + ".in"
		outName := strconv.Itoa(num) + ".out"

		inData, err := storage.Default.ReadTestCase(problemID, inName)
		if err != nil {
			return nil, fmt.Errorf("failed to read case %d input: %v", num, err)
		}
		outData, err := storage.Default.ReadTestCase(problemID, outName)
		if err != nil {
			return nil, fmt.Errorf("failed to read case %d output: %v", num, err)
		}

		result = append(result, testCaseDisk{
			Input:    string(inData),
			Expected: string(outData),
		})
	}

	return result, nil
}

// readTestCasesFromFilesystem 从本地磁盘读取测试数据。
// 与 readTestCasesFromStorage 逻辑相同，但使用 os 文件操作。
// 额外检查：确保 .in 文件有对应的 .out 文件才纳入（否则跳过）。
func readTestCasesFromFilesystem(problemID uint) ([]testCaseDisk, error) {
	testDir := filepath.Join(TestDataPath, strconv.FormatUint(uint64(problemID), 10))

	entries, err := os.ReadDir(testDir)
	if err != nil {
		return nil, err
	}

	// 收集编号，同时验证 .out 文件存在
	caseNums := make(map[int]bool)
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, ".in") {
			if num, err := strconv.Atoi(strings.TrimSuffix(name, ".in")); err == nil {
				caseNums[num] = true
			}
		}
	}

	if len(caseNums) == 0 {
		return nil, fmt.Errorf("no .in files found in %s", testDir)
	}

	sorted := make([]int, 0, len(caseNums))
	for num := range caseNums {
		outPath := filepath.Join(testDir, strconv.Itoa(num)+".out")
		if _, err := os.Stat(outPath); err != nil {
			return nil, fmt.Errorf("missing .out for case %d", num)
		}
		sorted = append(sorted, num)
	}
	sort.Ints(sorted)

	result := make([]testCaseDisk, 0, len(sorted))
	for _, num := range sorted {
		inPath := filepath.Join(testDir, strconv.Itoa(num)+".in")
		outPath := filepath.Join(testDir, strconv.Itoa(num)+".out")

		inData, err := os.ReadFile(inPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read case %d input: %v", num, err)
		}
		outData, err := os.ReadFile(outPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read case %d output: %v", num, err)
		}

		result = append(result, testCaseDisk{
			Input:    string(inData),
			Expected: string(outData),
		})
	}

	return result, nil
}

// loadTestCasesFromMySQL 从 MySQL test_cases 表加载测试数据（降级方案）。
//
// 当磁盘存储不可用时使用此降级路径。test_cases 表是旧模型：
// 所有数据存在 MySQL 中，不支持 S3，IO 性能较差。
// 新部署的系统应该只使用磁盘文件 + S3。
func loadTestCasesFromMySQL(problemID uint) ([]testCaseDisk, error) {
	var dbTCs []model.TestCase
	database.DB.Where("problem_id = ?", problemID).Find(&dbTCs)
	if len(dbTCs) == 0 {
		return nil, fmt.Errorf("no test cases found for problem %d", problemID)
	}
	tcs := make([]testCaseDisk, 0, len(dbTCs))
	for _, tc := range dbTCs {
		tcs = append(tcs, testCaseDisk{Input: tc.Input, Expected: tc.Expected})
	}
	return tcs, nil
}

// ============================================================================
// 去重 & SSE 推送
// ============================================================================

// dedupKey 生成提交去重的 Redis key。
// 使用 SHA256(userID:problemID:language:code) 确保唯一性。
func dedupKey(userID, problemID uint, language, code string) string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%d:%d:%s:%s", userID, problemID, language, code)))
	return "dedup:" + hex.EncodeToString(hash[:])
}

// cacheDedupResult 缓存判题结果用于去重。
// 3 秒内完全相同的提交直接返回缓存结果，避免重复判题。
func cacheDedupResult(userID, problemID uint, language, code, status string) {
	cache.Set(dedupKey(userID, problemID, language, code), status, 3*time.Second)
}

// publishSubmissionStatus 通过 Redis Pub/Sub 推送提交状态更新。
// 前端 SSE 端点（StreamEvents）订阅了 "submission:{id}" 频道，
// 收到消息后会立即推送给等待中的客户端。
func publishSubmissionStatus(id int64, status string, timeUsed, memUsed int) {
	data, _ := json.Marshal(map[string]interface{}{
		"id":          id,
		"status":      status,
		"time_used":   timeUsed,
		"memory_used": memUsed,
	})
	cache.Publish("submission:"+strconv.FormatInt(id, 10), string(data))
}
