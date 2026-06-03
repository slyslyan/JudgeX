package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"judgex/internal/database"
	"judgex/internal/model"
)

type AnnouncementHandler struct{}

func NewAnnouncementHandler() *AnnouncementHandler {
	return &AnnouncementHandler{}
}

// List 获取所有公告（按创建时间倒序）。
func (h *AnnouncementHandler) List(c *gin.Context) {
	var list []model.Announcement
	database.DB.Order("created_at desc").Find(&list)
	c.JSON(http.StatusOK, gin.H{"announcements": list})
}

// Create 创建公告（管理员）。
func (h *AnnouncementHandler) Create(c *gin.Context) {
	var req struct {
		Title   string `json:"title" binding:"required"`
		Content string `json:"content"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "title is required"})
		return
	}
	a := model.Announcement{Title: req.Title, Content: req.Content}
	database.DB.Create(&a)
	c.JSON(http.StatusCreated, a)
}

// Update 更新公告（管理员）。
func (h *AnnouncementHandler) Update(c *gin.Context) {
	id := c.Param("id")
	var a model.Announcement
	if err := database.DB.First(&a, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	var req struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}
	c.ShouldBindJSON(&req)
	if req.Title != "" {
		a.Title = req.Title
	}
	a.Content = req.Content
	database.DB.Save(&a)
	c.JSON(http.StatusOK, a)
}

// Delete 删除公告（管理员）。
func (h *AnnouncementHandler) Delete(c *gin.Context) {
	id := c.Param("id")
	if err := database.DB.Delete(&model.Announcement{}, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "deleted"})
}
