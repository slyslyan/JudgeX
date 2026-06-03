package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"

	"judgex/internal/database"
	"judgex/internal/middleware"
	"judgex/internal/model"
)

// ============================================================================
// 用户档案控制器 — Profile
// ============================================================================
//
// 提供用户个人资料的查看和编辑功能。
//
// 接口：
//   Get             — 获取当前用户的完整资料（GET /api/profile）
//   GetByID         — 查看其他用户的公开资料（GET /api/users/:id）
//   UpdateProfile   — 更新个人资料（PUT /api/profile）
//   ChangePassword  — 修改密码（PUT /api/profile/password）
//   GetTemplates    — 获取用户的代码模板（GET /api/profile/templates）
//   SaveTemplates   — 保存代码模板（PUT /api/profile/templates）
//
// 资料包含：
//   - 用户基本信息（用户名、邮箱、角色、个人简介）
//   - 统计数据（总提交数、AC 数、解题数）
//   - 最近的 10 条提交记录
//
// 代码模板功能：
//   用户可以为不同编程语言保存代码模板（如 Go 的 main 函数模板），
//   在代码编辑器中自动填充。模板以 JSON 格式存储在 users 表中。

type ProfileHandler struct{}

func NewProfileHandler() *ProfileHandler {
	return &ProfileHandler{}
}

// ProfileResponse 是返回给客户端的用户资料结构。
type ProfileResponse struct {
	model.User
	TotalSubmissions    int64              `json:"total_submissions"`    // 总提交数
	AcceptedSubmissions int64              `json:"accepted_submissions"` // AC 提交数
	SolvedProblems      int64              `json:"solved_problems"`      // 解题数（去重）
	RecentSubmissions   []model.Submission `json:"recent_submissions"`   // 最近 10 条提交
}

// Get 处理 GET /api/profile。
// 返回当前用户的个人资料（含统计信息）。
func (h *ProfileHandler) Get(c *gin.Context) {
	userID := c.MustGet("user_id").(uint)

	var user model.User
	if err := database.DB.First(&user, userID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	resp := buildProfile(user)
	c.JSON(http.StatusOK, resp)
}

// GetByID 处理 GET /api/users/:id。
// 查看其他用户的公开资料。
func (h *ProfileHandler) GetByID(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}

	var user model.User
	if err := database.DB.First(&user, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	resp := buildProfile(user)
	c.JSON(http.StatusOK, resp)
}

// buildProfile 构建用户资料响应（包含统计信息）。
// 被 Get 和 GetByID 复用。
func buildProfile(user model.User) ProfileResponse {
	var totalSubs, acceptedSubs int64
	database.DB.Model(&model.Submission{}).Where("user_id = ?", user.ID).Count(&totalSubs)
	database.DB.Model(&model.Submission{}).Where("user_id = ? AND status = ?", user.ID, "Accepted").Count(&acceptedSubs)

	// 解题数：DISTINCT problem_id，确保每道题只计数一次
	var solvedProblems int64
	database.DB.Model(&model.Submission{}).
		Where("user_id = ? AND status = ?", user.ID, "Accepted").
		Distinct("problem_id").Count(&solvedProblems)

	// 最近 10 条提交
	var recent []model.Submission
	database.DB.Where("user_id = ?", user.ID).Order("id DESC").Limit(10).Find(&recent)

	return ProfileResponse{
		User:                user,
		TotalSubmissions:    totalSubs,
		AcceptedSubmissions: acceptedSubs,
		SolvedProblems:      solvedProblems,
		RecentSubmissions:   recent,
	}
}

// UpdateProfile 处理 PUT /api/profile。
// 更新个人信息（邮箱、个人简介）。
func (h *ProfileHandler) UpdateProfile(c *gin.Context) {
	userID := c.MustGet("user_id").(uint)

	var req struct {
		Email string `json:"email"`
		Bio   string `json:"bio"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	updates := map[string]interface{}{}
	if req.Email != "" {
		updates["email"] = req.Email
	}
	// Bio 可以通过发送空字符串清空
	updates["bio"] = req.Bio

	database.DB.Model(&model.User{}).Where("id = ?", userID).Updates(updates)
	c.JSON(http.StatusOK, gin.H{"message": "profile updated"})
}

// GetTemplates 处理 GET /api/profile/templates。
// 返回用户的代码模板（每种语言一个模板片段）。
func (h *ProfileHandler) GetTemplates(c *gin.Context) {
	userID := c.MustGet("user_id").(uint)

	var user model.User
	if err := database.DB.First(&user, userID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"templates": user.CodeTemplates})
}

// SaveTemplates 处理 PUT /api/profile/templates。
// 保存用户的代码模板。
func (h *ProfileHandler) SaveTemplates(c *gin.Context) {
	userID := c.MustGet("user_id").(uint)

	var req struct {
		Templates json.RawMessage `json:"templates"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	database.DB.Model(&model.User{}).Where("id = ?", userID).Update("code_templates", req.Templates)
	c.JSON(http.StatusOK, gin.H{"message": "templates saved"})
}

// ChangePassword 处理 PUT /api/profile/password。
// 修改密码：验证旧密码 → bcrypt 新密码 → 更新 → 生成新令牌（使旧令牌失效）。
//
// 密码修改后，客户端需要使用新令牌访问 API。
// 这使得旧令牌立即失效，防止密码泄露后攻击者继续使用旧令牌。
func (h *ProfileHandler) ChangePassword(c *gin.Context) {
	userID := c.MustGet("user_id").(uint)

	var req struct {
		OldPassword string `json:"old_password" binding:"required"`
		NewPassword string `json:"new_password" binding:"required,min=6,max=128"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	var user model.User
	if err := database.DB.First(&user, userID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	// 验证旧密码
	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.OldPassword)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "current password is incorrect"})
		return
	}

	// 生成新密码哈希
	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to hash password"})
		return
	}

	database.DB.Model(&user).Update("password_hash", string(hash))

	// 生成新令牌（使客户端之前持有的旧令牌作废）
	token, err := middleware.GenerateToken(user.ID, user.Username, user.Role)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to generate token"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "password changed",
		"token":   token,
	})
}
