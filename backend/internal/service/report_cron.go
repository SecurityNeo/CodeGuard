package service

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/ai-optimizer/backend/internal/model"
	"github.com/robfig/cron/v3"
	"go.uber.org/zap"
)

var (
	reportCron    *cron.Cron
	reportCronIDs map[string]cron.EntryID
	reportCronMu  sync.Mutex
)

func SetReportCron(c *cron.Cron) {
	reportCron = c
	reportCronIDs = make(map[string]cron.EntryID)
}

func InitReportCron() {
	ReloadReportCron()
}

func ReloadReportCron() {
	reportCronMu.Lock()
	defer reportCronMu.Unlock()

	if reportCron == nil {
		zap.L().Warn("ReloadReportCron called but reportCron is nil")
		return
	}

	// Remove existing report cron entries
	for t, id := range reportCronIDs {
		reportCron.Remove(id)
		zap.L().Info("removed report cron", zap.String("type", t))
		delete(reportCronIDs, t)
	}

	var configs []model.ReportConfig
	model.DB.Find(&configs)

	for _, cfg := range configs {
		if cfg.CronExpr == "" {
			continue
		}
		expr := cfg.CronExpr
		isGenerate := cfg.Enabled || cfg.GenerateEnabled
		isSend := cfg.Enabled || cfg.SendEnabled
		if !isGenerate {
			continue
		}

		// Capture variables for closure
		reportType := cfg.ReportType
		shouldSend := isSend

		id, err := reportCron.AddFunc(expr, func() {
			svc := NewReportService()

			// 解析配置中的发送分组
			var sendGroups []string
			if cfg.SendGroups != "" {
				json.Unmarshal([]byte(cfg.SendGroups), &sendGroups)
			}

			// Query recipients for logging
			query := model.DB.Where("enabled = ?", true)
			if len(sendGroups) > 0 {
				query = query.Where("group_name IN ?", sendGroups)
			}
			var recipients []model.ReportRecipient
			query.Find(&recipients)
			recipientsJSON, _ := json.Marshal(recipients)

			html, err := svc.GenerateHTML(reportType)
			if err != nil {
				zap.L().Error("report auto generate failed", zap.String("type", reportType), zap.Error(err))
				log := model.ReportLog{
					ReportType:  reportType,
					TriggerType: "auto",
					Status:      "generated_failed",
					Recipients:  string(recipientsJSON),
					ErrorMsg:    err.Error(),
					SentAt:      time.Now(),
				}
				model.DB.Create(&log)
				return
			}

			if !shouldSend {
				zap.L().Info("report auto generated (send disabled)", zap.String("type", reportType))
				return
			}

			if err := svc.SendEmail(reportType, html, sendGroups); err != nil {
				log := model.ReportLog{
					ReportType:  reportType,
					TriggerType: "auto",
					Status:      "sent_failed",
					Recipients:  string(recipientsJSON),
					HtmlContent: html,
					ErrorMsg:    err.Error(),
					SentAt:      time.Now(),
				}
				model.DB.Create(&log)
				zap.L().Error("report auto send failed", zap.String("type", reportType), zap.Error(err))
				return
			}

			log := model.ReportLog{
				ReportType:  reportType,
				TriggerType: "auto",
				Status:      "sent_success",
				Recipients:  string(recipientsJSON),
				HtmlContent: html,
				SentAt:      time.Now(),
			}
			model.DB.Create(&log)
			zap.L().Info("report auto sent", zap.String("type", reportType))
		})

		if err != nil {
			zap.L().Error("report cron register failed", zap.String("type", reportType), zap.String("expr", expr), zap.Error(err))
		} else {
			reportCronIDs[reportType] = id
			zap.L().Info("report cron registered", zap.String("type", reportType), zap.String("expr", expr))
		}
	}
}
