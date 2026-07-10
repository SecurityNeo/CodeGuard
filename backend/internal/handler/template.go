package handler

import (
	"encoding/json"
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
	// 如果未设置 Prompt 但有 CustomInstruction，动态生成
	if t.Prompt == "" && t.CustomInstruction != "" {
		t.Prompt = "请根据以下规则进行代码审查：\n\n" + t.CustomInstruction
	}
	err := service.NewTemplateService().Create(&t)
	if err != nil {
		zap.L().Error("create template failed", zap.Error(err))
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	userID, _ := c.Get("user_id")
	model.RecordOpLog("配置化模板创建", t.Name, t.ID, userID.(uint), "success", "", c.ClientIP())
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
	updates := make(map[string]interface{})

	// 支持所有新的配置化字段
	if v, ok := req["name"]; ok {
		updates["name"] = v.(string)
	}
	if v, ok := req["description"]; ok {
		updates["description"] = v.(string)
	}
	if v, ok := req["prompt"]; ok {
		updates["prompt"] = v.(string)
	}
	if v, ok := req["custom_instruction"]; ok {
		updates["custom_instruction"] = v.(string)
	}
	var v interface{}
	var dwOk bool
	if v, dwOk = req["dimension_weights"]; dwOk {
		updates["dimension_weights"] = v.(string)
		// 校验权重 JSON 格式合法且和为 100
		dwStr := v.(string)
		if dwStr != "" && dwStr != "{}" {
			parsed, err := parseDimWeights(dwStr)
			if err != nil {
				c.JSON(400, gin.H{"error": "维度权重 JSON 格式无效: " + err.Error()})
				return
			}
			total := 0
			for _, w := range parsed {
				total += w
				if w < 0 || w > 100 {
					c.JSON(400, gin.H{"error": "维度权重必须在 0-100 之间"})
					return
				}
			}
			if total != 100 {
				c.JSON(400, gin.H{"error": fmt.Sprintf("维度权重之和必须等于 100，当前为 %d", total)})
				return
			}
		}
	}
	if v, ok := req["max_rules_per_review"]; ok {
		updates["max_rules_per_review"] = int(v.(float64))
	}
	if v, ok := req["gitlab_comment_template"]; ok {
		updates["gitlab_comment_template"] = v.(string)
	}

	t, err := service.NewTemplateService().Get(uint(id))
	if err != nil {
		c.JSON(404, gin.H{"error": "template not found"})
		return
	}
	err = service.NewTemplateService().Update(uint(id), updates)
	if err != nil {
		zap.L().Error("update template failed", zap.Error(err))
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	userID, _ := c.Get("user_id")
	model.RecordOpLog("配置化模板更新", t.Name, uint(id), userID.(uint), "success", "", c.ClientIP())
	c.JSON(200, gin.H{"message": "updated"})
}

func (h *TemplateHandler) Delete(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	t, err := service.NewTemplateService().Get(uint(id))
	if err != nil {
		c.JSON(404, gin.H{"error": "template not found"})
		return
	}
	err = service.NewTemplateService().Delete(uint(id))
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
	original, err := service.NewTemplateService().Get(uint(id))
	if err != nil {
		c.JSON(404, gin.H{"error": "template not found"})
		return
	}
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

// parseDimWeights 解析维度权重 JSON 字符串
func parseDimWeights(jsonStr string) (map[string]int, error) {
	var result map[string]int
	if err := json.Unmarshal([]byte(jsonStr), &result); err != nil {
		return nil, err
	}
	return result, nil
}
