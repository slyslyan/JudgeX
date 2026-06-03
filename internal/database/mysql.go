package database

import (
	"log"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"judgex/internal/config"
)

// ============================================================================
// 数据库层 — GORM MySQL 连接管理
// ============================================================================
//
// database 包是整个系统的数据访问层，基于 GORM 实现。
// GORM 是 Go 最流行的 ORM 框架，支持自动迁移、钩子、预加载等特性。
//
// 核心职责：
//   1. 建立 MySQL 连接池（从 config.Config 读取 DSN 和连接池参数）
//   2. 提供全局 DB 实例供其他包访问
//   3. 提供连接池状态监控接口（PoolStats）
//
// 连接池配置（通过环境变量控制）：
//   DB_MAX_OPEN_CONNS   — 最大打开连接数（默认 100）
//   DB_MAX_IDLE_CONNS   — 最大空闲连接数（默认 20）
//   DB_CONN_MAX_LIFETIME — 连接最大存活时间（默认 5 分钟）
//
// DSN 格式：
//   username:password@tcp(host:port)/dbname?charset=utf8mb4&parseTime=True&loc=Local

// DB 是全局的 GORM 数据库实例。
// 所有数据库操作都通过此对象进行（模型操作、查询、事务等）。
// 在 main.go 的 Init 阶段调用 database.Init() 初始化。
var DB *gorm.DB

// Init 初始化 MySQL 数据库连接池。
// 使用 config.Config 中的 DSN 连接数据库，并配置连接池参数。
// 如果连接失败，直接 log.Fatalf 终止程序启动（数据库不可用无法运行）。
func Init(cfg *config.Config) {
	var err error
	DB, err = gorm.Open(mysql.Open(cfg.DSN()), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Info), // 开发阶段打印 SQL 日志
	})
	if err != nil {
		log.Fatalf("failed to connect database: %v", err)
	}

	// 获取底层 sql.DB 对象以配置连接池
	sqlDB, err := DB.DB()
	if err != nil {
		log.Fatalf("failed to get underlying sql.DB: %v", err)
	}

	// 连接池配置
	sqlDB.SetMaxOpenConns(cfg.DBMaxOpenConns)       // 最大连接数（防止 MySQL 连接耗尽）
	sqlDB.SetMaxIdleConns(cfg.DBMaxIdleConns)       // 最大空闲连接数（减少建连开销）
	sqlDB.SetConnMaxLifetime(cfg.DBConnMaxLifetime) // 连接最大存活时间（避免使用过期连接）

	if err := sqlDB.Ping(); err != nil {
		log.Fatalf("failed to ping database: %v", err)
	}

	log.Printf("MySQL connection pool initialized (max_open=%d max_idle=%d max_lifetime=%s)",
		cfg.DBMaxOpenConns, cfg.DBMaxIdleConns, cfg.DBConnMaxLifetime)
}

// PoolStats 返回数据库连接池的当前状态。
// 用于 SRE 监控页面展示数据库连接情况。
//
// 返回值：
//
//	maxOpen — 最大打开连接数
//	open    — 当前打开的连接数（包括空闲和正在使用的）
//	inUse   — 正在使用的连接数
//	idle    — 空闲连接数
func PoolStats() (maxOpen, open, inUse, idle int) {
	if DB == nil {
		return 0, 0, 0, 0
	}
	sqlDB, err := DB.DB()
	if err != nil {
		return 0, 0, 0, 0
	}
	stats := sqlDB.Stats()
	return stats.MaxOpenConnections, stats.OpenConnections, stats.InUse, stats.Idle
}
