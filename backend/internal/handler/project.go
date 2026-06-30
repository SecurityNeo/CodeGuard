package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/ai-optimizer/backend/internal/model"
	"github.com/ai-optimizer/backend/internal/service"
	"go.uber.org/zap"
	"strconv"
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
	model.RecordOpLog("项目更新", p.Name, uint(id), "success", "", c.ClientIP())
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
	model.RecordOpLog("项目创建", p.Name, p.ID, "success", "", c.ClientIP())
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
	model.RecordOpLog("项目删除", p.Name, uint(id), "success", "", c.ClientIP())
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
