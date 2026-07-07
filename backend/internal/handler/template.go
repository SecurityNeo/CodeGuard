package handler

import (
	"fmt"

	"github.com/ai-optimizer/backend/internal/model"
	"github.com/ai-optimizer/backend/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"strconv"
)

type TemplateHandler struct{}

func NewTemplateHandler() *TemplateHandler {
	return &TemplateHandler{}
}

func (h *TemplateHandler) List(c *gin.Context) {
	templates, err := service.NewTemplateService().List()
	if err != nil {
		zap.L().Error("list templates failed", zap.Error(err))
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"data": templates})
}

func (h *TemplateHandler) Create(c *gin.Context) {
	var t model.ProjectTemplate
	if err := c.ShouldBindJSON(&t); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	err := service.NewTemplateService().Create(&t)
	if err != nil {
		zap.L().Error("create template failed", zap.Error(err))
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	userID, _ := c.Get("user_id")
	model.RecordOpLog("模板创建", t.Name, t.ID, userID.(uint), "success", "", c.ClientIP())
	c.JSON(200, gin.H{"message": "created", "data": t})
}

func (h *TemplateHandler) Get(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	t, err := service.NewTemplateService().Get(uint(id))
	if err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	c.JSON(200, gin.H{"data": t})
}

func (h *TemplateHandler) Update(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var req map[string]interface{}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	t, _ := service.NewTemplateService().Get(uint(id))
	err := service.NewTemplateService().Update(uint(id), req)
	if err != nil {
		zap.L().Error("update template failed", zap.Error(err))
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	userID, _ := c.Get("user_id")
	model.RecordOpLog("模板更新", t.Name, uint(id), userID.(uint), "success", "", c.ClientIP())
	c.JSON(200, gin.H{"message": "updated"})
}

func (h *TemplateHandler) Delete(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	t, _ := service.NewTemplateService().Get(uint(id))
	err := service.NewTemplateService().Delete(uint(id))
	if err != nil {
		zap.L().Error("delete template failed", zap.Error(err))
		if err == service.ErrTemplateInUse {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	userID, _ := c.Get("user_id")
	model.RecordOpLog("模板删除", t.Name, uint(id), userID.(uint), "success", "", c.ClientIP())
	c.JSON(200, gin.H{"message": "deleted"})
}

func (h *TemplateHandler) Clone(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var req struct {
		Name string `json:"name"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if req.Name == "" {
		c.JSON(400, gin.H{"error": "name is required"})
		return
	}
	original, _ := service.NewTemplateService().Get(uint(id))
	t, err := service.NewTemplateService().Clone(uint(id), req.Name)
	if err != nil {
		zap.L().Error("clone template failed", zap.Error(err))
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	userID, _ := c.Get("user_id")
	model.RecordOpLog("模板克隆", fmt.Sprintf("%s->%s", original.Name, t.Name), t.ID, userID.(uint), "success", "", c.ClientIP())
	c.JSON(200, gin.H{"message": "cloned", "data": t})
}
