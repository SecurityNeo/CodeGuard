package engine

import (
	"encoding/json"
	"fmt"

	"github.com/ai-optimizer/backend/internal/model"
	"github.com/ai-optimizer/backend/pkg/llm"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// PersistStructuredReview 将结构化评审结果幂等持久化到数据库
// 每次调用会先清理该任务的历史 Issue，再写入最新结果，确保 Retry 后数据一致
func PersistStructuredReview(taskID uint, result *llm.AIReviewResult) error {
	return model.DB.Transaction(func(tx *gorm.DB) error {
		// 1. 更新 Task 表
		taskUpdates := map[string]interface{}{
			"ai_response_json": marshalJSON(result),
			"dimension_scores": marshalJSON(result.Dimensions),
			"issue_count":      len(result.Issues),
			"score_value":      result.TotalScore,         // 后置校验后的最终评分
			"raw_ai_score":     result.OriginalTotalScore, // LLM 原始评分（用于对比）
		}

		if err := tx.Model(&model.Task{}).Where("id = ?", taskID).Updates(taskUpdates).Error; err != nil {
			return fmt.Errorf("update task structured review failed: %w", err)
		}

		// 2. 清理旧 Issue（幂等关键：每次重试前先删除历史记录）
		if err := tx.Where("task_id = ?", taskID).Delete(&model.ReviewIssue{}).Error; err != nil {
			return fmt.Errorf("delete old review issues failed: %w", err)
		}

		// 3. 插入最新 Issue
		for _, issue := range result.Issues {
			reviewIssue := model.ReviewIssue{
				TaskID:      taskID,
				RuleCode:    issue.RuleCode,
				Category:    issue.Category,
				Severity:    issue.Severity,
				DeductScore: issue.DeductScore,
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
				if err := tx.Where("code = ?", issue.RuleCode).First(&rule).Error; err == nil {
					reviewIssue.RuleID = &rule.ID
				}
			}

			if err := tx.Create(&reviewIssue).Error; err != nil {
				zap.L().Warn("create review issue failed",
					zap.Uint("task_id", taskID),
					zap.String("rule_code", issue.RuleCode),
					zap.Error(err))
				// 单条失败不影响其他
			}
		}

		// 4. 更新 task_review_rules.IssueCount（只更新本次命中的规则）
		// 先清零该任务所有规则的 issue_count，再写入新计数
		if err := tx.Model(&model.TaskReviewRule{}).
			Where("task_id = ?", taskID).
			UpdateColumn("issue_count", 0).Error; err != nil {
			zap.L().Warn("reset task review rule issue_count failed",
				zap.Uint("task_id", taskID), zap.Error(err))
		}

		ruleHitCount := make(map[string]int)
		for _, issue := range result.Issues {
			ruleHitCount[issue.RuleCode]++
		}
		for ruleCode, count := range ruleHitCount {
			if err := tx.Model(&model.TaskReviewRule{}).
				Where("task_id = ? AND rule_code = ?", taskID, ruleCode).
				UpdateColumn("issue_count", count).Error; err != nil {
				zap.L().Warn("update task review rule issue_count failed",
					zap.Uint("task_id", taskID),
					zap.String("rule_code", ruleCode),
					zap.Error(err))
			}
		}

		zap.L().Info("structured review persisted",
			zap.Uint("task_id", taskID),
			zap.Int("score", result.TotalScore),
			zap.Int("issue_count", len(result.Issues)))

		return nil
	})
}

func marshalJSON(v interface{}) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
