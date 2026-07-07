package model

import (
	"time"

	"gorm.io/gorm"
)

// --- Project ---

type Project struct {
	ID              uint            `gorm:"primaryKey" json:"id"`
	Name            string          `gorm:"size:255;not null" json:"name"`
	ProjectPath     string          `gorm:"size:255;uniqueIndex" json:"project_path"`
	GitLabProjectID int             `gorm:"column:gitlab_project_id" json:"gitlab_project_id"`
	TemplateID      uint            `gorm:"index" json:"template_id"`
	PoolID          uint            `gorm:"index" json:"pool_id"`
	DefaultModelID  *uint           `gorm:"index" json:"default_model_id"` // NULL = 未指定，不触发review任务;
	AIEnabled       bool            `gorm:"default:false" json:"ai_enabled"`
	Source          string          `gorm:"size:20;default:'manual'" json:"source"`
	AccessToken     string          `gorm:"size:500" json:"access_token"`
	LastSyncAt      *time.Time      `json:"last_sync_at"`
	SyncStatus      string          `gorm:"size:20;default:'success'" json:"sync_status"`
	SyncError       string          `gorm:"size:512" json:"sync_error"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	DeletedAt       gorm.DeletedAt  `gorm:"index" json:"deleted_at"`
	Template        ProjectTemplate `gorm:"foreignKey:TemplateID;references:ID" json:"template,omitempty"`
	Pool            ResourcePool    `gorm:"foreignKey:PoolID;references:ID" json:"pool,omitempty"`
	Model           LLMModel        `gorm:"foreignKey:DefaultModelID;references:ID" json:"model,omitempty"`
	Tasks           []Task          `gorm:"foreignKey:ProjectID" json:"tasks,omitempty"`
}

type TaskStatus string

const (
	TaskPending TaskStatus = "pending"
	TaskRunning TaskStatus = "running"
	TaskSuccess TaskStatus = "success"
	TaskFailed  TaskStatus = "failed"
	TaskTimeout TaskStatus = "timeout"
	TaskStopped TaskStatus = "stopped"
)

type Task struct {
	ID                  uint                `gorm:"primaryKey" json:"id"`
	ProjectID           uint                `gorm:"index;not null" json:"project_id"`
	MRMergeID           int                 `json:"mr_iid"`
	MRAuthor            string              `gorm:"size:100" json:"author"`
	MRAuthorDisplayName string              `gorm:"size:100" json:"author_display_name"`
	MRTitle             string              `gorm:"size:512" json:"mr_title"`
	MRURL               string              `gorm:"size:512" json:"mr_url"`
	NoteID              int                 `json:"note_id"`
	TriggerType         string              `gorm:"size:20;default:'webhook'" json:"trigger_type"`
	TriggerSource       string              `gorm:"size:30;default:'manual'" json:"trigger_source"` // manual | score_threshold
	TaskType            string              `gorm:"size:20;default:'chat'" json:"task_type"`        // chat 或 bugfix
	Status              TaskStatus          `gorm:"size:20;index;default:'pending'" json:"status"`
	SourceBranch        string              `gorm:"size:100" json:"source_branch"`
	TargetBranch        string              `gorm:"size:100" json:"target_branch"`
	PoolID              uint                `json:"pool_id"`
	UsedModelID         uint                `gorm:"column:model_id" json:"model_id"` // 实际使用的LLM模型ID（review任务）
	GitlabTokenID       uint                `json:"gitlab_token_id"`
	StartedAt           *time.Time          `json:"started_at"`
	CompletedAt         *time.Time          `json:"completed_at"`
	DurationSec         int                 `gorm:"default:0" json:"duration_sec"`
	ErrorMsg            string              `gorm:"type:longtext" json:"error_msg"`
	OpencodeSessionID   string              `gorm:"size:128" json:"opencode_session_id"`
	DiffSummary         string              `gorm:"type:text" json:"diff_summary"`
	AIPrompt            string              `gorm:"type:longtext;column:ai_prompt" json:"ai_prompt"`
	AIResponse          string              `gorm:"type:longtext" json:"ai_response"`
	RetryCount          int                 `gorm:"default:0" json:"retry_count"`
	ScoreValue          int                 `gorm:"default:0" json:"score_value"` // 评分值
	CreatedAt           time.Time           `json:"created_at"`
	UpdatedAt           time.Time           `json:"updated_at"`
	ReviewComments      []TaskReviewComment `gorm:"foreignKey:TaskID" json:"review_comments,omitempty"`
	Project             Project             `gorm:"foreignKey:ProjectID" json:"project,omitempty"`
	Pool                ResourcePool        `gorm:"foreignKey:PoolID" json:"pool,omitempty"`
	UsedModel           LLMModel            `gorm:"foreignKey:UsedModelID;references:ID" json:"used_model,omitempty"`
}

// --- TaskReviewComment 任务人工复核意见 ---
type TaskReviewComment struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	TaskID     uint      `gorm:"index;not null" json:"task_id"`
	Content    string    `gorm:"type:text;not null" json:"content"`
	RetryRound int       `gorm:"default:1" json:"retry_round"`
	OperatorID uint      `gorm:"index" json:"operator_id"`
	CreatedAt  time.Time `json:"created_at"`
}

// --- MemberMapping 成员映射（Git用户名 <-> IM用户ID）---

type IMPlatform string

const (
	IMPlatformWeCom IMPlatform = "wecom" // 企业微信
)

type MemberMapping struct {
	ID          uint       `gorm:"primaryKey" json:"id"`
	GitUsername string     `gorm:"size:100;not null;index:idx_git_im,unique" json:"git_username"`
	IMPlatform  IMPlatform `gorm:"size:20;not null;index:idx_git_im,unique" json:"im_platform"`
	IMUserID    string     `gorm:"size:100;not null" json:"im_user_id"`
	DisplayName string     `gorm:"size:100" json:"display_name"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// --- ProjectTemplate ---

type ProjectTemplate struct {
	ID          uint   `gorm:"primaryKey"`
	Name        string `gorm:"size:100;uniqueIndex;not null"`
	Description string `gorm:"size:512"`
	Prompt      string `gorm:"type:text;not null"`
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// --- ResourcePool ---

type PoolStatus string

const (
	PoolActive   PoolStatus = "active"
	PoolInactive PoolStatus = "inactive"
	PoolError    PoolStatus = "error"
)

type ResourcePool struct {
	ID               uint       `gorm:"primaryKey" json:"id"`
	Name             string     `gorm:"size:100;uniqueIndex;not null" json:"name"`
	OpencodeEndpoint string     `gorm:"size:512;not null" json:"opencode_endpoint"`
	OpencodeUsername string     `gorm:"size:100" json:"opencode_username"`
	OpencodePassword string     `gorm:"size:512" json:"opencode_password"`
	OpencodeAPIKey   string     `gorm:"size:512" json:"opencode_api_key"`
	OpencodeVersion  string     `gorm:"size:50" json:"opencode_version"`
	MaxParallel      int        `gorm:"default:5" json:"max_parallel"`
	CheckIntervalSec int        `gorm:"default:5" json:"check_interval_sec"`
	Status           PoolStatus `gorm:"size:20;default:'active'" json:"status"`
	IsDefault        bool       `gorm:"default:false" json:"is_default"`
	LastCheckAt      *time.Time `json:"last_check_at"`
	StatusChangedAt  *time.Time `json:"status_changed_at"`
	LastAlertAt      *time.Time `json:"last_alert_at"`
	CheckError       string     `gorm:"size:512" json:"check_error"`
	ActiveJobs       int        `gorm:"default:0" json:"active_jobs"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// --- LLMModel ---

type LLMModel struct {
	ID               uint       `gorm:"primaryKey" json:"id"`
	Provider         string     `gorm:"size:50;not null" json:"provider"`
	ModelID          string     `gorm:"size:100;not null" json:"model_id"`
	BaseURL          string     `gorm:"size:512;not null" json:"base_url"`
	APIKey           string     `gorm:"size:512;not null" json:"api_key"`
	MaxTokens        int        `gorm:"default:4096" json:"max_tokens"`
	TimeoutSec       int        `gorm:"default:120" json:"timeout_sec"`
	Temperature      float64    `gorm:"default:0.1" json:"temperature"`
	IsDefault        bool       `gorm:"default:false" json:"is_default"`
	IsPrimary        bool       `gorm:"default:false" json:"is_primary"` // 是否为主模型（全局唯一）
	BackupOrder      int        `gorm:"default:0" json:"backup_order"`   // 备用顺序：0=非备用，1=第一备用，2=第二备用...
	CheckIntervalSec int        `gorm:"default:5" json:"check_interval_sec"`
	Status           string     `gorm:"size:20;default:'active'" json:"status"`
	CheckError       string     `gorm:"size:512" json:"check_error"`
	LastCheckAt      *time.Time `json:"last_check_at"`
	StatusChangedAt  *time.Time `json:"status_changed_at"`
	LastAlertAt      *time.Time `json:"last_alert_at"`
	LastTestAt       *time.Time `json:"last_test_at"`
	LastTestStatus   string     `gorm:"size:20;default:''" json:"last_test_status"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// --- WeComNotifier ---

type WeComNotifier struct {
	ID              uint   `gorm:"primaryKey"`
	Name            string `gorm:"size:100;not null"`
	WebhookUrl      string `gorm:"size:512;not null"` // Webhook URL（完整URL，明文存储）
	MessageTemplate string `gorm:"type:text"`         // 消息模版
	ProjectID       *uint  `gorm:"index"`
	Enabled         bool   `gorm:"default:false"`
	LastTestAt      *time.Time
	LastTestStatus  string `gorm:"size:20"`
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// --- OperationLog ---

type OperationLog struct {
	ID         uint   `gorm:"primaryKey"`
	OpType     string `gorm:"size:50;index;not null"`
	OpObject   string `gorm:"size:100"`
	OpObjectID uint
	OpUserID   uint      // 操作人ID
	OpResult   string    `gorm:"size:20"`
	ErrorMsg   string    `gorm:"size:512"`
	RequestIP  string    `gorm:"size:45"`
	CreatedAt  time.Time `gorm:"index"`
}

// --- MergeRequestReviewLog ---

type MergeRequestReviewLog struct {
	ID                uint       `gorm:"primaryKey" json:"id"`
	ProjectName       string     `gorm:"size:255;index" json:"project_name"`
	Author            string     `gorm:"size:100;index" json:"author"`
	AuthorDisplayName string     `gorm:"column:author_display_name;size:100" json:"author_display_name"`
	SourceBranch      string     `gorm:"column:source_branch;size:100" json:"source_branch"`
	TargetBranch      string     `gorm:"column:target_branch;size:100" json:"target_branch"`
	MRID              int        `gorm:"column:mr_id;index" json:"mr_id"`
	Score             float64    `gorm:"default:0" json:"score"`
	ScoreHistory      string     `gorm:"type:text" json:"score_history"`
	ReviewCount       int        `gorm:"default:0" json:"review_count"`
	Additions         int        `json:"additions"`
	Deletions         int        `json:"deletions"`
	URL               string     `gorm:"size:512;index" json:"url"`
	LastCommitID      string     `gorm:"column:last_commit_id;size:64" json:"last_commit_id"`
	MRTitle           string     `gorm:"column:mr_title;size:512" json:"mr_title"`
	MRState           string     `gorm:"column:mr_state;size:20" json:"mr_state"`
	IsDraft           bool       `gorm:"column:is_draft;default:false" json:"is_draft"`
	Commits           string     `gorm:"type:text" json:"commits"`
	MRCreatedAt       *time.Time `gorm:"column:mr_created_at" json:"mr_created_at"`
	SyncedAt          time.Time  `json:"synced_at"`
}

// --- SystemConfig ---

type SystemConfig struct {
	ID                      uint   `gorm:"primaryKey" json:"id"`
	GitlabToken             string `gorm:"size:255" json:"gitlab_token"`
	TaskTimeoutMin          int    `gorm:"default:120" json:"task_timeout_min"`
	SyncIntervalSec         int    `gorm:"default:60" json:"sync_interval_sec"`
	MRSyncIntervalSec       int    `gorm:"default:60" json:"mr_sync_interval_sec"`
	MaxParallelTask         int    `gorm:"default:20" json:"max_parallel_task"`
	LogRetentionDay         int    `gorm:"default:90" json:"log_retention_day"`
	AILogTemplate           string `gorm:"type:text" json:"ai_log_template"`
	ScoreThreshold          int    `gorm:"default:60" json:"score_threshold"`
	ReviewTemplate          string `gorm:"type:text" json:"review_template"`
	DiffTruncationThreshold int    `gorm:"default:5000" json:"diff_truncation_threshold"`
	AlertDurationSec        int    `gorm:"default:300" json:"alert_duration_sec"`
	AlertCooldownSec        int    `gorm:"default:3600" json:"alert_cooldown_sec"`
	AlertNotifierID         uint   `gorm:"default:0" json:"alert_notifier_id"`
	AlertMentionUserIDs     string `gorm:"size:512" json:"alert_mention_user_ids"`

	// GitLab OAuth 配置（从环境变量迁移到数据库动态配置）
	GitlabOAuthEnabled        bool   `gorm:"default:false;column:gitlab_oauth_enabled" json:"gitlab_oauth_enabled"`
	GitlabBaseURL             string `gorm:"size:512;column:gitlab_base_url" json:"gitlab_base_url"`
	GitlabOAuthClientID       string `gorm:"size:255;column:gitlab_oauth_client_id" json:"gitlab_oauth_client_id"`
	GitlabOAuthClientSecret   string `gorm:"size:255;column:gitlab_oauth_client_secret" json:"gitlab_oauth_client_secret"`
	GitlabOAuthRedirectURI    string `gorm:"size:512;column:gitlab_oauth_redirect_uri" json:"gitlab_oauth_redirect_uri"`
	GitlabOAuthAutoCreateUser bool   `gorm:"default:true;column:gitlab_oauth_auto_create_user" json:"gitlab_oauth_auto_create_user"`
	GitlabOAuthSkipVerify     bool   `gorm:"default:false;column:gitlab_oauth_skip_verify" json:"gitlab_oauth_skip_verify"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// --- Role Constants ---

const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

// --- User ---
type User struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	Username       string    `gorm:"size:50;uniqueIndex;not null" json:"username"`
	DisplayName    string    `gorm:"size:100" json:"display_name"`
	Password       string    `gorm:"size:255" json:"-"`                         // GitLab 用户无本地密码
	Role           string    `gorm:"size:20;default:'user'" json:"role"`        // admin / user
	LoginType      string    `gorm:"size:20;default:'local'" json:"login_type"` // local / gitlab
	GitlabUserID   *uint64   `gorm:"index" json:"gitlab_user_id"`
	GitlabUsername string    `gorm:"size:100" json:"gitlab_username"`
	GitlabEmail    string    `gorm:"size:255" json:"gitlab_email"`
	AvatarURL      string    `gorm:"size:512" json:"avatar_url"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// --- Token ---
type Token struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"index" json:"user_id"`
	Username  string    `gorm:"size:50" json:"username"`
	Token     string    `gorm:"size:255;index" json:"-"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// --- SMTPConfig ---

type SMTPConfig struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Host      string    `gorm:"size:255;not null" json:"host"`
	Port      int       `gorm:"not null;default:587" json:"port"`
	Username  string    `gorm:"size:255" json:"username"`
	Password  string    `gorm:"size:512" json:"password"`
	FromEmail string    `gorm:"size:255;not null" json:"from_email"`
	FromName  string    `gorm:"size:100" json:"from_name"`
	UseTLS    bool      `gorm:"default:true" json:"use_tls"`
	IsDefault bool      `gorm:"default:true" json:"is_default"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// --- ReportConfig ---

type ReportConfig struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	ReportType      string    `gorm:"size:20;not null" json:"report_type"` // weekly / monthly
	Enabled         bool      `gorm:"default:false" json:"enabled"`        // 保留兼容
	GenerateEnabled bool      `gorm:"default:false;column:generate_enabled" json:"generate_enabled"`
	SendEnabled     bool      `gorm:"default:false;column:send_enabled" json:"send_enabled"`
	SendGroups      string    `gorm:"size:512" json:"send_groups"` // JSON 数组，空则发送给所有分组
	CronExpr        string    `gorm:"size:100" json:"cron_expr"`
	DataPeriodDays  int       `gorm:"default:7" json:"data_period_days"`
	SendHour        int       `gorm:"default:9" json:"send_hour"`
	SendMinute      int       `gorm:"default:0" json:"send_minute"`
	SendDayOfWeek   int       `gorm:"default:1" json:"send_day_of_week"`  // 0=Sunday, 1=Monday
	SendDayOfMonth  int       `gorm:"default:1" json:"send_day_of_month"` // 1-31
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// --- ReportRecipient ---

type ReportRecipient struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Name      string    `gorm:"size:100" json:"name"`
	Email     string    `gorm:"size:255;not null" json:"email"`
	GroupName string    `gorm:"size:100;default:'默认分组'" json:"group_name"`
	Enabled   bool      `gorm:"default:true" json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// --- ReportLog ---

type ReportLog struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	ReportType  string    `gorm:"size:20" json:"report_type"`  // weekly / monthly
	TriggerType string    `gorm:"size:20" json:"trigger_type"` // auto / manual / preview
	Status      string    `gorm:"size:20" json:"status"`       // sent_success / sent_failed / generated_success / generated_failed
	Subject     string    `gorm:"size:255" json:"subject"`
	Recipients  string    `gorm:"type:text" json:"recipients"` // JSON 数组
	HtmlContent string    `gorm:"type:longtext" json:"-"`      // 存储完整 HTML（长文本）
	ErrorMsg    string    `gorm:"type:text" json:"error_msg"`
	SentAt      time.Time `json:"sent_at"`
	CreatedAt   time.Time `json:"created_at"`
}
