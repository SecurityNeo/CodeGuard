package model

import (
	"fmt"
	"strings"

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
		Logger:                                   logger.Default.LogMode(logger.Warn),
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	if err != nil {
		return fmt.Errorf("connect database failed: %w", err)
	}

	DB = db
	return autoMigrate()
}

// migrateOAuthColumns 兼容已有表：添加 SystemConfig 新增的 OAuth 列
func migrateSystemConfigColumns() {
	// GORM 将 GitlabOAuthClientID 翻译为 gitlab_o_auth_client_id（多出一个下划线）
	// 而 json tag / frontend 使用的是 gitlab_oauth_client_id
	// 这导致数据库中可能同时存在两套列名，数据读写分离
	// 修复：显式指定 column tag 后，统一使用正确命名的列，删除错误列

	correctColumns := []struct {
		name string
		def  string
	}{
		{"gitlab_oauth_enabled", "TINYINT(1) NOT NULL DEFAULT 0"},
		{"gitlab_base_url", "VARCHAR(512)"},
		{"gitlab_oauth_client_id", "VARCHAR(255)"},
		{"gitlab_oauth_client_secret", "VARCHAR(255)"},
		{"gitlab_oauth_redirect_uri", "VARCHAR(512)"},
		{"gitlab_oauth_auto_create_user", "TINYINT(1) NOT NULL DEFAULT 1"},
		{"gitlab_oauth_skip_verify", "TINYINT(1) NOT NULL DEFAULT 0"},
	}

	wrongSuffixes := []string{
		"_o_auth_", // GORM 错误生成的 gitlab_o_auth_client_id 等
	}

	// 1. 删除错误命名的列（如果存在）
	for _, col := range correctColumns {
		wrongName := strings.ReplaceAll(col.name, "gitlab_oauth_", "gitlab_o_auth_")
		if DB.Migrator().HasColumn(&SystemConfig{}, wrongName) {
			if err := DB.Exec("ALTER TABLE system_configs DROP COLUMN " + wrongName).Error; err != nil {
				zap.L().Warn("drop wrong column failed, may have data",
					zap.String("column", wrongName), zap.Error(err))
			} else {
				zap.L().Info("dropped wrong column", zap.String("column", wrongName))
			}
		}
		_ = wrongSuffixes // 标记使用
	}

	// 2. 创建正确命名的列（如果不存在）
	for _, col := range correctColumns {
		if !DB.Migrator().HasColumn(&SystemConfig{}, col.name) {
			sql := fmt.Sprintf("ALTER TABLE system_configs ADD COLUMN %s %s", col.name, col.def)
			if err := DB.Exec(sql).Error; err != nil {
				zap.L().Warn("add column failed", zap.String("column", col.name), zap.Error(err))
			} else {
				zap.L().Info("added column", zap.String("column", col.name))
			}
		}
	}
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

	// 兼容已有表：补充新增列（AutoMigrate 在某些场景下可能遗漏）
	migrateSystemConfigColumns()

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
