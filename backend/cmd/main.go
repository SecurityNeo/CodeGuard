package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ai-optimizer/backend/config"
	"github.com/ai-optimizer/backend/internal/handler"
	"github.com/ai-optimizer/backend/internal/middleware"
	"github.com/ai-optimizer/backend/internal/model"
	"github.com/ai-optimizer/backend/internal/service"
	"github.com/ai-optimizer/backend/pkg/encrypt"
	"github.com/gin-gonic/gin"
	"github.com/robfig/cron/v3"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	cronRunner *cron.Cron
)

func main() {
	// 1. 加载配置
	cfg := config.Load()

	// 1.5 初始化加密模块
	encrypt.Init(cfg.EncryptKey)

	// 2. 初始化日志
	logger := initLogger(cfg)
	defer logger.Sync()
	zap.ReplaceGlobals(logger)

	// 3. 初始化数据库
	if err := model.InitDB(cfg); err != nil {
		logger.Fatal("init database failed", zap.Error(err))
	}

	// 4. 初始化加密模块
	initEncrypt(cfg.EncryptKey)

	// 4.1 初始化 admin 用户
	if err := service.NewUserService().InitAdmin(); err != nil {
		logger.Warn("init admin user failed", zap.Error(err))
	}

	// 4.2. 初始化报表默认配置
	handler.InitReportConfigs()

	// 5. 初始化定时任务
	cronRunner := initCron(cfg)
	defer cronRunner.Stop()

	// 5.1. 初始化报表定时任务
	service.SetReportCron(cronRunner)
	service.InitReportCron()

	// 6. 初始化 HTTP Router
	router := setupRouter(cfg)

	// 7. 启动服务
	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: router,
	}

	go func() {
		logger.Info("server starting", zap.Int("port", cfg.Port))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("server listen failed", zap.Error(err))
		}
	}()

	// 8. 优雅退出
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Fatal("server forced to shutdown", zap.Error(err))
	}
	logger.Info("server exited")
}

func initLogger(cfg *config.Config) *zap.Logger {
	level := zap.InfoLevel
	if cfg.Debug {
		level = zap.DebugLevel
	}
	cfgZap := zap.NewProductionConfig()
	cfgZap.Level = zap.NewAtomicLevelAt(level)
	// 使用人类可读的时间格式
	cfgZap.EncoderConfig.TimeKey = "ts"
	cfgZap.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	logger, err := cfgZap.Build()
	if err != nil {
		panic(err)
	}
	return logger
}

func initEncrypt(key string) {
	if key == "" {
		zap.L().Warn("ENCRYPTION_KEY is empty, sensitive data will be stored in plain text")
	}
}

func initCron(cfg *config.Config) *cron.Cron {
	cronRunner = cron.New(cron.WithSeconds())

	// 数据同步日志定期清理（每60分钟检查一次）
	_, _ = cronRunner.AddFunc("@every 1h", func() {
		service.CleanupSyncLogs()
	})

	// 资源池健康检查（后台守护进程）
	service.NewPoolService().StartHealthCheckDaemon()

	// 模型健康检查（后台守护进程）
	service.NewModelService().StartHealthCheckDaemon()

	// 任务超时检测（每 10 秒检查）
	_, _ = cronRunner.AddFunc("@every 10s", func() {
		service.NewTaskService().TimeoutCheck()
	})

	// MR 状态同步（动态间隔，由 mr_sync_interval_sec 控制）
	service.InitMRSyncCron(cronRunner)

	cronRunner.Start()
	zap.L().Info("cron jobs started")
	return cronRunner
}

func setupRouter(cfg *config.Config) *gin.Engine {
	if !cfg.Debug {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(middleware.Logger())
	r.Use(middleware.CORS())

	// 静态文件服务 - 前端页面
	frontendPath := cfg.FrontendPath
	if frontendPath == "" {
		frontendPath = "/data/ai-bug-fix/prototype"
	}
	r.Static("/js", frontendPath+"/js")
	r.Static("/vendor", frontendPath+"/vendor")
	r.Static("/static", frontendPath)
	r.GET("/projects.html", func(c *gin.Context) {
		c.File(frontendPath + "/projects.html")
	})
	r.GET("/models.html", func(c *gin.Context) {
		c.File(frontendPath + "/models.html")
	})
	r.GET("/tasks.html", func(c *gin.Context) {
		c.File(frontendPath + "/tasks.html")
	})
	r.GET("/pools.html", func(c *gin.Context) {
		c.File(frontendPath + "/pools.html")
	})
	r.GET("/notifiers.html", func(c *gin.Context) {
		c.File(frontendPath + "/notifiers.html")
	})
	r.GET("/settings.html", func(c *gin.Context) {
		c.File(frontendPath + "/settings.html")
	})
	r.GET("/templates.html", func(c *gin.Context) {
		c.File(frontendPath + "/templates.html")
	})
	r.GET("/project-detail.html", func(c *gin.Context) {
		c.File(frontendPath + "/project-detail.html")
	})
	r.GET("/pool-detail.html", func(c *gin.Context) {
		c.File(frontendPath + "/pool-detail.html")
	})
	r.GET("/login.html", func(c *gin.Context) {
		c.File(frontendPath + "/login.html")
	})
	r.GET("/mr-stats.html", func(c *gin.Context) {
		c.File(frontendPath + "/mr-stats.html")
	})
	r.GET("/statistics.html", func(c *gin.Context) {
		c.File(frontendPath + "/statistics.html")
	})
	r.GET("/", func(c *gin.Context) {
		c.File(frontendPath + "/statistics.html")
	})
    r.GET("/index.html", func(c *gin.Context) {
        c.File(frontendPath + "/index.html")
    })
    r.GET("/mail.html", func(c *gin.Context) {
        c.File(frontendPath + "/mail.html")
    })
    r.GET("/report.html", func(c *gin.Context) {
		c.File(frontendPath + "/report.html")
	})
	r.GET("/member-mappings.html", func(c *gin.Context) {
		c.File(frontendPath + "/member-mappings.html")
	})

	// 健康检查
	r.GET("/health", func(c *gin.Context) {
		c.JSON(200, gin.H{"status": "ok"})
	})

	api := r.Group("/api/v1")

	// 用户认证（无需认证）
	userHandler := handler.NewUserHandler()
	api.POST("/login", userHandler.Login)
	api.POST("/logout", userHandler.Logout)

	// GitLab Webhook（无需认证 - GitLab 不会携带我们的 Token）
	// 统一入口处理 note 和 merge_request
	api.POST("/webhooks/gitlab", handler.NewWebhookHandler().GitLabWebhook)
	api.POST("/tasks/callback", handler.NewTaskHandler().Callback)

	// 以下接口需要认证
	api.Use(middleware.Auth())
	{
		// 用户信息
		api.GET("/users/me", userHandler.GetCurrentUser)
		api.PUT("/users/password", userHandler.ChangePassword)
	}

	// Dashboard
	dashboard := api.Group("/dashboard")
	{
		h := handler.NewDashboardHandler()
		dashboard.GET("/stats", h.GetStats)
		dashboard.GET("/trends", h.GetTrends)
		dashboard.GET("/recent-projects", h.GetRecentProjects)
		dashboard.GET("/recent-failures", h.GetRecentFailures)
		dashboard.GET("/task-distribution", h.GetTaskDistribution)
	}

	// 项目管理
	project := api.Group("/projects")
	{
		h := handler.NewProjectHandler()
		project.GET("", h.List)
		project.POST("", h.Create)
		project.GET("/:id", h.Get)
		project.PUT("/:id", h.Update)
		project.DELETE("/:id", h.Delete)
		project.GET("/:id/tasks", h.Tasks)
	}

	// 模版管理
	template := api.Group("/templates")
	{
		h := handler.NewTemplateHandler()
		template.GET("", h.List)
		template.GET("/:id", h.Get)
		template.POST("", h.Create)
		template.PUT("/:id", h.Update)
		template.DELETE("/:id", h.Delete)
		template.POST("/:id/clone", h.Clone)
	}

	// 任务管理
	task := api.Group("/tasks")
	{
		h := handler.NewTaskHandler()
		task.GET("", h.List)
		task.GET("/:id", h.Get)
		task.POST("", h.Create)
		task.POST("/:id/execute", h.Execute)
		task.POST("/:id/retry", h.Retry)
		task.POST("/:id/stop", h.Stop)
		task.GET("/:id/logs", h.Logs)
		task.GET("/:id/messages", h.Messages)
		task.POST("/:id/messages", h.SendMessage)
		task.GET("/:id/events", h.SubscribeEvents) // SSE 实时事件流
		task.DELETE("/:id/session", h.DeleteSession)
	}

	// 资源池管理
	pool := api.Group("/pools")
	{
		h := handler.NewPoolHandler()
		pool.GET("", h.List)
		pool.GET("/:id", h.Get)
		pool.POST("", h.Create)
		pool.PUT("/:id", h.Update)
		pool.DELETE("/:id", h.Delete)
		pool.POST("/test", h.TestConnectivity)
		pool.POST("/:id/check", h.CheckConnectivity)
		pool.PUT("/:id/toggle", h.Toggle)
		pool.PUT("/:id/default", h.SetDefault)
		pool.DELETE("/:id/default", h.UnsetDefault)
		pool.GET("/:id/skills", h.GetPoolSkills)
	}

	// 大模型管理
	model := api.Group("/models")
	{
		h := handler.NewModelHandler()
		model.GET("", h.List)
		model.GET("/default", h.GetDefault)
        model.GET("/:id/edit", h.GetForUpdate)
        model.GET("/:id", h.Get)
		model.POST("", h.Create)
		model.POST("/test", h.CreateTest)
		model.PUT("/:id", h.Update)
		model.DELETE("/:id", h.Delete)
		model.PUT("/:id/default", h.SetDefault)
		model.DELETE("/:id/default", h.UnsetDefault)
		model.POST("/:id/check", h.CheckAPI)
	}

	// 企业微信通知
	notifier := api.Group("/notifiers")
	{
		h := handler.NewNotifierHandler()
		notifier.GET("", h.List)
		notifier.GET("/:id", h.Get)
		notifier.POST("", h.Create)
		notifier.PUT("/:id", h.Update)
		notifier.PUT("/:id/template", h.UpdateTemplate)
		notifier.DELETE("/:id", h.Delete)
		notifier.POST("/:id/test", h.Test)
		notifier.PUT("/:id/toggle", h.Toggle)
	}

	// 成员映射管理
	memberMapping := api.Group("/member-mappings")
	{
		h := handler.NewMemberMappingHandler()
		memberMapping.GET("", h.List)
		memberMapping.GET("/git-users", h.GitUsers)
		memberMapping.GET("/:id", h.Get)
		memberMapping.POST("", h.Create)
		memberMapping.PUT("/:id", h.Update)
		memberMapping.DELETE("/:id", h.Delete)
		memberMapping.GET("/check", h.CheckMapping)
	}

	// 系统管理
	sys := api.Group("/system")
	{
		h := handler.NewSystemHandler()
		sys.GET("/config", h.GetConfig)
		sys.PUT("/config", h.UpdateConfig)
		sys.GET("/logs", h.OperationLogs)
		sys.DELETE("/logs", h.ClearLogs)
		sys.GET("/info", h.Info)
		sys.GET("/sync-logs", h.SyncLogs)
	}

	// MR 审查日志
	mrLog := api.Group("/mr-review-logs")
	{
		h := handler.NewMRReviewLogHandler()
		mrLog.GET("", h.List)
		mrLog.POST("/:id/mark-as-draft", h.MarkAsDraft)
		mrLog.POST("/:id/mark-as-ready", h.MarkAsReady)
		mrLog.GET("/projects", h.Projects)
		mrLog.GET("/authors", h.Authors)
		// 统计
		mrLog.GET("/statistics", handler.NewStatisticsHandler().Get)
	}

	// 报表管理
	report := api.Group("/reports")
	{
		h := handler.NewReportHandler()
		// SMTP 配置
		report.GET("/smtp", h.GetSMTPConfig)
		report.PUT("/smtp", h.SaveSMTPConfig)
		report.POST("/smtp/test", h.TestSMTP)
		// 接收人
		report.GET("/recipients", h.ListRecipients)
		report.POST("/recipients", h.CreateRecipient)
		report.PUT("/recipients/:id", h.UpdateRecipient)
		report.DELETE("/recipients/:id", h.DeleteRecipient)
		// 报告配置
		report.GET("/config/:type", h.GetReportConfig)
		report.PUT("/config/:type", h.SaveReportConfig)
		// 预览与发送
		report.GET("/preview/:type", h.PreviewReport)
		report.POST("/send/:type", h.SendReport)
		// 日志
        report.GET("/logs", h.ListLogs)
         report.DELETE("/logs/:id", h.DeleteLog)
        report.GET("/logs/:id/html", h.GetReportLogHTML)
	}

	return r
}
