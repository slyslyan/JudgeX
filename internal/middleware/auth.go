package middleware

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"judgex/internal/cache"
	"judgex/internal/config"
)

// ============================================================================
// JWT 认证中间件
// ============================================================================
//
// JudgeX 使用 JWT（JSON Web Token）进行用户认证。
// JWT 是一种无状态的认证方案——服务器不需要存储会话信息，
// 所有用户身份信息都编码在 token 本身中，通过数字签名防篡改。
//
// Token 结构：
//
//	Header:  { "alg": "HS256", "typ": "JWT" }
//	Payload: { "user_id": 1, "username": "admin", "role": "super_admin", ... }
//	Signature: HMAC-SHA256(base64(header) + "." + base64(payload), secret)
//
// 认证流程：
//
//	客户端                    服务端
//	┌─────┐                 ┌─────┐
//	│登录 │ ──POST/login──→ │验证密码→生成JWT│
//	│     │ ←──JWT token─── │                │
//	│     │                 │                │
//	│请求 │ ──GET/problems──│                │
//	│     │ Authorization:  │ →验证JWT签名   │
//	│     │ Bearer <token>  │ →提取claims    │
//	│     │ ←──200 OK────── │ →放行         │
//	└─────┘                 └─────┘

// JWTSecret 是 JWT 签名密钥，在 InitAuth() 中初始化。
// 生产环境必须通过配置文件或环境变量设置，不能使用默认值。
var JWTSecret []byte

// InitAuth 从配置中读取 JWT 密钥并初始化全局变量。
// 必须在任何认证操作之前调用（main.go 中路由注册前调用）。
func InitAuth(cfg *config.Config) {
	JWTSecret = []byte(cfg.JWTSecret)
}

// Claims 是 JWT 的负载（Payload）结构体。
// 包含用户的身份信息和角色，用于权限控制。
//
// 嵌入 jwt.RegisteredClaims 提供了标准字段：
//   - ExpiresAt: 过期时间（24 小时后）
//   - IssuedAt: 签发时间
//
// 自定义字段：
//   - UserID: 用户 ID（数据库主键）
//   - Username: 用户名
//   - Role: 角色（"user" / "admin" / "super_admin"）
type Claims struct {
	UserID   uint   `json:"user_id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	jwt.RegisteredClaims
}

// GenerateToken 生成 JWT token。
// 接收用户 ID、用户名、角色，签发有效期 24 小时的 token。
// 返回 "Bearer <token>" 格式的完整认证头值。
func GenerateToken(userID uint, username, role string) (string, error) {
	claims := Claims{
		UserID:   userID,
		Username: username,
		Role:     role,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(24 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(JWTSecret)
}

// ============================================================================
// 认证中间件：AuthRequired / AdminRequired / SuperAdminRequired
// ============================================================================
//
// Gin 中间件的执行顺序：
// 在路由注册时添加的中间件按顺序执行，每个中间件可以选择：
//   - c.Next(): 继续执行后续中间件和处理器
//   - c.Abort(): 中断请求，直接返回响应

// AuthRequired 验证请求是否携带有效的 JWT token。
// 从 Authorization 头中提取 Bearer token，验证签名并解析 claims，
// 然后将用户信息写入 Gin 上下文供后续处理器使用。
//
// 工作流程：
// 1. 检查 Authorization 头是否存在且格式为 "Bearer <token>"
// 2. 用 JWTSecret 验证 token 签名
// 3. 检查 token 是否过期
// 4. 将 user_id、username、role 写入上下文
func AuthRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		if header == "" || !strings.HasPrefix(header, "Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing or malformed token"})
			return
		}
		tokenStr := strings.TrimPrefix(header, "Bearer ")

		claims := &Claims{}
		token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (interface{}, error) {
			return JWTSecret, nil
		})
		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}

		// 将用户信息写入上下文，后续处理器通过 c.GetUint("user_id") 等获取
		c.Set("user_id", claims.UserID)
		c.Set("username", claims.Username)
		c.Set("role", claims.Role)
		c.Next()
	}
}

// AdminRequired 要求用户角色为 admin 或 super_admin。
// 必须跟在 AuthRequired 之后使用，因为需要从上下文中读取 role。
func AdminRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		role := c.GetString("role")
		if role != "admin" && role != "super_admin" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "admin access required"})
			return
		}
		c.Next()
	}
}

// SuperAdminRequired 要求用户角色为 super_admin。
// 用于用户管理等敏感操作（创建 admin、删除用户等）。
func SuperAdminRequired() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.GetString("role") != "super_admin" {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "super admin access required"})
			return
		}
		c.Next()
	}
}

// ============================================================================
// 限流中间件
// ============================================================================

const (
	rateLimitMax    = 10              // 提交限流：每分钟最多 10 次
	rateLimitWindow = 1 * time.Minute // 限流窗口：1 分钟
)

// RateLimitSubmission 限制单个用户的提交频率，防止刷题/攻击。
// 使用 Redis 计数，key 格式为 "ratelimit:submission:<user_id>"
// 每次提交递增计数器，超过限制则返回 429 Too Many Requests。
func RateLimitSubmission() gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := c.GetUint("user_id")
		key := fmt.Sprintf("ratelimit:submission:%d", userID)
		count := cache.IncrWithTTL(key, rateLimitWindow)
		if count > rateLimitMax {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": fmt.Sprintf("rate limit exceeded: %d submissions per minute", rateLimitMax),
			})
			return
		}
		c.Next()
	}
}

// RateLimit 按 IP 地址限流，适用于公开 API（登录、注册等）。
// rateLimitMax 和 rateLimitWindow 由调用者指定。
//
// 参数：
//   - limit: 窗口内允许的最大请求数
//   - window: 时间窗口（如 1*time.Minute）
//
// 使用场景：
//   - 登录接口：20次/分钟（防暴力破解）
//   - 公共查询接口：60次/分钟
func RateLimit(limit int, window time.Duration) gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		key := fmt.Sprintf("ratelimit:ip:%s", ip)
		count := cache.IncrWithTTL(key, window)
		if count > int64(limit) {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": fmt.Sprintf("rate limit exceeded: %d requests per %s", limit, window),
			})
			return
		}
		c.Next()
	}
}
