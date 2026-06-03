package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// ============================================================================
// 12-Factor 配置管理
// ============================================================================
//
// JudgeX 遵循 12-Factor App 的配置管理原则：
// 所有配置通过环境变量注入，不硬编码在代码中。
//
// 配置加载优先级：
// 1. 环境变量（最高优先级）
// 2. 代码中的默认值（用于开发环境）
//
// 配置项命名规范：大写字母+下划线，如 DB_HOST、JWT_SECRET。
//
// 安全性：
// - Init() 中加载所有配置
// - ProductionCheck() 在生产环境启动时检查是否使用了不安全的默认值
// - 如果检测到默认值且未设置 INSECURE=1，进程拒绝启动

// Config 是 JudgeX 的全局配置结构体。
// 所有配置项通过环境变量加载，支持默认值。
//
// 环境变量与字段对照表：
//
//	服务器:       SERVER_PORT  (:8080)
//	数据库:       DB_HOST, DB_PORT, DB_USER, DB_PASSWORD, DB_NAME
//	Redis:        REDIS_ADDR  (127.0.0.1:6379)
//	消息队列:     NSQ_ADDR, QUEUE_BACKEND (nsq/redis/local)
//	JWT:          JWT_SECRET
//	AI/LLM:       LLM_API_URL, LLM_API_KEY, LLM_MODEL
//	对象存储:     S3_ENDPOINT, S3_ACCESS_KEY, S3_SECRET_KEY, S3_BUCKET, S3_USE_SSL
//	数据路径:     TEST_DATA_PATH
//	管理员:       ADMIN_PASSWORD
//	连接池:       DB_MAX_OPEN_CONNS, DB_MAX_IDLE_CONNS, DB_CONN_MAX_LIFETIME
//	日志:         LOG_LEVEL  (debug/info/warn/error)
//	沙箱:         SANDBOX_MODE  (native/gvisor/auto)
type Config struct {
	// HTTP 服务端口
	ServerPort string

	// MySQL 数据库连接
	DBHost     string
	DBPort     string
	DBUser     string
	DBPassword string
	DBName     string

	// Redis 缓存/队列
	RedisAddr string

	// NSQ 消息队列
	NSQAddr string

	// JWT 签名密钥（生产环境必须更换）
	JWTSecret string

	// LLM API 配置
	LLMAPIURL string
	LLMAPIKey string
	LLMModel  string

	// 队列后端：nsq（默认）/ redis / local
	QueueBackend string

	// S3/MinIO 对象存储
	S3Endpoint  string
	S3AccessKey string
	S3SecretKey string
	S3Bucket    string
	S3UseSSL    string

	// 测试数据存储路径
	TestDataPath string

	// 初始管理员密码
	AdminPassword string

	// 数据库连接池配置
	DBMaxOpenConns    int
	DBMaxIdleConns    int
	DBConnMaxLifetime time.Duration

	// 日志级别
	LogLevel string

	// 沙箱模式：native（完整隔离）/ gvisor（容器环境）/ auto（自动检测）
	SandboxMode string
}

// Load 从环境变量读取配置，使用合理的默认值。
// 必需的配置项（如 DB_PASSWORD、JWT_SECRET）没有默认值时
// 仍然会加载，但 ProductionCheck() 会发出警告。
func Load() *Config {
	return &Config{
		ServerPort:        getEnv("SERVER_PORT", "8080"),
		DBHost:            getEnv("DB_HOST", "127.0.0.1"),
		DBPort:            getEnv("DB_PORT", "3306"),
		DBUser:            getEnv("DB_USER", "judgex"),
		DBPassword:        getEnv("DB_PASSWORD", "judgex123"),
		DBName:            getEnv("DB_NAME", "judgex"),
		DBMaxOpenConns:    getEnvInt("DB_MAX_OPEN_CONNS", 50),
		DBMaxIdleConns:    getEnvInt("DB_MAX_IDLE_CONNS", 10),
		DBConnMaxLifetime: getEnvDuration("DB_CONN_MAX_LIFETIME", time.Hour),
		RedisAddr:         getEnv("REDIS_ADDR", "127.0.0.1:6379"),
		NSQAddr:           getEnv("NSQ_ADDR", "127.0.0.1:4150"),
		JWTSecret:         getEnv("JWT_SECRET", "judgex-secret-change-in-production"),
		LLMAPIURL:         getEnv("LLM_API_URL", "https://api.openai.com/v1"),
		LLMAPIKey:         getEnv("LLM_API_KEY", ""),
		LLMModel:          getEnv("LLM_MODEL", "gpt-4o-mini"),
		QueueBackend:      getEnv("QUEUE_BACKEND", "nsq"),
		S3Endpoint:        getEnv("S3_ENDPOINT", ""),
		S3AccessKey:       getEnv("S3_ACCESS_KEY", ""),
		S3SecretKey:       getEnv("S3_SECRET_KEY", ""),
		S3Bucket:          getEnv("S3_BUCKET", "judgex-testcases"),
		S3UseSSL:          getEnv("S3_USE_SSL", "true"),
		TestDataPath:      getEnv("TEST_DATA_PATH", "/home/sly/Downloads/oj/data/testcases"),
		AdminPassword:     getEnv("ADMIN_PASSWORD", "adminadmin"),
		LogLevel:          getEnv("LOG_LEVEL", "info"),
		SandboxMode:       getEnv("SANDBOX_MODE", "auto"),
	}
}

// DSN 生成 MySQL 连接字符串（Data Source Name），用于 GORM 初始化。
// 格式：user:password@tcp(host:port)/dbname?charset=utf8mb4&parseTime=True&loc=Local
func (c *Config) DSN() string {
	return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		c.DBUser, c.DBPassword, c.DBHost, c.DBPort, c.DBName)
}

// ProductionCheck 检查生产环境下的安全配置。
// 返回两个列表：
//   - fatal: 必须修复的问题（使用默认密钥/密码）
//   - warn: 建议修复的问题（AI 功能不可用等）
//
// 在 main.go 中，如果存在 fatal 问题且未设置 INSECURE=1，进程会拒绝启动。
// 这确保生产环境不会意外使用默认密钥。
func (c *Config) ProductionCheck() (fatal []string, warn []string) {
	if c.JWTSecret == "judgex-secret-change-in-production" {
		fatal = append(fatal, "JWT_SECRET is the default insecure value — set a random 32+ char secret")
	}
	if c.AdminPassword == "adminadmin" {
		fatal = append(fatal, "ADMIN_PASSWORD is the default insecure value — set a strong password")
	}
	if c.DBPassword == "judgex123" {
		fatal = append(fatal, "DB_PASSWORD is the default insecure value — use a unique database password")
	}
	if c.LLMAPIKey == "" {
		warn = append(warn, "LLM_API_KEY is not set — AI features will be unavailable")
	}
	if c.QueueBackend == "local" {
		warn = append(warn, "QUEUE_BACKEND is 'local' — queue will not survive restarts")
	}
	return fatal, warn
}

// ============================================================================
// 环境变量读取辅助函数
// ============================================================================

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return strings.TrimSpace(v)
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return fallback
}

func getEnvDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(strings.TrimSpace(v)); err == nil {
			return d
		}
	}
	return fallback
}
