package service

import (
	"time"

	"github.com/ai-optimizer/backend/internal/model"
	"go.uber.org/zap"
)

// CleanupSyncLogs 按系统配置定期清理过期的数据同步日志
func CleanupSyncLogs() {
	var cfg model.SystemConfig
	if err := model.DB.First(&cfg).Error; err != nil {
		zap.L().Error("cleanup sync logs: failed to get system config", zap.Error(err))
		return
	}

	days := cfg.LogRetentionDay
	if days <= 0 {
		days = 90 // 使用统一的日志保留策略
	}

	cutoff := time.Now().AddDate(0, 0, -days)
	result := model.DB.Where("created_at < ?", cutoff).Delete(&model.SyncLog{})
	if result.Error != nil {
		zap.L().Error("cleanup sync logs failed", zap.Error(result.Error))
	} else if result.RowsAffected > 0 {
		zap.L().Info("cleanup sync logs done",
			zap.Int64("deleted", result.RowsAffected),
			zap.Time("cutoff", cutoff))
	}
}
