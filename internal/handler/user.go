package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"judgex/internal/database"
	"judgex/internal/middleware"
	"judgex/internal/model"
)

// ============================================================================
// 用户认证控制器 — Register / Login
// ============================================================================
//
// 实现用户注册和登录接口。认证方式为 JWT（JSON Web Token）。
//
// 注册流程：
//   1. 验证必填字段（用户名 ≥3 字符、邮箱、密码 ≥6 字符、确认密码）
//   2. bcrypt 哈希密码（成本因子 = 10）
//   3. 创建用户记录（默认角色 "user"）
//   4. 生成 JWT 令牌并返回
//
// 登录流程：
//   1. 查询用户名是否存在
//   2. bcrypt 比较密码哈希
//   3. 生成 JWT 令牌并返回
//
// JWT 令牌包含：
//   - user_id（用户 ID）
//   - username（用户名）
//   - role（角色）
//   有效期 24 小时

type UserHandler struct{}

func NewUserHandler() *UserHandler {
	return &UserHandler{}
}

// registerReq 注册请求体。
// 使用 binding tag 做请求端验证：
//
//	required — 必填
//	min/max — 长度限制
//	email — 邮箱格式
//	eqfield=Password — 必须与 Password 字段相同
type registerReq struct {
	Username        string `json:"username" binding:"required,min=3,max=64"`
	Email           string `json:"email" binding:"required,email,max=128"`
	Password        string `json:"password" binding:"required,min=6,max=128"`
	ConfirmPassword string `json:"confirm_password" binding:"required,eqfield=Password"`
}

// loginReq 登录请求体。
type loginReq struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

// Register 处理 POST /api/auth/register。
// 创建新用户并返回 JWT 令牌。
//
// 安全措施：
//   - 密码 bcrypt 哈希存储（不可逆）
//   - 用户名唯一约束（冲突返回 409）
//   - 密码长度 ≥6
func (h *UserHandler) Register(c *gin.Context) {
	var req registerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// bcrypt 哈希密码（默认成本 10，约 100ms）
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to hash password"})
		return
	}

	user := model.User{
		Username:     req.Username,
		Email:        req.Email,
		PasswordHash: string(hash),
		Role:         "user", // 新用户默认为普通用户
	}
	if err := database.DB.Create(&user).Error; err != nil {
		// 用户名唯一约束冲突
		c.JSON(http.StatusConflict, gin.H{"error": "username already exists"})
		return
	}

	token, err := middleware.GenerateToken(user.ID, user.Username, user.Role)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate token"})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"token":    token,
		"user_id":  user.ID,
		"username": user.Username,
		"role":     user.Role,
	})
}

// Login 处理 POST /api/auth/login。
// 验证用户名密码，返回 JWT 令牌。
//
// 注意：错误消息统一为 "invalid username or password"，
// 不透露具体是用户名错误还是密码错误（防止账户枚举攻击）。
func (h *UserHandler) Login(c *gin.Context) {
	var req loginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var user model.User
	if err := database.DB.Where("username = ?", req.Username).First(&user).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid username or password"})
		return
	}

	// 比较 bcrypt 哈希（恒定时间比较，防止时序攻击）
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid username or password"})
		return
	}

	token, err := middleware.GenerateToken(user.ID, user.Username, user.Role)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token":    token,
		"user_id":  user.ID,
		"username": user.Username,
		"role":     user.Role,
	})
}
