package handler

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/ai-optimizer/backend/internal/model"
	"github.com/ai-optimizer/backend/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type ReportHandler struct{}

func NewReportHandler() *ReportHandler {
	return &ReportHandler{}
}

// ============ SMTP 配置 ============

func (h *ReportHandler) GetSMTPConfig(c *gin.Context) {
	var cfg model.SMTPConfig
	if err := model.DB.First(&cfg).Error; err != nil {
		c.JSON(200, gin.H{"data": nil})
		return
	}
	c.JSON(200, gin.H{"data": cfg})
}

func (h *ReportHandler) SaveSMTPConfig(c *gin.Context) {
	var req struct {
		Host      string `json:"host" binding:"required"`
		Port      int    `json:"port" binding:"required"`
		Username  string `json:"username"`
		Password  string `json:"password"`
		FromEmail string `json:"from_email" binding:"required"`
		FromName  string `json:"from_name"`
		UseTLS    bool   `json:"use_tls"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	var cfg model.SMTPConfig
	model.DB.First(&cfg)
	cfg.Host = req.Host
	cfg.Port = req.Port
	cfg.Username = req.Username
	cfg.Password = req.Password
	cfg.FromEmail = req.FromEmail
	cfg.FromName = req.FromName
	cfg.UseTLS = req.UseTLS
	cfg.IsDefault = true

	model.DB.Save(&cfg)
	c.JSON(200, gin.H{"message": "saved"})
}

func (h *ReportHandler) TestSMTP(c *gin.Context) {
	var req struct {
		Host      string `json:"host" binding:"required"`
		Port      int    `json:"port" binding:"required"`
		Username  string `json:"username"`
		Password  string `json:"password"`
		FromEmail string `json:"from_email" binding:"required"`
		FromName  string `json:"from_name"`
		UseTLS    bool   `json:"use_tls"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	cfg := model.SMTPConfig{
		Host:      req.Host,
		Port:      req.Port,
		Username:  req.Username,
		Password:  req.Password,
		FromEmail: req.FromEmail,
		FromName:  req.FromName,
		UseTLS:    req.UseTLS,
	}
	if err := service.NewReportService().TestSMTP(&cfg); err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"message": "SMTP test email sent"})
}

// ============ 接收人管理 ============

func (h *ReportHandler) ListRecipients(c *gin.Context) {
	var list []model.ReportRecipient
	model.DB.Order("id DESC").Find(&list)
	c.JSON(200, gin.H{"data": list})
}

func (h *ReportHandler) CreateRecipient(c *gin.Context) {
	var r model.ReportRecipient
	if err := c.ShouldBindJSON(&r); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if r.GroupName == "" {
		r.GroupName = "默认分组"
	}
	model.DB.Create(&r)
	c.JSON(200, gin.H{"data": r})
}

func (h *ReportHandler) UpdateRecipient(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var r model.ReportRecipient
	if err := model.DB.First(&r, id).Error; err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	var req struct {
		Name      string `json:"name"`
		Email     string `json:"email"`
		GroupName string `json:"group_name"`
		Enabled   *bool  `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}
	if req.Name != "" {
		r.Name = req.Name
	}
	if req.Email != "" {
		r.Email = req.Email
	}
	if req.GroupName != "" {
		r.GroupName = req.GroupName
	}
	if req.Enabled != nil {
		r.Enabled = *req.Enabled
	}
	model.DB.Save(&r)
	c.JSON(200, gin.H{"data": r})
}

func (h *ReportHandler) DeleteRecipient(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	model.DB.Delete(&model.ReportRecipient{}, id)
	c.JSON(200, gin.H{"message": "deleted"})
}

// ============ 报告配置 ============

func (h *ReportHandler) GetReportConfig(c *gin.Context) {
	reportType := c.Param("type")
	if reportType != "weekly" && reportType != "monthly" {
		c.JSON(400, gin.H{"error": "invalid type"})
		return
	}

	var cfg model.ReportConfig
	if err := model.DB.Where("report_type = ?", reportType).First(&cfg).Error; err != nil {
		// 首次访问时创建默认配置
		cfg.ReportType = reportType
		if reportType == "weekly" {
			cfg.DataPeriodDays = 7
			cfg.SendDayOfWeek = 1 // 周一
			cfg.SendHour = 9
			cfg.SendMinute = 0
			cfg.CronExpr = "0 0 9 * * 1"
		} else {
			cfg.DataPeriodDays = 30
			cfg.SendDayOfMonth = 1
			cfg.SendHour = 9
			cfg.SendMinute = 0
			cfg.CronExpr = "0 0 9 1 * *"
		}
		cfg.GenerateEnabled = false
		cfg.SendEnabled = false
		cfg.Enabled = false // 兼容旧字段
		model.DB.Create(&cfg)
	}
	c.JSON(200, gin.H{"data": cfg})
}

func (h *ReportHandler) SaveReportConfig(c *gin.Context) {
	reportType := c.Param("type")
	if reportType != "weekly" && reportType != "monthly" {
		c.JSON(400, gin.H{"error": "invalid type"})
		return
	}

	var req struct {
		GenerateEnabled bool     `json:"generate_enabled"`
		SendEnabled     bool     `json:"send_enabled"`
		Enabled         bool     `json:"enabled"`
		SendGroups      []string `json:"send_groups"`
		DataPeriodDays  int      `json:"data_period_days"`
		SendHour        int      `json:"send_hour"`
		SendMinute      int      `json:"send_minute"`
		SendDayOfWeek   int      `json:"send_day_of_week"`
		SendDayOfMonth  int      `json:"send_day_of_month"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(400, gin.H{"error": err.Error()})
		return
	}

	var cfg model.ReportConfig
	model.DB.Where("report_type = ?", reportType).First(&cfg)
	cfg.ReportType = reportType
	cfg.GenerateEnabled = req.GenerateEnabled
	cfg.SendEnabled = req.SendEnabled
	// 兼容旧字段：任一开启则 Enabled=true
	cfg.Enabled = req.GenerateEnabled || req.SendEnabled || req.Enabled
	// 保存发送分组
	if len(req.SendGroups) > 0 {
		groupsJSON, _ := json.Marshal(req.SendGroups)
		cfg.SendGroups = string(groupsJSON)
	} else {
		cfg.SendGroups = ""
	}
	cfg.DataPeriodDays = req.DataPeriodDays
	cfg.SendHour = req.SendHour
	cfg.SendMinute = req.SendMinute
	cfg.SendDayOfWeek = req.SendDayOfWeek
	cfg.SendDayOfMonth = req.SendDayOfMonth
	// 重新生成 Cron 表达式
	cfg.CronExpr = service.NewReportService().BuildCronExpression(&cfg)
	model.DB.Save(&cfg)
	// 热重载 cron
	service.ReloadReportCron()
	c.JSON(200, gin.H{"message": "saved", "cron_expr": cfg.CronExpr})
}

// ============ 预览与发送 ============

func (h *ReportHandler) PreviewReport(c *gin.Context) {
	reportType := c.Param("type")
	if reportType != "weekly" && reportType != "monthly" {
		c.JSON(400, gin.H{"error": "invalid type"})
		return
	}

	html, err := service.NewReportService().GenerateHTML(reportType)
	if err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, html)
}

func (h *ReportHandler) SendReport(c *gin.Context) {
	reportType := c.Param("type")
	if reportType != "weekly" && reportType != "monthly" {
		c.JSON(400, gin.H{"error": "invalid type"})
		return
	}

	// 读取发送分组
	var req struct {
		Groups []string `json:"groups"`
	}
	c.ShouldBindJSON(&req)

	query := model.DB.Where("enabled = ?", true)
	if len(req.Groups) > 0 {
		query = query.Where("group_name IN ?", req.Groups)
	}
	var recipients []model.ReportRecipient
	query.Find(&recipients)
	recipientsJSON, _ := json.Marshal(recipients)

	svc := service.NewReportService()
	html, err := svc.GenerateHTML(reportType)
	if err != nil {
		log := model.ReportLog{
			ReportType:  reportType,
			TriggerType: "manual",
			Status:      "generated_failed",
			Recipients:  string(recipientsJSON),
			ErrorMsg:    err.Error(),
			SentAt:      time.Now(),
		}
		model.DB.Create(&log)
		c.JSON(500, gin.H{"error": "generate failed: " + err.Error()})
		return
	}

	if err := svc.SendEmail(reportType, html, req.Groups); err != nil {
		log := model.ReportLog{
			ReportType:  reportType,
			TriggerType: "manual",
			Status:      "sent_failed",
			Recipients:  string(recipientsJSON),
			HtmlContent: html,
			ErrorMsg:    err.Error(),
			SentAt:      time.Now(),
		}
		model.DB.Create(&log)
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}

	log := model.ReportLog{
		ReportType:  reportType,
		TriggerType: "manual",
		Status:      "sent_success",
		Recipients:  string(recipientsJSON),
		HtmlContent: html,
		SentAt:      time.Now(),
	}
	model.DB.Create(&log)
	c.JSON(200, gin.H{"message": "报告已生成并发送"})
}

// ============ 日志 ============

func (h *ReportHandler) ListLogs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("page_size", "20"))
	reportType := c.Query("type")
	status := c.Query("status")

	var total int64
	db := model.DB.Model(&model.ReportLog{})
	if reportType != "" {
		db = db.Where("report_type = ?", reportType)
	}
	if status != "" {
		db = db.Where("status = ?", status)
	}
	db.Count(&total)

	var logs []model.ReportLog
	db.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&logs)
	c.JSON(200, gin.H{"data": logs, "total": total, "page": page, "page_size": pageSize})
}

func (h *ReportHandler) GetReportLogHTML(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	var log model.ReportLog
	if err := model.DB.First(&log, id).Error; err != nil {
		c.JSON(404, gin.H{"error": "not found"})
		return
	}
	if log.HtmlContent == "" {
		c.JSON(404, gin.H{"error": "no html content"})
		return
	}
	c.Header("Content-Type", "text/html; charset=utf-8")
	c.String(http.StatusOK, log.HtmlContent)
}

func (h *ReportHandler) DeleteLog(c *gin.Context) {
	id, _ := strconv.Atoi(c.Param("id"))
	if err := model.DB.Delete(&model.ReportLog{}, id).Error; err != nil {
		c.JSON(500, gin.H{"error": err.Error()})
		return
	}
	c.JSON(200, gin.H{"message": "deleted"})
}

// InitReportConfigs 初始化周报/月报默认配置
func InitReportConfigs() {
	for _, t := range []string{"weekly", "monthly"} {
		var cfg model.ReportConfig
		if err := model.SilentFirst(model.DB.Where("report_type = ?", t), &cfg); err != nil {
			cfg.ReportType = t
			if t == "weekly" {
				cfg.DataPeriodDays = 7
				cfg.SendDayOfWeek = 1
			} else {
				cfg.DataPeriodDays = 30
				cfg.SendDayOfMonth = 1
			}
			cfg.SendHour = 9
			cfg.SendMinute = 0
			cfg.CronExpr = service.NewReportService().BuildCronExpression(&cfg)
			cfg.GenerateEnabled = false
			cfg.SendEnabled = false
			cfg.Enabled = false
			if err := model.DB.Create(&cfg).Error; err != nil {
				zap.L().Error("init report config failed", zap.String("type", t), zap.Error(err))
			}
		}
	}
}
