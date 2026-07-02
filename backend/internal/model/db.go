package model

import (
	"fmt"

	"github.com/ai-optimizer/backend/config"
	"go.uber.org/zap"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

var DB *gorm.DB

// FilterByUser 按用户角色过滤数据查询
// admin 不过滤，user 按 authorColumn = gitlab_username 过滤
func FilterByUser(db *gorm.DB, user User, authorColumn string) *gorm.DB {
	if user.Role == RoleAdmin || user.GitlabUsername == "" {
		return db
	}
	return db.Where(authorColumn+" = ?", user.GitlabUsername)
}

func InitDB(cfg *config.Config) error {
	var dialector gorm.Dialector
	dsn := cfg.GetDSN()

	switch cfg.Database {
	case "postgres":
		dialector = postgres.Open(dsn)
	default:
		dialector = mysql.Open(dsn)
	}

	db, err := gorm.Open(dialector, &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		return fmt.Errorf("connect database failed: %w", err)
	}

	DB = db
	return autoMigrate()
}

func autoMigrate() error {
	// 按依赖顺序创建表，避免外键约束问题
	if err := DB.AutoMigrate(
		&SystemConfig{},
		&OperationLog{},
		&SyncLog{},
	); err != nil {
		return err
	}

	if err := DB.AutoMigrate(
		&User{},
		&Token{},
	); err != nil {
		return err
	}

	// 兼容：现有用户标记 login_type = local，role 保持现有值（主要是 admin）
	// 只要 role 为空字符串的记录才设为 'user'
	DB.Exec("UPDATE users SET login_type = 'local' WHERE login_type = '' OR login_type IS NULL")
	DB.Exec("UPDATE users SET role = 'admin' WHERE role = '' OR role IS NULL")

	if err := DB.AutoMigrate(
		&ResourcePool{},
		&LLMModel{},
		&WeComNotifier{},
		&MemberMapping{},
	); err != nil {
		return err
	}

	if err := DB.AutoMigrate(
		&ProjectTemplate{},
	); err != nil {
		return err
	}

	if err := DB.AutoMigrate(
		&Project{},
	); err != nil {
		return err
	}

	// 清理 Task.used_model_id 空字符串脏数据（从 string 改为 uint 前的兼容处理）
	DB.Exec("UPDATE tasks SET model_id = 0 WHERE model_id = '' OR model_id IS NULL")

	if err := DB.AutoMigrate(
		&Task{},
	); err != nil {
		return err
	}

	if err := DB.AutoMigrate(
		&MergeRequestReviewLog{},
		&SMTPConfig{},
		&ReportConfig{},
		&ReportRecipient{},
		&ReportLog{},
	); err != nil {
		return err
	}

	// 让 review_count 字段可空（已存在的记录可能没有该字段的数据，设为 0）
	DB.Exec("UPDATE merge_request_review_logs SET review_count = 0 WHERE review_count IS NULL")

	// 清理已废弃的字段：webhook_key（已从 Model 移除，旧表残留会导致插入报错）
	if DB.Migrator().HasColumn(&WeComNotifier{}, "webhook_key") {
		DB.Migrator().DropColumn(&WeComNotifier{}, "webhook_key")
	}

	return nil
}

// OrderColumn helper for sorting
func OrderColumn(key string) string {
	o := map[string]string{
		"created_at": "created_at",
		"updated_at": "updated_at",
		"status":     "status",
		"name":       "name",
	}
	if v, ok := o[key]; ok {
		return v
	}
	return "created_at DESC"
}

// Pagination helper
func Paginate(page, pageSize int) func(db *gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		if page <= 0 {
			page = 1
		}
		if pageSize <= 0 {
			pageSize = 20
		}
		if pageSize > 100 {
			pageSize = 100
		}
		offset := (page - 1) * pageSize
		return db.Offset(offset).Limit(pageSize)
	}
}

// RecordOpLog records an operation log
func RecordOpLog(opType, opObject string, objectID uint, result, errorMsg, requestIP string) {
	log := &OperationLog{
		OpType:     opType,
		OpObject:   opObject,
		OpObjectID: objectID,
		OpResult:   result,
		ErrorMsg:   errorMsg,
		RequestIP:  requestIP,
	}
	if err := DB.Create(log).Error; err != nil {
		zap.L().Error("record operation log failed", zap.Error(err))
	}
}