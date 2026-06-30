package handler

import (
	"strconv"

	"github.com/ai-optimizer/backend/internal/model"
	"github.com/ai-optimizer/backend/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type MemberMappingHandler struct{}

func NewMemberMappingHandler() *MemberMappingHandler {
	return &MemberMappingHandler{}
}

func (h *MemberMappingHandler) List(c *gin.Context) {
	gitUsername := c.Query("git_username")
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	mappings, total, err := service.NewMemberMappingService().List(gitUsername, page, pageSize)
	if err != nil {
		zap.L().Error("list member mappings failed", zap.Error(err))
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, gin.H{"data": mappings, "total": total})
}

func (h *MemberMappingHandler) Get(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	mapping, err := service.NewMemberMappingService().Get(uint(id))
	if err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	c.JSON(200, gin.H{"data": mapping})
}

func (h *MemberMappingHandler) Create(c *gin.Context) {
	var data map[string]interface{}
	if err := c.ShouldBindJSON(&data); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	mapping, err := service.NewMemberMappingService().Create(data)
	if err != nil {
		zap.L().Error("create member mapping failed", zap.Error(err))
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, gin.H{"data": mapping})
}

func (h *MemberMappingHandler) Update(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var data map[string]interface{}
	if err := c.ShouldBindJSON(&data); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	if err := service.NewMemberMappingService().Update(uint(id), data); err != nil {
		zap.L().Error("update member mapping failed", zap.Error(err))
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, gin.H{"message": "updated"})
}

func (h *MemberMappingHandler) Delete(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if err := service.NewMemberMappingService().Delete(uint(id)); err != nil {
		zap.L().Error("delete member mapping failed", zap.Error(err))
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"message": "deleted"})
}

func (h *MemberMappingHandler) GitUsers(c *gin.Context) {
	usernames, err := service.NewMemberMappingService().GetGitUsers()
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"data": usernames})
}

// CheckMapping 检查某个 Git 用户名是否有映射（用于通知服务查询）
type CheckMappingResponse struct {
	Mapped      bool   `json:"mapped"`
	IMUserID    string `json:"im_user_id,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}

func (h *MemberMappingHandler) CheckMapping(c *gin.Context) {
	gitUsername := c.Query("git_username")
	platform := c.DefaultQuery("platform", string(model.IMPlatformWeCom))

	if gitUsername == "" {
		c.JSON(400, gin.H{"error": "git_username is required"})
		return
	}

	mapping, err := service.NewMemberMappingService().GetByGitUsername(gitUsername, model.IMPlatform(platform))
	if err != nil {
		c.JSON(200, gin.H{"data": CheckMappingResponse{Mapped: false}})
		return
	}

	c.JSON(200, gin.H{"data": CheckMappingResponse{
		Mapped:      true,
		IMUserID:    mapping.IMUserID,
		DisplayName: mapping.DisplayName,
	}})
}
