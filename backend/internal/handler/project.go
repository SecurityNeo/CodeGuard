package handler

import (
	"strconv"

	"github.com/ai-optimizer/backend/internal/model"
	"github.com/ai-optimizer/backend/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type ProjectHandler struct{}

func NewProjectHandler() *ProjectHandler {
	return &ProjectHandler{}
}

func (h *ProjectHandler) List(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	keyword := c.Query("keyword")
	status := c.Query("status")
	source := c.Query("source")

	projects, total, err := service.NewProjectService().List(page, pageSize, keyword, status, source)
	if err != nil {
		zap.L().Error("list projects failed", zap.Error(err))
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"data": projects, "total": total, "page": page, "page_size": pageSize})
}

// Options 返回项目名称列表（用于下拉框选择，不含敏感字段）
func (h *ProjectHandler) Options(c *gin.Context) {
	projects, err := service.NewProjectService().Options()
	if err != nil {
		zap.L().Error("list project options failed", zap.Error(err))
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"data": projects})
}

func (h *ProjectHandler) Get(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	p, err := service.NewProjectService().Get(uint(id))
	if err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	c.JSON(200, gin.H{"data": p})
}

func (h *ProjectHandler) Update(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var req map[string]interface{}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	p, _ := service.NewProjectService().Get(uint(id))
	err := service.NewProjectService().Update(uint(id), req)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	userID, _ := c.Get("user_id")
	model.RecordOpLog("项目更新", p.Name, uint(id), userID.(uint), "success", "", c.ClientIP())
	c.JSON(200, gin.H{"message": "updated"})
}

func (h *ProjectHandler) Create(c *gin.Context) {
	var p model.Project
	if err := c.ShouldBindJSON(&p); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if p.Source == "" {
		p.Source = "manual"
	}
	err := service.NewProjectService().Create(&p)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	userID, _ := c.Get("user_id")
	model.RecordOpLog("项目创建", p.Name, p.ID, userID.(uint), "success", "", c.ClientIP())
	c.JSON(200, gin.H{"message": "created", "data": p})
}

func (h *ProjectHandler) Delete(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	p, _ := service.NewProjectService().Get(uint(id))
	err := service.NewProjectService().Delete(uint(id))
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	userID, _ := c.Get("user_id")
	model.RecordOpLog("项目删除", p.Name, uint(id), userID.(uint), "success", "", c.ClientIP())
	c.JSON(200, gin.H{"message": "deleted"})
}

func (h *ProjectHandler) Tasks(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))

	tasks, total, err := service.NewProjectService().GetProjectTasks(uint(id), page, pageSize)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"data": tasks, "total": total})
}
