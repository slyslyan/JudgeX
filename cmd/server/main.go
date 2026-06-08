package main

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"golang.org/x/crypto/bcrypt"
	"judgex/internal/cache"
	"judgex/internal/config"
	"judgex/internal/database"
	"judgex/internal/handler"
	"judgex/internal/metrics"
	"judgex/internal/middleware"
	"judgex/internal/model"
	"judgex/internal/queue"
	"judgex/internal/sandbox"
	"judgex/internal/storage"
	"judgex/internal/tracing"
	"judgex/internal/worker"
)

var testDataPath string

// ============================================================================
// 主入口
// ============================================================================
//
// JudgeX 的主函数承担了以下职责：
// 1. 沙箱 reexec 入口处理（SandboxInit）
// 2. 配置加载和安全性检查
// 3. 初始化所有基础设施（日志、数据库、缓存、存储、追踪、消息队列）
// 4. 数据库自动迁移和默认管理员创建
// 5. 注册所有 API 路由和中间件
// 6. 启动 HTTP 服务（支持优雅关闭）

func main() {
	// ---------- 第一步：沙箱 reexec 入口 ----------
	// 当 judge-worker 需要运行用户代码时，它会通过 /proc/self/exe 重新执行
	// 当前进程，并传递 "judgex-sandbox-init" 参数。此时 SandboxInit() 返回 true，
	// 执行沙箱初始化（chroot + seccomp + exec 用户代码）后退出，不会执行后续逻辑。
	// 正常启动时返回 false，继续执行主流程。
	if sandbox.SandboxInit() {
		return
	}

	// ---------- 第二步：配置加载 ----------
	cfg := config.Load()
	testDataPath = cfg.TestDataPath
	worker.TestDataPath = cfg.TestDataPath

	// 初始化结构化日志（JSON 格式，带请求 ID）
	middleware.InitLogger()

	// 生产环境安全检查：检测是否使用了不安全的默认值（密钥、密码等）
	// 如果检测到不安全配置且没有设置 INSECURE=1，则拒绝启动
	fatal, warns := cfg.ProductionCheck()
	for _, w := range warns {
		slog.Warn("config: " + w)
	}
	if len(fatal) > 0 && os.Getenv("INSECURE") != "1" {
		slog.Error("Insecure defaults detected — set INSECURE=1 to bypass (dev only)")
		for _, f := range fatal {
			slog.Error("  " + f)
		}
		os.Exit(1)
	}
	if len(fatal) > 0 {
		slog.Warn("INSECURE mode enabled — running with default secrets")
	}

	// ---------- 第三步：基础设施初始化 ----------
	middleware.InitAuth(cfg) // JWT 密钥
	database.Init(cfg)       // MySQL / GORM 连接池
	cache.Init()             // Redis 连接
	cache.InitBloomFilter()  // Bloom Filter 防穿透（从数据库加载所有题目 ID）
	storage.Init()           // 文件存储后端（本地磁盘 / S3）

	// 初始化 OpenTelemetry 分布式追踪
	shutdownTracing := tracing.Init()
	defer shutdownTracing()

	// ---------- 第四步：数据库迁移和种子数据 ----------
	if err := database.DB.AutoMigrate(
		&model.User{},
		&model.Problem{},
		&model.Submission{},
		&model.TestCase{}, // 已废弃：保留向后兼容旧数据
		&model.Contest{},
		&model.ContestProblem{},
		&model.Announcement{},
		&model.ProblemTag{},
		&model.ProblemFeedback{},
	); err != nil {
		log.Fatalf("auto migration failed: %v", err)
	}
	log.Println("All tables migrated successfully")

	// 确保默认管理员账号存在（开发环境使用）
	var adminCount int64
	database.DB.Model(&model.User{}).Where("username = ?", "admin").Count(&adminCount)
	if adminCount == 0 {
		hash, err := bcrypt.GenerateFromPassword([]byte(cfg.AdminPassword), bcrypt.DefaultCost)
		if err == nil {
			database.DB.Create(&model.User{
				Username:     "admin",
				Email:        "admin@judgex.local",
				PasswordHash: string(hash),
				Role:         "super_admin",
			})
			slog.Info("default admin account created", "username", "admin")
		}
	}

	// ---------- 第五步：消息队列初始化 ----------
	// 同时初始化为生产者和消费者：API 服务器提交任务到队列，
	// 同时本地消费队列执行判题（单机模式）。
	// 生产环境可以通过部署独立的 judge-worker 来拆分生产和消费。
	queue.Init(worker.JudgeTask)

	// 定时上报队列深度到 Prometheus metrics（用于 KEDA 自动伸缩）
	go func() {
		for {
			time.Sleep(5 * time.Second)
			metrics.SetQueueDepth(queue.NSQDepth())
		}
	}()

	// 定时上报磁盘空闲百分比（用于磁盘告警）
	go func() {
		for {
			time.Sleep(30 * time.Second)
			if pct, err := handler.DiskFreePercent(testDataPath); err == nil {
				metrics.SetDiskFreePercent(pct)
			}
		}
	}()

	// ---------- 第六步：路由注册 ----------
	r := gin.New()
	r.Use(gin.Recovery())                // 异常恢复，防止进程崩溃
	r.Use(middleware.RequestID())        // 请求 ID（用于日志追踪）
	r.Use(middleware.StructuredLogger()) // 结构化访问日志
	r.Use(middleware.Tracing())          // OpenTelemetry 分布式追踪
	// 全局 Prometheus 指标中间件
	r.Use(gin.HandlerFunc(func(c *gin.Context) {
		start := time.Now()
		metrics.IncAPIRequest() // 总请求数 +1
		c.Next()
		metrics.ObserveLatency(time.Since(start)) // 请求延迟
		if c.Writer.Status() >= 400 {
			metrics.IncAPIError() // 错误请求数 +1
		}
	}))

	// Prometheus 监控指标端点
	r.GET("/metrics", gin.WrapH(metrics.Handler()))

	// 健康检查端点（用于 K8s 存活/就绪探针）
	healthH := handler.NewHealthHandler(testDataPath)
	r.GET("/health", healthH.Liveness) // 存活：进程是否活着
	r.GET("/ready", healthH.Readiness) // 就绪：能否处理请求

	// 初始化各 handler
	userH := handler.NewUserHandler()
	problemH := handler.NewProblemHandler()
	submissionH := handler.NewSubmissionHandler()
	leaderboardH := handler.NewLeaderboardHandler()
	adminH := handler.NewAdminHandler()
	contestH := handler.NewContestHandler()
	profileH := handler.NewProfileHandler()
	announcementH := handler.NewAnnouncementHandler()

	// ======== API 路由分组 ========
	// 路由设计原则：
	// - 公开接口：GET 查询 + 限流
	// - 认证接口：AuthRequired 中间件
	// - 管理接口：AuthRequired + AdminRequired/SuperAdminRequired
	api := r.Group("/api")
	{
		// ---------- 排行榜（公开，限流） ----------
		leaderboard := api.Group("/leaderboard")
		{
			leaderboard.GET("", middleware.RateLimit(60, 1*time.Minute), leaderboardH.Get)
		}

		// ---------- 公告（公开） ----------
		api.GET("/announcements", middleware.RateLimit(30, 1*time.Minute), announcementH.List)
		// ---------- 比赛（部分公开，部分需要管理员） ----------
		contests := api.Group("/contests")
		{
			contests.GET("", middleware.RateLimit(60, 1*time.Minute), contestH.List)
			contests.GET("/:id", middleware.RateLimit(60, 1*time.Minute), contestH.Get)
			contests.GET("/:id/leaderboard", middleware.RateLimit(60, 1*time.Minute), contestH.GetLeaderboard)
			contests.GET("/:id/leaderboard/events", contestH.StreamLeaderboard) // SSE 实时推送
			contests.POST("", middleware.AuthRequired(), middleware.AdminRequired(), contestH.Create)
			contests.POST("/:id/submissions", middleware.AuthRequired(), middleware.RateLimitSubmission(), contestH.Submit)
			contests.PUT("/:id", middleware.AuthRequired(), middleware.AdminRequired(), contestH.Update)
			contests.POST("/:id/problems", middleware.AuthRequired(), middleware.AdminRequired(), contestH.AddProblem)
			contests.DELETE("/:id/problems/:pid", middleware.AuthRequired(), middleware.AdminRequired(), contestH.RemoveProblem)
		}

		// ---------- 用户信息 ----------
		api.GET("/users/:id", middleware.RateLimit(60, 1*time.Minute), profileH.GetByID)

		// ---------- 认证 ----------
		auth := api.Group("/auth")
		{
			auth.POST("/register", middleware.RateLimit(20, 1*time.Minute), userH.Register)
			auth.POST("/login", middleware.RateLimit(20, 1*time.Minute), userH.Login)
		}

		// ---------- 题目管理 ----------
		problems := api.Group("/problems")
		{
			problems.GET("", middleware.RateLimit(60, 1*time.Minute), problemH.List)
			problems.GET("/:id", middleware.RateLimit(60, 1*time.Minute), problemH.Get)
			problems.POST("", middleware.AuthRequired(), middleware.AdminRequired(), problemH.Create)
			problems.PUT("/:id", middleware.AuthRequired(), middleware.AdminRequired(), problemH.Update)
			problems.DELETE("/:id", middleware.AuthRequired(), middleware.AdminRequired(), problemH.Delete)
		}

		// ---------- 需要认证的接口 ----------
		protected := api.Group("")
		protected.Use(middleware.AuthRequired())
		{
			// 提交管理
			protected.POST("/submissions", middleware.RateLimitSubmission(), submissionH.Submit)
			protected.GET("/submissions", submissionH.List)
			protected.GET("/submissions/:id", submissionH.Get)
			protected.GET("/submissions/:id/events", submissionH.StreamEvents) // SSE 状态推送
			protected.POST("/submissions/:id/rejudge", middleware.AdminRequired(), submissionH.ReJudge)
			protected.POST("/submissions/rejudge-batch", middleware.AdminRequired(), submissionH.ReJudgeBatch)

			// 用户个人资料
			protected.GET("/profile", profileH.Get)
			protected.PUT("/profile", profileH.UpdateProfile)
			protected.PUT("/profile/password", profileH.ChangePassword)
			protected.GET("/profile/templates", profileH.GetTemplates)
			protected.PUT("/profile/templates", profileH.SaveTemplates)

			// 快捷功能
			protected.GET("/problems/:id/last-code", submissionH.LastCode) // 获取用户上次提交的代码
			protected.POST("/run", submissionH.Run)                        // 运行（不保存提交记录）

			// AI 助手（SSE 流式响应）
			protected.POST("/ai/chat", handler.ChatStream)                                                     // 通用 AI 对话
			protected.POST("/ai/debug", handler.DebugHandler)                                                  // AI Debug Agent
			protected.POST("/ai/generate-test-script", middleware.AdminRequired(), handler.GenerateTestScript) // AI 测试用例生成
			protected.POST("/ai/sre", middleware.AdminRequired(), handler.SREDiagnose)                         // SRE 诊断
			protected.POST("/ai/sre/agent", middleware.AdminRequired(), handler.SREAgentChat)                  // SRE Ops Agent

			// SRE 监控（管理员）
			protected.GET("/admin/sre/snapshot", middleware.AdminRequired(), handler.SRESnapshot)
			protected.GET("/admin/ai/status", middleware.AdminRequired(), handler.AIStatus)
			protected.POST("/admin/alerts/webhook", middleware.AdminRequired(), handler.AlertWebhook)

			// 废弃的测试用例接口（保留向后兼容）
			protected.POST("/problems/:id/testcases", middleware.AdminRequired(), createTestCase)
			protected.GET("/problems/:id/testcases", listTestCases)
		}

		// 废弃的单条测试用例操作
		api.PUT("/testcases/:tid", middleware.AuthRequired(), middleware.AdminRequired(), updateTestCase)
		api.DELETE("/testcases/:tid", middleware.AuthRequired(), middleware.AdminRequired(), deleteTestCase)

		// 新版测试用例管理（基于磁盘文件 / S3 存储）
		api.POST("/admin/problems/:id/testcases", middleware.AuthRequired(), middleware.AdminRequired(), uploadTestCases)
		api.GET("/admin/problems/:id/testcases/disk", middleware.AuthRequired(), middleware.AdminRequired(), listDiskTestCases)
		api.DELETE("/admin/problems/:id/testcases", middleware.AuthRequired(), middleware.AdminRequired(), deleteAllTestCases)
		api.POST("/admin/problems/:id/testcases/single", middleware.AuthRequired(), middleware.AdminRequired(), addSingleTestCase)
		api.GET("/admin/problems/:id/testcases/:caseId", middleware.AuthRequired(), middleware.AdminRequired(), getSingleTestCase)
		api.PUT("/admin/problems/:id/testcases/:caseId", middleware.AuthRequired(), middleware.AdminRequired(), updateSingleTestCase)
		api.DELETE("/admin/problems/:id/testcases/:caseId", middleware.AuthRequired(), middleware.AdminRequired(), deleteSingleTestCase)

		// 用户管理（仅 super_admin）
		admin := api.Group("/admin")
		admin.Use(middleware.AuthRequired(), middleware.SuperAdminRequired())
		{
			admin.GET("/users", adminH.ListUsers)
			admin.PUT("/users/:id/role", adminH.UpdateUserRole)
			admin.DELETE("/users/:id", adminH.DeleteUser)
			admin.GET("/announcements", announcementH.List)
			admin.POST("/announcements", announcementH.Create)
			admin.PUT("/announcements/:id", announcementH.Update)
			admin.DELETE("/announcements/:id", announcementH.Delete)
			admin.GET("/problem-feedback", adminH.ListProblemFeedback)
			admin.DELETE("/problem-feedback/:id", adminH.DeleteProblemFeedback)
		}
	}

	// ---------- 前端静态文件服务 ----------
	// 构建好的 Vue SPA 应用打包在 frontend/dist 目录
	// 所有非 API 请求返回 index.html（SPA 客户端路由）
	distPath := os.Getenv("FRONTEND_DIST")
	if distPath == "" {
		distPath = "frontend/dist"
	}
	r.NoRoute(func(c *gin.Context) {
		path := c.Request.URL.Path
		if strings.HasPrefix(path, "/api/") || path == "/metrics" || path == "/health" || path == "/ready" {
			c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
			return
		}
		filePath := filepath.Join(distPath, path)
		if _, err := os.Stat(filePath); err == nil {
			c.File(filePath)
			return
		}
		c.File(filepath.Join(distPath, "index.html"))
	})

	// ---------- 第七步：启动 HTTP 服务 ----------
	srv := &http.Server{
		Addr:              ":" + cfg.ServerPort,
		Handler:           r,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      120 * time.Second, // SSE 流需要较长的写超时
		IdleTimeout:       60 * time.Second,
	}

	// 优雅关闭：收到 SIGINT/SIGTERM 信号后，等待 30 秒让正在处理的请求完成
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		sigReceived := <-sig
		slog.Info("shutting down server", "signal", sigReceived.String())

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if err := srv.Shutdown(ctx); err != nil {
			slog.Error("forced shutdown", "error", err)
		}
	}()

	slog.Info("JudgeX server starting", "port", cfg.ServerPort)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("failed to start server: %v", err)
	}
	slog.Info("server stopped gracefully")
}

// ============================================================================
// 测试用例管理（已废弃的 MySQL 版本）
// ============================================================================
//
// 以下四个函数（createTestCase, listTestCases, updateTestCase, deleteTestCase）
// 操作 MySQL 中的 test_cases 表，是旧版测试用例管理方式。
// 保留它们是为了防止前端旧代码报 404。
//
// 新版使用基于磁盘文件的测试用例管理：
//   - uploadTestCases: ZIP 上传（1.in/1.out, 2.in/2.out, ...）
//   - listDiskTestCases: 列出磁盘上的测试用例
//   - deleteAllTestCases: 删除所有测试用例
//   - addSingleTestCase: 添加单个测试用例
//   - getSingleTestCase: 读取单个测试用例内容
//   - updateSingleTestCase: 更新单个测试用例
//   - deleteSingleTestCase: 删除单个测试用例

func createTestCase(c *gin.Context) {
	log.Println("[deprecated] POST /api/problems/:id/testcases — use ZIP upload instead")
	c.Header("Deprecation", "true")
	pid, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid problem id"})
		return
	}
	var req struct {
		Input     string `json:"input"`
		Expected  string `json:"expected"`
		IsExample bool   `json:"is_example"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	tc := model.TestCase{
		ProblemID: uint(pid),
		Input:     req.Input,
		Expected:  req.Expected,
		IsExample: req.IsExample,
	}
	database.DB.Create(&tc)
	c.JSON(201, tc)
}

func listTestCases(c *gin.Context) {
	log.Println("[deprecated] GET /api/problems/:id/testcases — use GET /api/admin/problems/:id/testcases/disk")
	c.Header("Deprecation", "true")
	pid, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid problem id"})
		return
	}
	var tcs []model.TestCase
	q := database.DB.Where("problem_id = ?", pid)
	if c.Query("type") == "example" {
		q = q.Where("is_example = ?", true)
	}
	q.Find(&tcs)
	c.JSON(200, tcs)
}

func updateTestCase(c *gin.Context) {
	log.Println("[deprecated] PUT /api/testcases/:tid — use ZIP upload instead")
	c.Header("Deprecation", "true")
	tid, err := strconv.ParseUint(c.Param("tid"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid test case id"})
		return
	}
	var tc model.TestCase
	if err := database.DB.First(&tc, tid).Error; err != nil {
		c.JSON(404, gin.H{"error": "test case not found"})
		return
	}
	var req struct {
		Input     string `json:"input"`
		Expected  string `json:"expected"`
		IsExample *bool  `json:"is_example"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if req.Input != "" {
		tc.Input = req.Input
	}
	if req.Expected != "" {
		tc.Expected = req.Expected
	}
	if req.IsExample != nil {
		tc.IsExample = *req.IsExample
	}
	database.DB.Save(&tc)
	c.JSON(200, tc)
}

func deleteTestCase(c *gin.Context) {
	log.Println("[deprecated] DELETE /api/testcases/:tid — use ZIP upload to replace test cases")
	c.Header("Deprecation", "true")
	tid, err := strconv.ParseUint(c.Param("tid"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid test case id"})
		return
	}
	database.DB.Delete(&model.TestCase{}, tid)
	c.JSON(200, gin.H{"message": "deleted"})
}

// ============================================================================
// 新版测试用例管理（磁盘文件 / S3 存储）
// ============================================================================

// uploadTestCases 接受 ZIP 文件上传，解压后保存测试用例到磁盘或 S3。
//
// ZIP 文件格式要求：
//
//	test-cases.zip
//	├── 1.in        ← 第 1 个测试用例的输入
//	├── 1.out       ← 第 1 个测试用例的期望输出
//	├── 2.in        ← 第 2 个测试用例的输入
//	├── 2.out       ← 第 2 个测试用例的期望输出
//	└── ...
//
// 验证规则：
//   - 每个 .in 文件必须有对应的 .out 文件
//   - 忽略 __MACOSX/ 目录和隐藏文件
//   - 只处理 .in 和 .out 后缀的文件
//
// 上传成功后自动递增 test_case_version（触发 judge-worker 重新加载）
func uploadTestCases(c *gin.Context) {
	pid, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid problem id"})
		return
	}

	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(400, gin.H{"error": "missing zip file"})
		return
	}

	// 保存上传的 ZIP 到临时文件
	tmpPath := filepath.Join(os.TempDir(), fmt.Sprintf("judgex_tc_%d_%s", pid, file.Filename))
	if err := c.SaveUploadedFile(file, tmpPath); err != nil {
		c.JSON(500, gin.H{"error": "failed to save upload"})
		return
	}
	defer os.Remove(tmpPath)

	// 打开 ZIP 文件
	reader, err := zip.OpenReader(tmpPath)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid zip file: " + err.Error()})
		return
	}
	defer reader.Close()

	// 解压 ZIP 到内存 map
	files := make(map[string][]byte)
	for _, f := range reader.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := filepath.ToSlash(f.Name)
		if strings.HasPrefix(name, "__MACOSX/") {
			continue // 跳过 macOS 的元数据目录
		}
		base := filepath.Base(f.Name)
		if strings.HasPrefix(base, ".") {
			continue // 跳过隐藏文件
		}
		if !strings.HasSuffix(base, ".in") && !strings.HasSuffix(base, ".out") {
			continue // 只处理 .in 和 .out 文件
		}

		rc, err := f.Open()
		if err != nil {
			continue
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			continue
		}
		files[base] = content
	}

	// 验证：每个 .in 必须有对应的 .out
	inFiles := make(map[string]bool)
	outFiles := make(map[string]bool)
	for name := range files {
		caseID := strings.TrimSuffix(strings.TrimSuffix(name, ".in"), ".out")
		if strings.HasSuffix(name, ".in") {
			inFiles[caseID] = true
		} else if strings.HasSuffix(name, ".out") {
			outFiles[caseID] = true
		}
	}
	for caseID := range inFiles {
		if !outFiles[caseID] {
			c.JSON(400, gin.H{"error": fmt.Sprintf("missing .out for case %s", caseID)})
			return
		}
	}
	if len(inFiles) == 0 || len(inFiles) != len(outFiles) {
		c.JSON(400, gin.H{"error": "invalid test cases: each .in must have a matching .out"})
		return
	}

	// 通过存储后端保存（本地磁盘或 S3/MinIO）
	if err := storage.Default.SaveTestCases(uint(pid), files); err != nil {
		c.JSON(500, gin.H{"error": "failed to save test cases: " + err.Error()})
		return
	}

	// 递增版本号，通知 judge 数据已变更
	database.DB.Model(&model.Problem{}).Where("id = ?", pid).
		UpdateColumn("test_case_version", gorm.Expr("test_case_version + 1"))

	c.JSON(200, gin.H{
		"message": "ok",
		"cases":   len(inFiles),
	})
}

type diskCaseInfo struct {
	CaseID    int   `json:"case_id"`
	InputSize int64 `json:"input_size"`
	OutSize   int64 `json:"out_size"`
}

// listDiskTestCases 列出题目在磁盘上的测试用例文件。
// 通过存储后端（LocalBackend / S3Backend）的 ListTestCases 接口读取。
// 返回排序后的测试用例编号列表和文件大小信息。
func listDiskTestCases(c *gin.Context) {
	pid, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid problem id"})
		return
	}

	var problem model.Problem
	if err := database.DB.Select("test_case_version").First(&problem, pid).Error; err != nil {
		c.JSON(404, gin.H{"error": "problem not found"})
		return
	}

	files, err := storage.Default.ListTestCases(uint(pid))
	if err != nil {
		files = nil
	}

	var cases []diskCaseInfo
	caseNums := make(map[int]bool)

	for _, f := range files {
		name := f.Name
		if strings.HasSuffix(name, ".in") {
			if num, err := strconv.Atoi(strings.TrimSuffix(name, ".in")); err == nil {
				caseNums[num] = true
			}
		}
	}

	sorted := make([]int, 0, len(caseNums))
	for num := range caseNums {
		sorted = append(sorted, num)
	}
	sort.Ints(sorted)

	// 构建文件信息：按测试用例编号分组 .in/.out
	for _, num := range sorted {
		inName := strconv.Itoa(num) + ".in"
		outName := strconv.Itoa(num) + ".out"
		var inSize, outSize int64
		for _, f := range files {
			if f.Name == inName {
				inSize = f.Size
			}
			if f.Name == outName {
				outSize = f.Size
			}
		}
		if inSize > 0 || outSize > 0 {
			cases = append(cases, diskCaseInfo{
				CaseID:    num,
				InputSize: inSize,
				OutSize:   outSize,
			})
		}
	}

	if cases == nil {
		cases = []diskCaseInfo{}
	}

	c.JSON(200, gin.H{
		"problem_id":        pid,
		"test_case_version": problem.TestCaseVersion,
		"cases":             cases,
		"count":             len(cases),
	})
}

// deleteAllTestCases 删除题目的所有测试用例。
// 同时清理存储后端（磁盘/S3）和 MySQL 中的旧数据。
func deleteAllTestCases(c *gin.Context) {
	pid, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid problem id"})
		return
	}

	if err := storage.Default.DeleteTestCases(uint(pid)); err != nil {
		c.JSON(500, gin.H{"error": "failed to delete test cases: " + err.Error()})
		return
	}

	// 清理 MySQL 中的旧测试用例数据
	database.DB.Where("problem_id = ?", pid).Delete(&model.TestCase{})

	// 递增版本号
	database.DB.Model(&model.Problem{}).Where("id = ?", pid).
		UpdateColumn("test_case_version", gorm.Expr("test_case_version + 1"))

	c.JSON(200, gin.H{"message": "deleted"})
}

type singleTestCaseReq struct {
	Input    string `json:"input" binding:"required"`
	Expected string `json:"expected" binding:"required"`
}

// addSingleTestCase 添加单个测试用例。
// 自动查找下一个可用的测试用例编号，写入磁盘。
func addSingleTestCase(c *gin.Context) {
	pid, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid problem id"})
		return
	}

	var req singleTestCaseReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	destDir := filepath.Join(testDataPath, strconv.FormatUint(pid, 10))
	if err := os.MkdirAll(destDir, 0755); err != nil {
		c.JSON(500, gin.H{"error": "failed to create directory"})
		return
	}

	// 查找下一个可用的测试用例编号
	nextNum := 1
	entries, _ := os.ReadDir(destDir)
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".in") {
			if n, err := strconv.Atoi(strings.TrimSuffix(name, ".in")); err == nil && n >= nextNum {
				nextNum = n + 1
			}
		}
	}

	inPath := filepath.Join(destDir, strconv.Itoa(nextNum)+".in")
	outPath := filepath.Join(destDir, strconv.Itoa(nextNum)+".out")

	if err := os.WriteFile(inPath, []byte(req.Input), 0644); err != nil {
		c.JSON(500, gin.H{"error": "failed to write input file"})
		return
	}
	if err := os.WriteFile(outPath, []byte(req.Expected), 0644); err != nil {
		os.Remove(inPath)
		c.JSON(500, gin.H{"error": "failed to write output file"})
		return
	}

	database.DB.Model(&model.Problem{}).Where("id = ?", pid).
		UpdateColumn("test_case_version", gorm.Expr("test_case_version + 1"))

	c.JSON(201, gin.H{"message": "added", "case_id": nextNum})
}

// updateSingleTestCase 更新单个测试用例的输入/输出文件。
func updateSingleTestCase(c *gin.Context) {
	pid, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid problem id"})
		return
	}
	caseNum, err := strconv.Atoi(c.Param("caseId"))
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid case id"})
		return
	}

	var req singleTestCaseReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	destDir := filepath.Join(testDataPath, strconv.FormatUint(pid, 10))
	inPath := filepath.Join(destDir, strconv.Itoa(caseNum)+".in")
	outPath := filepath.Join(destDir, strconv.Itoa(caseNum)+".out")

	if _, err := os.Stat(inPath); os.IsNotExist(err) {
		c.JSON(404, gin.H{"error": "test case not found"})
		return
	}

	if err := os.WriteFile(inPath, []byte(req.Input), 0644); err != nil {
		c.JSON(500, gin.H{"error": "failed to write input file"})
		return
	}
	if err := os.WriteFile(outPath, []byte(req.Expected), 0644); err != nil {
		c.JSON(500, gin.H{"error": "failed to write output file"})
		return
	}

	database.DB.Model(&model.Problem{}).Where("id = ?", pid).
		UpdateColumn("test_case_version", gorm.Expr("test_case_version + 1"))

	c.JSON(200, gin.H{"message": "updated", "case_id": caseNum})
}

// getSingleTestCase 读取单个测试用例的 .in/.out 文件内容。
func getSingleTestCase(c *gin.Context) {
	pid, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid problem id"})
		return
	}
	caseNum, err := strconv.Atoi(c.Param("caseId"))
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid case id"})
		return
	}

	destDir := filepath.Join(testDataPath, strconv.FormatUint(pid, 10))
	inPath := filepath.Join(destDir, strconv.Itoa(caseNum)+".in")
	outPath := filepath.Join(destDir, strconv.Itoa(caseNum)+".out")

	inData, err := os.ReadFile(inPath)
	if err != nil {
		c.JSON(404, gin.H{"error": "test case not found"})
		return
	}
	outData, _ := os.ReadFile(outPath)

	c.JSON(200, gin.H{
		"case_id":  caseNum,
		"input":    string(inData),
		"expected": string(outData),
	})
}

// deleteSingleTestCase 删除单个测试用例的 .in/.out 文件对。
func deleteSingleTestCase(c *gin.Context) {
	pid, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid problem id"})
		return
	}
	caseNum, err := strconv.Atoi(c.Param("caseId"))
	if err != nil {
		c.JSON(400, gin.H{"error": "invalid case id"})
		return
	}

	destDir := filepath.Join(testDataPath, strconv.FormatUint(pid, 10))
	inPath := filepath.Join(destDir, strconv.Itoa(caseNum)+".in")
	outPath := filepath.Join(destDir, strconv.Itoa(caseNum)+".out")

	os.Remove(inPath)
	os.Remove(outPath)

	database.DB.Model(&model.Problem{}).Where("id = ?", pid).
		UpdateColumn("test_case_version", gorm.Expr("test_case_version + 1"))

	c.JSON(200, gin.H{"message": "deleted", "case_id": caseNum})
}
