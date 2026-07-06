package service

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/ai-optimizer/backend/internal/model"
	"github.com/ai-optimizer/backend/pkg/gitlab"
	"github.com/robfig/cron/v3"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

var (
	mrSyncCron    *cron.Cron
	mrSyncEntryID cron.EntryID
	mrSyncMu      sync.Mutex
)

// InitMRSyncCron 在 main.go 启动时初始化 MR 同步定时任务
func InitMRSyncCron(c *cron.Cron) {
	mrSyncCron = c
	RebuildMRSyncCron()
}

// RebuildMRSyncCron 重建 MR 同步定时任务（配置更新后调用）
func RebuildMRSyncCron() {
	mrSyncMu.Lock()
	defer mrSyncMu.Unlock()

	if mrSyncCron == nil {
		zap.L().Warn("mr sync cron not initialized")
		return
	}

	// 移除旧任务
	if mrSyncEntryID > 0 {
		mrSyncCron.Remove(mrSyncEntryID)
		mrSyncEntryID = 0
	}

	var cfg model.SystemConfig
	if err := model.SilentFirst(model.DB, &cfg); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			zap.L().Info("mr sync: system config not found, skipping")
		} else {
			zap.L().Error("mr sync: get config failed", zap.Error(err))
		}
		return
	}

	if cfg.MRSyncIntervalSec <= 0 {
		zap.L().Info("mr sync: disabled (interval <= 0)")
		return
	}

	spec := fmt.Sprintf("@every %ds", cfg.MRSyncIntervalSec)
	id, err := mrSyncCron.AddFunc(spec, func() {
		NewMRSyncService().SyncOpenedMRs()
	})
	if err != nil {
		zap.L().Error("mr sync: add cron job failed", zap.String("spec", spec), zap.Error(err))
		return
	}

	mrSyncEntryID = id
	zap.L().Info("mr sync cron scheduled", zap.Int("interval_sec", cfg.MRSyncIntervalSec), zap.String("spec", spec))
}

// MRSyncService 提供 MR 状态同步能力
type MRSyncService struct{}

// NewMRSyncService 创建 MR 同步服务
func NewMRSyncService() *MRSyncService {
	return &MRSyncService{}
}

// SyncOpenedMRs 轮询 GitLab API，刷新本地 opened 状态 MR 的 mr_state、is_draft 等字段
func (s *MRSyncService) SyncOpenedMRs() {
	var cfg model.SystemConfig
	if err := model.SilentFirst(model.DB, &cfg); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			zap.L().Info("mr sync: system config not found, skipping")
		} else {
			zap.L().Error("mr sync: get config failed", zap.Error(err))
		}
		return
	}

	if cfg.MRSyncIntervalSec <= 0 {
		return
	}

	// 每次最多同步 50 条最久未同步的 opened MR
	var logs []model.MergeRequestReviewLog
	if err := model.DB.Where("mr_state = ?", "opened").
		Order("synced_at ASC").
		Limit(50).
		Find(&logs).Error; err != nil {
		zap.L().Error("mr sync: query opened mr failed", zap.Error(err))
		return
	}

	if len(logs) == 0 {
		return
	}

	// 缓存项目配置，避免重复查库
	projectCache := make(map[string]*model.Project)

	for i := range logs {
		log := &logs[i]
		project := s.getProjectFromCache(log.ProjectName, projectCache)
		if project == nil || project.GitLabProjectID == 0 {
			continue
		}

		token := project.AccessToken
		if token == "" {
			token = cfg.GitlabToken
		}
		if token == "" {
			zap.L().Warn("mr sync: no token available", zap.String("project", log.ProjectName))
			continue
		}

		// 调用 GitLab API 刷新详情
		if err := gitlab.FetchMRDetails(log, project.GitLabProjectID, token); err != nil {
			zap.L().Warn("mr sync: fetch details failed", zap.String("url", log.URL), zap.Error(err))
			continue
		}

		// 更新 synced_at 并保存
		now := time.Now()
		log.SyncedAt = now
		if err := model.DB.Save(log).Error; err != nil {
			zap.L().Error("mr sync: save log failed", zap.Uint("id", log.ID), zap.Error(err))
		}
	}
}

func (s *MRSyncService) getProjectFromCache(name string, cache map[string]*model.Project) *model.Project {
	if p, ok := cache[name]; ok {
		return p
	}
	var project model.Project
	if err := model.DB.Where("name = ?", name).First(&project).Error; err != nil {
		cache[name] = nil
		return nil
	}
	cache[name] = &project
	return &project
}
