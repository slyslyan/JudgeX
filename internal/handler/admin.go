package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"judgex/internal/database"
	"judgex/internal/model"
)

// ============================================================================
// 管理员控制器 — 用户管理
// ============================================================================
//
// 由 super_admin 角色使用，提供用户管理功能。
// 所有路由都在 super_admin 权限中间件保护下。
//
// 功能：
//   ListUsers     — 列出所有用户（GET /api/admin/users）
//   UpdateUserRole — 修改用户角色（PUT /api/admin/users/:id/role）
//   DeleteUser    — 删除用户（DELETE /api/admin/users/:id）
//
// 安全约束：
//   - 不能修改或删除 super_admin 角色用户（禁止自毁）
//   - 只能将用户设置为 "user" 或 "admin"（不能设置为 super_admin）
//   - 删除用户不会删除其提交记录（外键约束：提交记录保留但 user_id 指向已删除用户）

type AdminHandler struct{}

func NewAdminHandler() *AdminHandler {
	return &AdminHandler{}
}

// userInfo 是返回给前端的用户信息（不包含密码等敏感字段）。
type userInfo struct {
	ID       uint   `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
	Role     string `json:"role"`
}

// ListUsers 处理 GET /api/admin/users。
// 返回所有用户的列表（不包含密码哈希等敏感信息）。
func (h *AdminHandler) ListUsers(c *gin.Context) {
	var users []model.User
	database.DB.Order("id ASC").Limit(200).Find(&users)
	result := make([]userInfo, 0, len(users))
	for _, u := range users {
		result = append(result, userInfo{
			ID:       u.ID,
			Username: u.Username,
			Email:    u.Email,
			Role:     u.Role,
		})
	}
	c.JSON(http.StatusOK, gin.H{"users": result})
}

type updateRoleReq struct {
	Role string `json:"role" binding:"required"` // 新角色："user" 或 "admin"
}

// UpdateUserRole 处理 PUT /api/admin/users/:id/role。
// 修改指定用户的角色。
//
// 约束：
//   - 只能设置为 "user" 或 "admin"
//   - 不能修改 super_admin 的角色
func (h *AdminHandler) UpdateUserRole(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}

	var req updateRoleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.Role != "user" && req.Role != "admin" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "role must be 'user' or 'admin'"})
		return
	}

	var user model.User
	if err := database.DB.First(&user, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "user not found"})
		return
	}

	if user.Role == "super_admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "cannot change super admin role"})
		return
	}

	database.DB.Model(&user).Update("role", req.Role)
	c.JSON(http.StatusOK, gin.H{"message": "role updated"})
}

// DeleteUser 处理 DELETE /api/admin/users/:id。
// 删除指定用户。
//
// 约束：
//   - 不能删除 super_admin
func (h *AdminHandler) DeleteUser(c *gin.Context) {
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

	if user.Role == "super_admin" {
		c.JSON(http.StatusForbidden, gin.H{"error": "cannot delete super admin"})
		return
	}

	database.DB.Delete(&user)
	c.JSON(http.StatusOK, gin.H{"message": "user deleted"})
}

// ListProblemFeedback 处理 GET /api/admin/problem-feedback。
func (h *AdminHandler) ListProblemFeedback(c *gin.Context) {
	status := c.Query("status")
	problemID := c.Query("problem_id")
	priority := c.Query("priority")

	// 默认按优先级排序：P1（紧急）在前，同优先级按时间倒序
	query := database.DB.Model(&model.ProblemFeedback{}).
		Order("CASE priority WHEN 'P1' THEN 0 ELSE 1 END, id DESC")
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if problemID != "" {
		query = query.Where("problem_id = ?", problemID)
	}
	if priority != "" {
		query = query.Where("priority = ?", priority)
	}

	var feedbacks []model.ProblemFeedback
	query.Find(&feedbacks)
	if feedbacks == nil {
		feedbacks = []model.ProblemFeedback{}
	}
	c.JSON(http.StatusOK, gin.H{"feedbacks": feedbacks})
}

// DeleteProblemFeedback 处理 DELETE /api/admin/problem-feedback/:id。
func (h *AdminHandler) DeleteProblemFeedback(c *gin.Context) {
	id, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid feedback id"})
		return
	}

	var feedback model.ProblemFeedback
	if err := database.DB.First(&feedback, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "feedback not found"})
		return
	}

	database.DB.Delete(&feedback)
	c.JSON(http.StatusOK, gin.H{"message": "feedback deleted"})
}
