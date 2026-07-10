package engine

import (
	"encoding/json"
	"fmt"

	"github.com/ai-optimizer/backend/internal/model"
	"github.com/ai-optimizer/backend/pkg/llm"
	"go.uber.org/zap"
)

// PersistStructuredReview 将结构化评审结果持久化到数据库
func PersistStructuredReview(taskID uint, result *llm.AIReviewResult) error {
	// 1. 更新 Task 表
	taskUpdates := map[string]interface{}{
		"ai_response_json":   marshalJSON(result),
		"dimension_scores":   marshalJSON(result.Dimensions),
		"issue_count":        len(result.Issues),
		"score_value":        result.TotalScore,
	}

	if err := model.DB.Model(&model.Task{}).Where("id = ?", taskID).Updates(taskUpdates).Error; err != nil {
		return fmt.Errorf("update task structured review failed: %w", err)
	}

	// 2. 插入 review_issues
	for _, issue := range result.Issues {
		reviewIssue := model.ReviewIssue{
			TaskID:      taskID,
			RuleCode:    issue.RuleCode,
			Category:    issue.Category,
			Severity:    issue.Severity,
			File:        issue.File,
			LineStart:   issue.LineStart,
			LineEnd:     issue.LineEnd,
			CodeSnippet: issue.CodeSnippet,
			Message:     issue.Message,
			Suggestion:  issue.Suggestion,
		}

		// 查找 rule_id
		if issue.RuleCode != "" {
			var rule model.ReviewRule
			if err := model.DB.Where("code = ?", issue.RuleCode).First(&rule).Error; err == nil {
				reviewIssue.RuleID = &rule.ID
			}
		}

		if err := model.DB.Create(&reviewIssue).Error; err != nil {
			zap.L().Warn("create review issue failed",
				zap.Uint("task_id", taskID),
				zap.String("rule_code", issue.RuleCode),
				zap.Error(err))
			// 单条失败不影响其他
		}
	}

	zap.L().Info("structured review persisted",
		zap.Uint("task_id", taskID),
		zap.Int("score", result.TotalScore),
		zap.Int("issue_count", len(result.Issues)))

	return nil
}

func marshalJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
