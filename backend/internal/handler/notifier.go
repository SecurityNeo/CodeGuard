package handler

import (
	"strconv"

	"github.com/ai-optimizer/backend/internal/model"
	"github.com/ai-optimizer/backend/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type NotifierHandler struct{}

func NewNotifierHandler() *NotifierHandler {
	return &NotifierHandler{}
}

func (h *NotifierHandler) List(c *gin.Context) {
	notifiers, err := service.NewNotifierService().List()
	if err != nil {
		zap.L().Error("list notifiers failed", zap.Error(err))
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	type NotifierResponse struct {
		ID             uint    `json:"id"`
		Name           string  `json:"name"`
		WebhookUrl     string  `json:"webhook_url"`
		HasTemplate    bool    `json:"has_template"`
		ProjectID      *uint   `json:"project_id"`
		Enabled        bool    `json:"enabled"`
		LastTestStatus string  `json:"last_test_status"`
		CreatedAt      string  `json:"created_at"`
	}

	result := make([]NotifierResponse, 0, len(notifiers))
	for _, n := range notifiers {
		result = append(result, NotifierResponse{
			ID:             n.ID,
			Name:           n.Name,
			WebhookUrl:     n.WebhookUrl,
			HasTemplate:    n.MessageTemplate != "",
			ProjectID:      n.ProjectID,
			Enabled:        n.Enabled,
			LastTestStatus: n.LastTestStatus,
			CreatedAt:      n.CreatedAt.Format("2006-01-02 15:04:05"),
		})
	}

	c.JSON(200, gin.H{"data": result})
}

func (h *NotifierHandler) Get(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	notifier, err := service.NewNotifierService().Get(uint(id))
	if err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}

	c.JSON(200, gin.H{
		"data": map[string]interface{}{
			"id":               notifier.ID,
			"name":             notifier.Name,
			"webhook_url":      notifier.WebhookUrl,
			"has_template":     notifier.MessageTemplate != "",
			"message_template": notifier.MessageTemplate,
			"project_id":       notifier.ProjectID,
			"enabled":          notifier.Enabled,
		},
	})
}

func (h *NotifierHandler) Create(c *gin.Context) {
	var data map[string]interface{}
	if err := c.ShouldBindJSON(&data); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	required := []string{"name", "webhook_url"}
	for _, key := range required {
		if _, ok := data[key]; !ok {
			c.JSON(400, gin.H{"error": "missing field: " + key})
			return
		}
	}

	notifier, err := service.NewNotifierService().Create(data)
	if err != nil {
		zap.L().Error("create notifier failed", zap.Error(err))
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	model.RecordOpLog("通知配置创建", notifier.Name, notifier.ID, "success", "", c.ClientIP())
	c.JSON(200, gin.H{"message": "created", "data": map[string]interface{}{"id": notifier.ID}})
}

func (h *NotifierHandler) Update(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var data map[string]interface{}
	if err := c.ShouldBindJSON(&data); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	// 如果 webhook_url 为空，不更新
	if url, ok := data["webhook_url"].(string); ok && url == "" {
		delete(data, "webhook_url")
	}

	err := service.NewNotifierService().Update(uint(id), data)
	if err != nil {
		zap.L().Error("update notifier failed", zap.Error(err))
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	model.RecordOpLog("通知配置更新", strconv.Itoa(id), uint(id), "success", "", c.ClientIP())
	c.JSON(200, gin.H{"message": "updated"})
}

// UpdateTemplate 更新消息模板
func (h *NotifierHandler) UpdateTemplate(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var data map[string]interface{}
	if err := c.ShouldBindJSON(&data); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	template, ok := data["message_template"].(string)
	if !ok {
		c.JSON(400, gin.H{"error": "missing field: message_template"})
		return
	}

	err := service.NewNotifierService().UpdateTemplate(uint(id), template)
	if err != nil {
		zap.L().Error("update template failed", zap.Error(err))
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	model.RecordOpLog("通知模板更新", strconv.Itoa(id), uint(id), "success", "", c.ClientIP())
	c.JSON(200, gin.H{"message": "updated"})
}

func (h *NotifierHandler) Delete(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))

	notifier, _ := service.NewNotifierService().Get(uint(id))
	err := service.NewNotifierService().Delete(uint(id))
	if err != nil {
		zap.L().Error("delete notifier failed", zap.Error(err))
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	model.RecordOpLog("通知配置删除", notifier.Name, uint(id), "success", "", c.ClientIP())
	c.JSON(200, gin.H{"message": "deleted"})
}

func (h *NotifierHandler) Test(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))

	success, msg, err := service.NewNotifierService().Test(uint(id))
	if err != nil {
		zap.L().Error("test notifier failed", zap.Error(err))
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	if success {
		c.JSON(200, gin.H{"message": "测试消息发送成功", "status": "success"})
	} else {
		c.JSON(400, gin.H{"error": "发送失败: " + msg})
	}
}

func (h *NotifierHandler) Toggle(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var data map[string]interface{}
	if err := c.ShouldBindJSON(&data); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	enabled := true
	if v, ok := data["enabled"].(bool); ok {
		enabled = v
	}

	err := service.NewNotifierService().Toggle(uint(id), enabled)
	if err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, gin.H{"message": "updated", "enabled": enabled})
}
