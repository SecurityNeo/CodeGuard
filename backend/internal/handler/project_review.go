package handler

import (
	"fmt"
	"strconv"
	"time"

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

	// 预加载所有规则，避免 N+1 查询
	var ruleIDs []uint
	for _, cfg := range configs {
		ruleIDs = append(ruleIDs, cfg.RuleID)
	}

	var rules []model.ReviewRule
	ruleMap := make(map[uint]model.ReviewRule)
	if len(ruleIDs) > 0 {
		model.DB.Where("id IN ?", ruleIDs).Find(&rules)
		for _, r := range rules {
			ruleMap[r.ID] = r
		}
	}

	var result []gin.H
	for _, cfg := range configs {
		rule := ruleMap[cfg.RuleID]
		result = append(result, gin.H{
			"rule_id":          cfg.RuleID,
			"code":             rule.Code,
			"name":             rule.Name,
			"category":         rule.Category,
			"is_enabled":       cfg.IsEnabled,
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

// BatchResolveIssues 批量处理 Issue 状态（接纳/拒绝/恢复待处理）
func (h *ProjectReviewHandler) BatchResolveIssues(c *gin.Context) {
	var req struct {
		Issues []struct {
			ID           uint   `json:"id"`
			Status       string `json:"status" binding:"required"` // accepted / rejected / pending
			RejectReason string `json:"reject_reason"`
		} `json:"issues" binding:"required,min=1"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	userID, exist := c.Get("user_id")
	if !exist {
		c.JSON(401, gin.H{"error": "未认证"})
		return
	}
	operatorID := userID.(uint)
	now := time.Now()

	for _, item := range req.Issues {
		if item.Status != "accepted" && item.Status != "rejected" && item.Status != "pending" {
			c.JSON(400, gin.H{"error": fmt.Sprintf("invalid status for issue %d", item.ID)})
			return
		}

		updates := map[string]interface{}{
			"status":       item.Status,
			"resolved_by":  operatorID,
			"resolved_at":  now,
			"is_resolved":  item.Status == "accepted" || item.Status == "rejected",
		}
		// 仅拒绝/不采纳时记录原因
		if item.Status == "rejected" || item.Status == "dismissed" {
			updates["reject_reason"] = item.RejectReason
		}

		if err := model.DB.Model(&model.ReviewIssue{}).Where("id = ?", item.ID).Updates(updates).Error; err != nil {
			zap.L().Warn("update review issue status failed",
				zap.Uint("issue_id", item.ID),
				zap.Error(err))
		}
	}

	c.JSON(200, gin.H{"message": "updated", "count": len(req.Issues)})
}
