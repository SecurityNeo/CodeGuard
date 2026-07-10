package handler

import (
	"strconv"

	"github.com/ai-optimizer/backend/internal/model"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type ProjectReviewHandler struct{}

func NewProjectReviewHandler() *ProjectReviewHandler {
	return &ProjectReviewHandler{}
}

// ListRules 获取项目已配置规则
func (h *ProjectReviewHandler) ListRules(c *gin.Context) {
	projectID, _ := strconv.Atoi(c.Param("id"))

	var configs []model.ProjectReviewConfig
	if err := model.DB.Where("project_id = ?", projectID).Find(&configs).Error; err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	var result []gin.H
	for _, cfg := range configs {
		var rule model.ReviewRule
		model.DB.First(&rule, cfg.RuleID)
		result = append(result, gin.H{
			"rule_id":        cfg.RuleID,
			"code":           rule.Code,
			"name":           rule.Name,
			"category":       rule.Category,
			"is_enabled":     cfg.IsEnabled,
			"default_severity": rule.Severity,
			"project_severity": cfg.Severity,
		})
	}

	c.JSON(200, gin.H{"code": 0, "data": result})
}

// UpdateRules 批量更新项目规则配置
func (h *ProjectReviewHandler) UpdateRules(c *gin.Context) {
	projectID, _ := strconv.Atoi(c.Param("id"))

	var req struct {
		Rules []struct {
			RuleID    uint   `json:"rule_id"`
			IsEnabled bool   `json:"is_enabled"`
			Severity  string `json:"severity"`
		} `json:"rules"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	for _, r := range req.Rules {
		updates := map[string]interface{}{
			"is_enabled": r.IsEnabled,
			"severity":   r.Severity,
		}
		model.DB.Model(&model.ProjectReviewConfig{}).
			Where("project_id = ? AND rule_id = ?", projectID, r.RuleID).
			Updates(updates)
	}

	c.JSON(200, gin.H{"message": "updated"})
}

// ResetRules 重置为默认规则配置
func (h *ProjectReviewHandler) ResetRules(c *gin.Context) {
	projectID, _ := strconv.Atoi(c.Param("id"))

	// 删除现有配置
	model.DB.Where("project_id = ?", projectID).Delete(&model.ProjectReviewConfig{})

	// 重新生成
	var project model.Project
	if err := model.DB.First(&project, projectID).Error; err != nil {
		c.JSON(404, gin.H{"error": "project not found"})
		return
	}

	var rules []model.ReviewRule
	model.DB.Where("is_enabled = ? AND (language = 'common' OR language = ?)", true, project.Language).Find(&rules)

	for _, rule := range rules {
		cfg := model.ProjectReviewConfig{
			ProjectID: uint(projectID),
			RuleID:    rule.ID,
			IsEnabled: true,
			Severity:  "",
		}
		if err := model.DB.Create(&cfg).Error; err != nil {
			zap.L().Warn("reset project review config failed", zap.Error(err))
		}
	}

	c.JSON(200, gin.H{"message": "reset"})
}

// QueryStructuredReview 查询 Task 结构化评审结果
func (h *ProjectReviewHandler) QueryStructuredReview(c *gin.Context) {
	taskID, _ := strconv.Atoi(c.Param("id"))

	var task model.Task
	if err := model.DB.First(&task, taskID).Error; err != nil {
		c.JSON(404, gin.H{"error": "task not found"})
		return
	}

	if task.AIResponseJSON == "" {
		c.JSON(200, gin.H{"code": 0, "data": nil, "message": "无结构化评审数据"})
		return
	}

	// 加载 issues
	var issues []model.ReviewIssue
	model.DB.Where("task_id = ?", taskID).Order("severity DESC, id ASC").Find(&issues)

	c.JSON(200, gin.H{
		"code": 0,
		"data": gin.H{
			"total_score":      task.ScoreValue,
			"dimension_scores": task.DimensionScores,
			"issue_count":      task.IssueCount,
			"ai_response_json": task.AIResponseJSON,
			"issues":           issues,
			"markdown_comment": task.AIResponse,
		},
	})
}
