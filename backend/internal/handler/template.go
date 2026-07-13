package handler

import (
	"encoding/json"
	"fmt"

	"github.com/ai-optimizer/backend/internal/engine"
	"github.com/ai-optimizer/backend/internal/model"
	"github.com/ai-optimizer/backend/internal/service"
	"github.com/ai-optimizer/backend/pkg/llm"
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
		dwStr := v.(string)
		if dwStr == "" {
			dwStr = "{}"
		}
		// 校验权重 JSON 格式合法且和为 100
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
		updates["dimension_weights"] = dwStr
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

// PreviewComment 预览评论模板渲染效果
func (h *TemplateHandler) PreviewComment(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))

	var req struct {
		TotalScore      int                      `json:"total_score"`
		Summary         string                   `json:"summary"`
		Dimensions      map[string]map[string]int `json:"dimensions"`
		Issues          []struct {
			RuleCode    string `json:"rule_code"`
			Severity    string `json:"severity"`
			Category    string `json:"category"`
			File        string `json:"file"`
			LineStart   int    `json:"line_start"`
			LineEnd     int    `json:"line_end"`
			CodeSnippet string `json:"code_snippet"`
			Message     string `json:"message"`
			Suggestion  string `json:"suggestion"`
		} `json:"issues"`
		Recommendations []string `json:"recommendations"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	// 获取模板
	t, err := service.NewTemplateService().Get(uint(id))
	if err != nil {
		c.JSON(404, gin.H{"error": "template not found"})
		return
	}

	// 构建模拟的 AIReviewResult
	result := &llm.AIReviewResult{
		SchemaVersion:   "1.0",
		TotalScore:      req.TotalScore,
		Summary:         req.Summary,
		Recommendations: req.Recommendations,
	}

	// 转换 dimensions
	if req.Dimensions != nil {
		result.Dimensions = make(map[string]llm.Dimension)
		for k, v := range req.Dimensions {
			result.Dimensions[k] = llm.Dimension{
				Score:  v["score"],
				Weight: v["weight"],
			}
		}
	}

	// 转换 issues
	for _, issue := range req.Issues {
		result.Issues = append(result.Issues, llm.AIReviewIssue{
			RuleCode:    issue.RuleCode,
			Severity:    issue.Severity,
			Category:    issue.Category,
			File:        issue.File,
			LineStart:   issue.LineStart,
			LineEnd:     issue.LineEnd,
			CodeSnippet: issue.CodeSnippet,
			Message:     issue.Message,
			Suggestion:  issue.Suggestion,
		})
	}

	// 组装 Markdown
	var templateStr string
	if t.GitLabCommentTemplate != "" {
		templateStr = t.GitLabCommentTemplate
	} else {
		templateStr = engine.DefaultGitLabCommentTemplate
	}

	markdown, err := engine.AssembleMarkdownComment(result, templateStr)
	if err != nil {
		zap.L().Error("preview comment assembly failed", zap.Error(err))
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	c.JSON(200, gin.H{"data": markdown})
}
