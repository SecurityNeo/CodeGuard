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
	// 注意：只对 gitlab_oauth_xxx 字段检测 GORM 错误命名（如 gitlab_o_auth_client_id）
	// gitlab_base_url 不属于 gitlab_oauth_ 前缀，不需要检测错误命名
	for _, col := range correctColumns {
		if !strings.HasPrefix(col.name, "gitlab_oauth_") {
			continue // 跳过非 gitlab_oauth_ 前缀的字段
		}
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

	// 新增：AI 结构化评审相关表
	if err := DB.AutoMigrate(
		&ReviewRule{},
		&ReviewCategory{},
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

	if err := DB.AutoMigrate(
		&Task{},
		&TaskReviewComment{},
		&ProjectReviewConfig{},
		&ReviewIssue{},
		&TaskReviewRule{},
	); err != nil {
		return err
	}

	// 清理 Task.used_model_id 空字符串脏数据（从 string 改为 uint 前的兼容处理）
	DB.Exec("UPDATE tasks SET model_id = 0 WHERE model_id = '' OR model_id IS NULL")

	// 删除已废弃的 user_review_comment 列（数据已迁移到 TaskReviewComment 表）
	if DB.Migrator().HasColumn(&Task{}, "user_review_comment") {
		if err := DB.Migrator().DropColumn(&Task{}, "user_review_comment"); err != nil {
			zap.L().Warn("drop column user_review_comment failed", zap.Error(err))
		} else {
			zap.L().Info("dropped deprecated column user_review_comment")
		}
	}

	// 初始化内置评审规则
	initBuiltInReviewRules()

	// 初始化内置评审维度
	initBuiltInReviewCategories()

	// 为现有项目生成默认规则配置
	initDefaultProjectReviewConfigs()

	// 初始化系统配置（确保任务超时等配置有默认值）
	initSystemConfig()

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

// SilentFirst 执行 First 查询但忽略 record not found 日志（初始化场景使用）
func SilentFirst(db *gorm.DB, dest interface{}, conds ...interface{}) error {
	return db.Session(&gorm.Session{Logger: logger.Default.LogMode(logger.Silent)}).First(dest, conds...).Error
}
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
func RecordOpLog(opType, opObject string, objectID, userID uint, result, errorMsg, requestIP string) {
	log := &OperationLog{
		OpType:     opType,
		OpObject:   opObject,
		OpObjectID: objectID,
		OpUserID:   userID,
		OpResult:   result,
		ErrorMsg:   errorMsg,
		RequestIP:  requestIP,
	}
	if err := DB.Create(log).Error; err != nil {
		zap.L().Error("record operation log failed", zap.Error(err))
	}
}

// initBuiltInReviewRules 初始化内置评审规则（INSERT IGNORE）
func initBuiltInReviewRules() {
	rules := []ReviewRule{
		// --- common ---
		{Code: "common-sql-injection", Name: "SQL注入", Category: "security", Severity: "critical", Language: "common", IsEnabled: true, Description: "检查拼接SQL、未参数化查询", Prompt: "检查是否存在SQL注入漏洞。重点关注：\n1. 字符串拼接SQL查询\n2. 使用fmt.Sprintf构建SQL\n3. 未使用预编译语句的参数化查询\n4. ORM的Raw方法传入变量", SortOrder: 1},
		{Code: "common-hardcoded-secret", Name: "硬编码密钥", Category: "security", Severity: "high", Language: "common", IsEnabled: true, Description: "检查硬编码API密钥、密码、Token", Prompt: "检查是否存在硬编码的敏感信息：\n1. 字符串字面量中包含 'api_key', 'secret', 'password', 'token', 'private_key' 等关键词\n2. JWT签名密钥、数据库密码、云服务凭据\n3. 配置文件中明文存储的密钥\n4. 注释中泄露的敏感信息", SortOrder: 2},
		{Code: "common-xss-vulnerability", Name: "XSS漏洞", Category: "security", Severity: "high", Language: "common", IsEnabled: true, Description: "检查未转义的用户输入输出", Prompt: "检查是否存在XSS漏洞：\n1. 用户输入未转义直接输出到HTML\n2. 使用innerHTML插入不可信内容\n3. URL参数直接反射到页面\n4. 前端模板中未使用安全插值", SortOrder: 3},
		{Code: "common-unsafe-deserialization", Name: "不安全的反序列化", Category: "security", Severity: "high", Language: "common", IsEnabled: true, Description: "检查不安全的反序列化操作", Prompt: "检查是否存在不安全的反序列化：\n1. 反序列化不可信来源的数据\n2. 使用存在已知漏洞的序列化库\n3. 未对反序列化结果进行类型校验\n4. 使用pickle/反序列化执行不可信数据", SortOrder: 4},
		{Code: "common-resource-leak", Name: "资源泄露", Category: "performance", Severity: "high", Language: "common", IsEnabled: true, Description: "检查文件、连接未关闭", Prompt: "检查是否存在资源泄露：\n1. 文件打开后未关闭\n2. 数据库连接未释放\n3. 网络连接未关闭\n4. 锁未释放\n5. 内存未回收（循环引用等）", SortOrder: 5},
		{Code: "common-n-plus-one-query", Name: "N+1查询", Category: "performance", Severity: "medium", Language: "common", IsEnabled: true, Description: "检查循环体内数据库查询", Prompt: "检查是否存在N+1查询问题：\n1. 循环体内调用数据库查询\n2. ORM懒加载导致的隐式查询\n3. 批量操作未完成的地方\n4. 应该使用JOIN或IN查询的地方使用了多次查询", SortOrder: 6},
		{Code: "common-inefficient-loop", Name: "低效循环", Category: "performance", Severity: "medium", Language: "common", IsEnabled: true, Description: "检查O(n²)、重复计算", Prompt: "检查是否存在低效循环：\n1. 循环嵌套导致O(n²)或更高复杂度\n2. 循环体内重复计算不变值\n3. 不必要的循环（可用集合操作替代）\n4. 递归未优化或可能导致栈溢出", SortOrder: 7},
		{Code: "common-magic-number", Name: "魔法数字", Category: "readability", Severity: "low", Language: "common", IsEnabled: true, Description: "检查未命名的常量", Prompt: "检查是否存在魔法数字：\n1. 代码中直接使用的未命名数字常量\n2. 应该定义为具名常量的值\n3. 魔法字符串（重复出现的相同字符串字面量）", SortOrder: 8},
		{Code: "common-deep-nesting", Name: "嵌套过深", Category: "maintainability", Severity: "medium", Language: "common", IsEnabled: true, Description: "检查if/for嵌套>4层", Prompt: "检查是否存在嵌套过深的问题：\n1. if/for/while嵌套超过4层\n2. 回调地狱（多层嵌套回调）\n3. 应该抽取为函数的复杂嵌套逻辑\n4. 使用早期返回减少嵌套", SortOrder: 9},
		{Code: "common-too-long-function", Name: "函数过长", Category: "maintainability", Severity: "medium", Language: "common", IsEnabled: true, Description: "检查函数行数>100", Prompt: "检查是否存在函数过长的问题：\n1. 函数行数超过100行\n2. 函数职责不单一（应拆分为多个函数）\n3. 参数过多（超过5个）\n4. 圈复杂度过高", SortOrder: 10},

		// --- golang ---
		{Code: "go-error-handling", Name: "错误处理不当", Category: "maintainability", Severity: "medium", Language: "golang", IsEnabled: true, Description: "检查未wrap错误或裸返回", Prompt: "检查Go代码的错误处理是否符合最佳实践：\n1. 错误返回时是否使用了fmt.Errorf(\"...: %w\", err)进行wrap\n2. 是否避免了只写 `if err != nil { return err }` 而未添加上下文\n3. 是否在错误路径上记录了足够的信息\n4. 是否避免了panic/recover的错误处理模式", SortOrder: 11},
		{Code: "go-context-propagation", Name: "Context未传递", Category: "maintainability", Severity: "medium", Language: "golang", IsEnabled: true, Description: "检查context是否正确传递", Prompt: "检查Go代码的Context传递：\n1. 函数是否接收并传递了context.Context参数\n2. HTTP handlers是否正确使用request context\n3. 数据库操作是否传入了context\n4. 跨goroutine时context是否正确传播", SortOrder: 12},
		{Code: "go-goroutine-leak", Name: "Goroutine泄露", Category: "performance", Severity: "high", Language: "golang", IsEnabled: true, Description: "检查goroutine未正确退出", Prompt: "检查是否存在Goroutine泄露：\n1. 启动的goroutine是否都有退出条件\n2. 是否使用了sync.WaitGroup正确等待\n3. channel是否可能阻塞导致goroutine无法退出\n4. 是否使用了context取消信号", SortOrder: 13},
		{Code: "go-interface-compliance", Name: "接口实现未显式校验", Category: "readability", Severity: "low", Language: "golang", IsEnabled: true, Description: "检查接口实现是否显式声明", Prompt: "检查Go代码的接口实现：\n1. 是否使用了 `var _ Interface = (*Type)(nil)` 显式声明接口实现\n2. 接口定义是否清晰\n3. 接口方法数量是否合理（接口隔离原则）", SortOrder: 14},
		{Code: "go-concurrency-race", Name: "共享状态未保护", Category: "security", Severity: "high", Language: "golang", IsEnabled: true, Description: "检查并发访问共享状态", Prompt: "检查Go代码的并发安全：\n1. 共享变量在多个goroutine中访问是否使用了sync.Mutex/RWMutex\n2. 是否使用了原子操作\n3. map在并发环境中是否安全\n4. 是否存在数据竞争", SortOrder: 15},
		{Code: "go-panic-recovery", Name: "不当使用panic", Category: "security", Severity: "high", Language: "golang", IsEnabled: true, Description: "检查panic/recovery使用", Prompt: "检查Go代码中panic的使用：\n1. 是否在生产代码中不当使用panic\n2. 是否有必要的recover机制\n3. panic信息是否泄露了敏感信息\n4. 是否应该返回error而非panic", SortOrder: 16},
		{Code: "go-prepared-statement", Name: "未使用预编译", Category: "security", Severity: "medium", Language: "golang", IsEnabled: true, Description: "检查数据库预编译语句", Prompt: "检查Go代码的数据库操作：\n1. SQL查询是否使用了参数化查询/预编译语句\n2. 是否使用fmt.Sprintf拼接SQL\n3. ORM查询是否安全", SortOrder: 17},
		{Code: "go-struct-tag", Name: "JSON tag格式错误", Category: "readability", Severity: "low", Language: "golang", IsEnabled: true, Description: "检查struct tag格式", Prompt: "检查Go代码的struct tag：\n1. json tag是否使用正确的驼峰命名\n2. tag格式是否正确（无多余空格）\n3. omitempty使用是否恰当\n4. 是否遗漏了必要的tag", SortOrder: 18},
		{Code: "go-channel-close", Name: "Channel未正确关闭", Category: "performance", Severity: "medium", Language: "golang", IsEnabled: true, Description: "检查channel关闭逻辑", Prompt: "检查Go代码的channel使用：\n1. channel是否由发送方正确关闭\n2. 是否向已关闭的channel发送数据\n3. 是否重复关闭channel\n4. select语句是否处理了所有case", SortOrder: 19},
		{Code: "go-nil-pointer", Name: "潜在空指针访问", Category: "security", Severity: "high", Language: "golang", IsEnabled: true, Description: "检查nil指针解引用", Prompt: "检查Go代码中的nil指针风险：\n1. 接口值为nil但底层类型非nil的情况\n2. 函数返回值未检查直接使用\n3. map查找结果直接访问\n4. 类型断言未检查ok值", SortOrder: 20},
		{Code: "go-string-concat-loop", Name: "循环内字符串拼接", Category: "performance", Severity: "low", Language: "golang", IsEnabled: true, Description: "检查循环中使用+拼接字符串", Prompt: "检查Go代码中的字符串拼接：\n1. 循环内是否使用了+拼接字符串（应使用strings.Builder）\n2. 大量字符串拼接是否使用了bytes.Buffer\n3. 格式化字符串是否使用了fmt.Sprintf（性能敏感场景）", SortOrder: 21},
		{Code: "go-defer-in-loop", Name: "循环内使用defer", Category: "performance", Severity: "medium", Language: "golang", IsEnabled: true, Description: "检查循环内defer资源泄漏", Prompt: "检查Go代码中defer的使用：\n1. 循环内使用defer可能导致资源延迟释放\n2. defer是否包裹了不应该defer的操作\n3. defer的执行顺序是否正确", SortOrder: 22},

		// --- python ---
		{Code: "py-bare-except", Name: "裸except", Category: "maintainability", Severity: "medium", Language: "python", IsEnabled: true, Description: "检查except未指定异常类型", Prompt: "检查Python代码的异常处理：\n1. 是否使用了裸except（应指定异常类型）\n2. except Exception是否过度宽泛\n3. 是否捕获了异常但未处理\n4. finally块是否正确使用", SortOrder: 23},
		{Code: "py-mutable-default-arg", Name: "可变默认参数", Category: "security", Severity: "high", Language: "python", IsEnabled: true, Description: "检查可变的默认参数值", Prompt: "检查Python函数默认参数：\n1. 是否使用了可变对象（list/dict）作为默认参数\n2. 默认参数是否在多次调用间共享状态\n3. 应使用None作为默认值并在函数内初始化", SortOrder: 24},
		{Code: "py-type-hint-missing", Name: "缺少类型注解", Category: "readability", Severity: "low", Language: "python", IsEnabled: true, Description: "检查函数缺少类型注解", Prompt: "检查Python代码的类型注解：\n1. 函数参数是否缺少类型注解\n2. 返回值类型是否标注\n3. 复杂类型是否使用了typing模块\n4. 类型注解是否准确", SortOrder: 25},
		{Code: "py-sql-string-format", Name: "SQL字符串格式化", Category: "security", Severity: "critical", Language: "python", IsEnabled: true, Description: "检查字符串格式化SQL", Prompt: "检查Python代码的SQL拼接：\n1. 是否使用字符串格式化（%, f-string, .format）拼接SQL\n2. 是否使用了参数化查询\n3. ORM查询是否安全\n4. 存储过程调用是否参数化", SortOrder: 26},
		{Code: "py-global-mutable", Name: "全局可变对象滥用", Category: "maintainability", Severity: "medium", Language: "python", IsEnabled: true, Description: "检查全局可变状态", Prompt: "检查Python代码的全局状态：\n1. 全局可变对象是否被多个函数修改\n2. 单例模式实现是否线程安全\n3. 模块级变量是否被意外修改\n4. 应使用函数参数传递而非全局状态", SortOrder: 27},
		{Code: "py-eval-exec", Name: "使用eval/exec", Category: "security", Severity: "critical", Language: "python", IsEnabled: true, Description: "检查危险的内置函数使用", Prompt: "检查Python代码中的危险函数：\n1. 是否使用了eval()或exec()处理用户输入\n2. 是否使用了compile() + exec\n3. subprocess或os.system是否拼接了用户输入\n4. 模板引擎中是否存在SSTI（服务器端模板注入）", SortOrder: 28},

		// --- frontend ---
		{Code: "frontend-xss-innerHTML", Name: "直接插入innerHTML", Category: "security", Severity: "high", Language: "frontend", IsEnabled: true, Description: "检查危险的DOM操作", Prompt: "检查前端代码的DOM操作：\n1. 是否直接使用innerHTML插入不可信内容\n2. 是否使用了document.write\n3. 是否对URL参数未过滤直接渲染\n4. React中是否使用了dangerouslySetInnerHTML", SortOrder: 29},
		{Code: "frontend-memory-leak", Name: "未清理事件监听", Category: "performance", Severity: "medium", Language: "frontend", IsEnabled: true, Description: "检查组件卸载未清理", Prompt: "检查前端代码的内存管理：\n1. 组件卸载时是否清理了事件监听\n2. 定时器/setInterval是否在unmount时清除\n3. 订阅是否在销毁时取消\n4. 闭包是否持有过期引用", SortOrder: 30},
		{Code: "frontend-callback-hell", Name: "回调地狱", Category: "readability", Severity: "low", Language: "frontend", IsEnabled: true, Description: "检查嵌套回调", Prompt: "检查前端代码的异步处理：\n1. 是否存在多层嵌套回调（回调地狱）\n2. 是否使用了Promise/async-await替代\n3. Promise链是否过长\n4. 错误处理是否完善", SortOrder: 31},
		{Code: "react-missing-key", Name: "列表缺少key", Category: "performance", Severity: "low", Language: "frontend", IsEnabled: true, Description: "检查React列表渲染", Prompt: "检查React代码的列表渲染：\n1. map遍历是否提供了key属性\n2. key是否使用了稳定的唯一标识\n3. 是否使用了index作为key（不推荐）\n4. 列表项重排时key是否正确", SortOrder: 32},
		{Code: "vue-mutate-prop", Name: "直接修改props", Category: "maintainability", Severity: "medium", Language: "frontend", IsEnabled: true, Description: "检查Vue props修改", Prompt: "检查Vue代码的props使用：\n1. 是否直接修改了props值\n2. 是否通过emit通知父组件更新\n3. 是否使用了computed处理派生状态\n4. 是否使用了v-model错误地修改prop", SortOrder: 33},
		{Code: "frontend-cors-misconfig", Name: "CORS配置过于宽松", Category: "security", Severity: "high", Language: "frontend", IsEnabled: true, Description: "检查CORS配置", Prompt: "检查前端/后端CORS配置：\n1. 是否允许了所有来源（*）\n2. 是否允许了危险的方法（PUT/DELETE）\n3. 是否暴露了敏感Header\n4. credentials配置是否正确", SortOrder: 34},
		{Code: "frontend-hardcoded-api-key", Name: "前端硬编码API Key", Category: "security", Severity: "critical", Language: "frontend", IsEnabled: true, Description: "检查前端泄露密钥", Prompt: "检查前端代码的密钥管理：\n1. 是否在前端代码中硬编码了API Key\n2. 是否将密钥提交到版本控制\n3. 环境变量是否正确使用\n4. 构建配置中是否泄露了敏感信息", SortOrder: 35},

		// --- java ---
		{Code: "java-null-pointer", Name: "NPE潜在风险", Category: "security", Severity: "high", Language: "java", IsEnabled: true, Description: "检查空指针风险", Prompt: "检查Java代码的NPE风险：\n1. Optional使用是否恰当\n2. 链式调用是否检查了中间null值\n3. 方法参数是否进行了null校验\n4. 集合操作是否检查了空值", SortOrder: 36},
		{Code: "java-resource-leak", Name: "未用try-with-resources", Category: "performance", Severity: "medium", Language: "java", IsEnabled: true, Description: "检查资源关闭", Prompt: "检查Java代码的资源管理：\n1. 是否使用了try-with-resources\n2. Closeable资源是否在finally中关闭\n3. 数据库连接是否及时归还\n4. 文件流是否正确关闭", SortOrder: 37},
		{Code: "java-concurrent-modification", Name: "并发修改异常", Category: "security", Severity: "high", Language: "java", IsEnabled: true, Description: "检查集合并发修改", Prompt: "检查Java代码的并发安全：\n1. 是否在迭代时修改了集合\n2. 并发环境是否使用了线程安全的集合\n3. 是否使用了CopyOnWriteArrayList等并发集合\n4. synchronized使用是否正确", SortOrder: 38},
		{Code: "java-string-concat-loop", Name: "循环内String拼接", Category: "performance", Severity: "medium", Language: "java", IsEnabled: true, Description: "检查低效字符串操作", Prompt: "检查Java代码的字符串操作：\n1. 循环内是否使用了+拼接String\n2. 是否使用了StringBuilder/StringBuffer\n3. 大量字符串拼接的场景优化", SortOrder: 39},
		{Code: "java-raw-type", Name: "使用泛型原始类型", Category: "maintainability", Severity: "low", Language: "java", IsEnabled: true, Description: "检查泛型使用", Prompt: "检查Java代码的泛型使用：\n1. 是否使用了原始类型（raw type）\n2. 泛型参数是否完整声明\n3. @SuppressWarnings是否必要\n4. 类型转换是否安全", SortOrder: 40},
		{Code: "java-transactional-misuse", Name: "事务注解使用不当", Category: "security", Severity: "high", Language: "java", IsEnabled: true, Description: "检查@Transactional使用", Prompt: "检查Java代码的事务管理：\n1. @Transactional是否在public方法上\n2. 事务传播行为是否恰当\n3. 事务边界是否合理\n4. 异常回滚配置是否正确", SortOrder: 41},
		{Code: "java-magic-number", Name: "魔法数字", Category: "readability", Severity: "low", Language: "java", IsEnabled: true, Description: "检查未命名常量", Prompt: "检查Java代码的常量使用：\n1. 是否存在魔法数字\n2. 是否使用了static final常量\n3. 枚举类型使用是否恰当\n4. 配置值是否提取到配置文件", SortOrder: 42},
		{Code: "java-singleton-race", Name: "单例模式并发问题", Category: "security", Severity: "high", Language: "java", IsEnabled: true, Description: "检查单例实现线程安全", Prompt: "检查Java代码的单例模式：\n1. 懒汉式单例是否线程安全\n2. 双重检查锁定是否正确\n3. 枚举单例是否被使用\n4. 单例状态是否被共享修改", SortOrder: 43},

		// ========== test_coverage（测试覆盖）全语言补充 ==========
		{Code: "common-missing-unit-test", Name: "缺少单元测试", Category: "test_coverage", Severity: "high", Language: "common", IsEnabled: true, Description: "新增业务逻辑未配套单元测试", Prompt: "检查本次变更是否缺少对应的单元测试：\n1. 新增的业务逻辑函数是否有测试覆盖\n2. Bug修复是否添加了回归测试\n3. 边界条件是否被测试\n4. 异常路径是否被测试\n5. 测试文件命名是否规范（*_test.go、test_*.py、*Test.java等）", SortOrder: 44},
		{Code: "common-low-test-assertion", Name: "测试断言不足", Category: "test_coverage", Severity: "medium", Language: "common", IsEnabled: true, Description: "测试仅验证 happy path 缺少断言", Prompt: "检查测试代码的断言质量：\n1. 是否只验证了正常路径而未检查错误路径\n2. 断言是否具体（避免过于宽泛的true/false判断）\n3. 是否验证了返回值的具体内容而非仅非空判断\n4. 副作用是否被验证（数据库状态、日志输出等）", SortOrder: 45},

		{Code: "go-test-table-driven", Name: "未使用表驱动测试", Category: "test_coverage", Severity: "low", Language: "golang", IsEnabled: true, Description: "Go测试应使用表驱动结构减少冗余", Prompt: "检查Go测试代码：\n1. 多个相似测试用例是否重复编写了测试函数\n2. 是否使用了表驱动测试（[]struct{name;input;want}）\n3. 子测试（t.Run）是否被使用\n4. 测试表格是否覆盖了边界值", SortOrder: 46},
		{Code: "go-test-race-unsafe", Name: "测试存在竞态风险", Category: "test_coverage", Severity: "medium", Language: "golang", IsEnabled: true, Description: "并发测试未使用-race或同步机制", Prompt: "检查Go并发相关测试：\n1. 并发测试是否使用了go test -race\n2. 并发原语（Mutex/channel）在测试中是否正确同步\n3. 共享变量在测试中是否被安全访问\n4. 是否使用了testing/quick或fuzzing测试并发场景", SortOrder: 47},

		{Code: "py-test-assert-count", Name: "测试用例过少", Category: "test_coverage", Severity: "medium", Language: "python", IsEnabled: true, Description: "核心业务缺少充分测试覆盖", Prompt: "检查Python测试代码：\n1. 核心业务函数是否有对应的测试\n2. 测试覆盖率是否充分（分支覆盖、条件覆盖）\n3. 是否使用了pytest的parametrize减少冗余\n4. fixture使用是否合理", SortOrder: 48},
		{Code: "py-test-mock-usage", Name: "Mock使用不当", Category: "test_coverage", Severity: "low", Language: "python", IsEnabled: true, Description: "Mock范围过宽或未验证调用", Prompt: "检查Python测试中Mock的使用：\n1. mock.patch是否过度宽泛（patch了整个模块而非具体方法）\n2. 是否验证了mock被正确调用（assert_called_with）\n3. mock的对象在测试后是否正确恢复\n4. 是否mock了不应被mock的内部实现", SortOrder: 49},

		{Code: "java-test-assert-all", Name: "未使用assertAll", Category: "test_coverage", Severity: "low", Language: "java", IsEnabled: true, Description: "JUnit5未使用assertAll批量断言", Prompt: "检查Java测试代码：\n1. JUnit5测试中多个独立断言是否使用了assertAll\n2. 断言失败时是否能得到完整结果而非第一个失败即终止\n3. 是否使用了适当的断言方法（assertThrows、assertIterableEquals等）\n4. @ParameterizedTest是否被使用", SortOrder: 50},
		{Code: "java-test-missing-timeout", Name: "测试缺少超时", Category: "test_coverage", Severity: "medium", Language: "java", IsEnabled: true, Description: "可能阻塞的测试未设置超时", Prompt: "检查Java测试代码：\n1. IO操作、异步调用的测试是否设置了超时（@Timeout或assertTimeout）\n2. 死锁风险的并发测试是否有超时保护\n3. 外部依赖调用的测试是否有超时控制\n4. 测试执行时间是否过长", SortOrder: 51},

		{Code: "frontend-test-missing", Name: "组件缺少测试", Category: "test_coverage", Severity: "high", Language: "frontend", IsEnabled: true, Description: "前端组件/Hook缺少单元测试", Prompt: "检查前端代码的测试覆盖：\n1. React/Vue组件是否有渲染测试\n2. 自定义Hook是否有行为测试\n3. 工具函数是否有单元测试\n4. 交互事件（点击、表单提交）是否被测试\n5. 是否使用了Testing Library而非Enzyme", SortOrder: 52},
		{Code: "frontend-test-async-await", Name: "异步测试处理不当", Category: "test_coverage", Severity: "medium", Language: "frontend", IsEnabled: true, Description: "异步测试未等待或断言时机错误", Prompt: "检查前端异步测试：\n1. await waitFor/findBy等是否正确等待异步更新\n2. 断言是否在异步操作完成后执行\n3. async/await在测试中的使用是否正确\n4. 定时器测试是否使用了jest fake timers", SortOrder: 53},

		// ========== api_design（API设计）全语言补充 ==========
		{Code: "common-api-versioning", Name: "API缺少版本控制", Category: "maintainability", Severity: "medium", Language: "common", IsEnabled: true, Description: "公共API未做版本管理", Prompt: "检查API接口设计：\n1. 公共API是否包含版本标识（v1/v2路径或Header）\n2. 版本变更是否有兼容策略\n3. 废弃的API是否有明确的弃用计划\n4. 客户端是否被通知版本变更", SortOrder: 54},
		{Code: "common-api-break-compat", Name: "破坏性变更未标记", Category: "maintainability", Severity: "high", Language: "common", IsEnabled: true, Description: "修改了public接口未声明破坏性变更", Prompt: "检查本次变更是否为破坏性变更：\n1. 是否删除了public字段/方法/参数\n2. 是否修改了返回值的类型或结构\n3. 是否修改了错误码或异常类型\n4. 是否未提前通知下游团队", SortOrder: 55},

		{Code: "go-api-pagination", Name: "列表接口缺少分页", Category: "performance", Severity: "medium", Language: "golang", IsEnabled: true, Description: "未分页的列表查询可能OOM", Prompt: "检查Go的列表/查询接口：\n1. 返回数组的接口是否提供了分页参数\n2. 默认分页大小是否合理（防止过大）\n3. 是否有最大分页限制\n4. 游标分页vs偏移分页的选择是否恰当", SortOrder: 56},
		{Code: "go-api-idempotent", Name: "修改接口缺少幂等性", Category: "security", Severity: "high", Language: "golang", IsEnabled: true, Description: "POST/PUT未实现幂等导致重复提交", Prompt: "检查Go的非安全HTTP方法：\n1. 创建/更新操作是否支持幂等键（Idempotency-Key）\n2. 重复请求是否会创建重复数据\n3. 支付/下单等关键操作是否幂等\n4. 是否使用了乐观锁或唯一索引防重", SortOrder: 57},

		{Code: "java-api-validation", Name: "入参缺少校验", Category: "security", Severity: "high", Language: "java", IsEnabled: true, Description: "Controller/Service入参未校验", Prompt: "检查Java的API入参校验：\n1. 是否使用了@Valid/@Validated进行参数校验\n2. 字符串长度、数值范围是否有约束\n3. DTO中是否缺少@NotBlank/@NotNull等注解\n4. 自定义校验规则是否覆盖业务约束\n5. 校验失败时错误响应是否友好", SortOrder: 58},
		{Code: "java-api-restful", Name: "不合规RESTful设计", Category: "readability", Severity: "low", Language: "java", IsEnabled: true, Description: "HTTP方法与资源路径不匹配", Prompt: "检查Java RESTful API设计：\n1. 是否使用了正确的HTTP方法（GET/POST/PUT/DELETE）\n2. URL路径是否使用名词复数（/users而非/getUsers）\n3. 状态码使用是否准确（201/204/400/401/403/404/409/422）\n4. 是否有合理的HATEOAS或API文档", SortOrder: 59},

		{Code: "py-api-validation", Name: "入参缺少校验", Category: "security", Severity: "high", Language: "python", IsEnabled: true, Description: "Flask/FastAPI/Django入参未严格校验", Prompt: "检查Python Web框架的入参校验：\n1. FastAPI是否使用了Pydantic模型约束字段\n2. Flask是否使用了 marshmallow/schema 校验\n3. 路径参数/查询参数是否有类型和范围限制\n4. 文件上传是否有大小和类型限制", SortOrder: 60},
		{Code: "py-api-rate-limit", Name: "接口缺少限流", Category: "security", Severity: "medium", Language: "python", IsEnabled: true, Description: "未配置速率限制导致暴力攻击", Prompt: "检查Python API的限流配置：\n1. 是否配置了全局或接口级速率限制\n2. 登录/注册等敏感接口是否有更严格的限制\n3. 限流响应是否正确（429状态码+Retry-After）\n4. 是否区分了认证用户和匿名用户", SortOrder: 61},

		// ========== logging（日志）全语言补充 ==========
		{Code: "common-sensitive-in-log", Name: "日志泄露敏感信息", Category: "security", Severity: "critical", Language: "common", IsEnabled: true, Description: "密码/Token/PII记录到日志", Prompt: "检查日志输出内容：\n1. 是否记录了密码、Token、信用卡号等PII\n2. 用户手机号、身份证号是否被记录\n3. 完整的请求体/响应体是否包含敏感字段\n4. 日志级别下调试信息是否在生产环境泄露\n5. 是否有日志脱敏或字段过滤机制", SortOrder: 62},
		{Code: "common-log-level-mismatch", Name: "日志级别与内容不符", Category: "readability", Severity: "low", Language: "common", IsEnabled: true, Description: "错误用info、调试用error", Prompt: "检查日志级别使用是否恰当：\n1. 预期的错误路径是否使用了error级别\n2. 调试信息是否混在info级别中\n3. 高频低价值日志是否使用了debug\n4. 致命错误是否使用了fatal/panic（避免滥用）", SortOrder: 63},
		{Code: "common-log-no-trace", Name: "缺少链路追踪ID", Category: "maintainability", Severity: "medium", Language: "common", IsEnabled: true, Description: "日志缺少request_id/trace_id", Prompt: "检查日志是否包含链路追踪信息：\n1. 每条日志是否包含request_id或trace_id\n2. 跨服务调用时trace_id是否正确传递\n3. 异步任务中trace_id是否保持一致\n4. 是否使用了结构化日志（JSON）便于聚合", SortOrder: 64},

		{Code: "go-log-sprintf-cost", Name: "日志使用Sprintf造成性能损失", Category: "performance", Severity: "low", Language: "golang", IsEnabled: true, Description: "log.Printf而非结构化logger", Prompt: "检查Go日志性能：\n1. 是否使用logrus/zap/slog等结构化logger\n2. 日志参数是否使用fmt.Sprintf预格式化（应避免）\n3. 高频日志是否使用了WithField/WithContext减少重复字段\n4. 日志级别检查是否在格式化之前", SortOrder: 65},

		{Code: "java-log-string-concat", Name: "日志参数拼接", Category: "performance", Severity: "medium", Language: "java", IsEnabled: true, Description: "log.info(\"a=\"+a)造成不必要的字符串构建", Prompt: "检查Java日志使用：\n1. 是否使用了SLF4J/Log4j2的占位符{}（避免字符串拼接）\n2. 是否在debug级别前进行了isDebugEnabled检查\n3. 异常堆栈是否被正确记录\n4. 日志配置是否与代码级别匹配", SortOrder: 66},

		{Code: "py-log-fstring-cost", Name: "日志使用f-string", Category: "performance", Severity: "low", Language: "python", IsEnabled: true, Description: "f-string在日志中无条件求值", Prompt: "检查Python日志性能：\n1. 是否使用了log.msg('format', arg)而非f-string/%\n2. 是否在debug日志前检查了logger.isEnabledFor\n3. 是否配置了structlog/json日志格式\n4. 异常信息是否被正确记录", SortOrder: 67},

		// ========== error_handling（错误处理）全语言补充 ==========
		{Code: "common-silent-error", Name: "错误被静默吞没", Category: "security", Severity: "high", Language: "common", IsEnabled: true, Description: "catch后未处理、未返回、未记录", Prompt: "检查代码中错误被静默处理的情况：\n1. catch/except后是否直接pass或空块\n2. 错误返回后调用方是否忽略\n3. defer中的错误是否被检查\n4. 异步操作的错误是否被await/catch\n5. 资源清理失败是否被忽略", SortOrder: 68},
		{Code: "common-error-info-leak", Name: "错误信息泄露内部细节", Category: "security", Severity: "high", Language: "common", IsEnabled: true, Description: "堆栈/SQL/路径暴露给用户", Prompt: "检查错误响应是否泄露敏感信息：\n1. 500错误响应是否包含堆栈跟踪\n2. SQL错误是否直接返回给客户端\n3. 文件路径、内部IP等是否暴露\n4. 是否对外统一了错误格式（对外隐藏细节）", SortOrder: 69},

		{Code: "py-except-pass", Name: "except后直接pass", Category: "maintainability", Severity: "medium", Language: "python", IsEnabled: true, Description: "异常被吞没无日志无告警", Prompt: "检查Python异常处理：\n1. except块中是否只有pass\n2. 是否捕获了异常但未记录日志\n3. 是否应该使用contextlib.suppress替代\n4. 异常信息是否被转换为无意义的返回值", SortOrder: 70},

		{Code: "java-swallow-exception", Name: "吞没异常堆栈", Category: "security", Severity: "high", Language: "java", IsEnabled: true, Description: "catch后只打印e.getMessage()", Prompt: "检查Java异常处理：\n1. catch块中是否只记录了e.getMessage()（丢失了堆栈）\n2. 是否使用了throw new RuntimeException(msg)而未传入cause\n3. 是否将异常转换为null/空值返回\n4. 是否在日志中完整包含了异常对象", SortOrder: 71},
		{Code: "java-generic-exception", Name: "抛出过度宽泛异常", Category: "maintainability", Severity: "medium", Language: "java", IsEnabled: true, Description: "throw new Exception()而非具体子类", Prompt: "检查Java异常类型：\n1. 是否抛出了过于宽泛的Exception/RuntimeException\n2. 是否定义了业务异常类（BusinessException）\n3. 异常层级是否清晰（checked vs unchecked）\n4. 是否应该使用标准异常（IllegalArgumentException等）", SortOrder: 72},

		{Code: "frontend-promise-unhandled", Name: "未处理Promise reject", Category: "security", Severity: "medium", Language: "frontend", IsEnabled: true, Description: "异步错误未catch导致全局崩溃", Prompt: "检查前端异步错误处理：\n1. Promise链中是否有未处理的.catch\n2. async/await是否包裹了try-catch\n3. fetch/axios请求失败是否被处理\n4. window.onunhandledrejection是否有兜底", SortOrder: 73},
		{Code: "frontend-error-boundary-missing", Name: "缺少错误边界", Category: "security", Severity: "high", Language: "frontend", IsEnabled: true, Description: "React未包裹ErrorBoundary", Prompt: "检查前端错误边界：\n1. 关键组件树是否被ErrorBoundary包裹\n2. 错误边界中是否正确记录和上报错误\n3. 是否展示了用户友好的fallback UI\n4. 路由级别是否有错误边界保护", SortOrder: 74},

		// ========== documentation（文档）全语言补充 ==========
		{Code: "common-missing-doc", Name: "公共函数缺少文档", Category: "readability", Severity: "low", Language: "common", IsEnabled: true, Description: "导出/公共API缺少注释说明", Prompt: "检查公共API的文档完整性：\n1. 公共函数/方法是否有注释说明用途、参数、返回值\n2. 复杂业务逻辑是否有内联注释\n3. 接口变更是否同步更新了文档\n4. TODO注释是否有对应的issue或责任人", SortOrder: 75},
		{Code: "common-outdated-comment", Name: "注释与代码不符", Category: "maintainability", Severity: "medium", Language: "common", IsEnabled: true, Description: "修改代码后未同步更新注释", Prompt: "检查注释的准确性：\n1. 注释描述的行为是否与代码实际行为一致\n2. 参数类型或数量变更后注释是否更新\n3. 是否包含已废弃的代码注释\n4. 注释中提到的变量名是否已更名", SortOrder: 76},
		{Code: "common-todo-unresolved", Name: "遗留TODO未处理", Category: "maintainability", Severity: "low", Language: "common", IsEnabled: true, Description: "TODO/FIXME长期未解决", Prompt: "检查TODO/FIXME注释：\n1. 新增代码中的TODO是否有明确的完成计划\n2. 长期存在的TODO是否应该转为issue\n3. 临时解决方案是否有明确的修正时间\n4. HACK/WORKAROUND注释是否被记录", SortOrder: 77},

		{Code: "go-exported-doc", Name: "导出项缺少godoc", Category: "readability", Severity: "low", Language: "golang", IsEnabled: true, Description: "exported函数/类型无文档注释", Prompt: "检查Go代码的文档：\n1. 导出函数/类型/变量是否有以名称开头的注释\n2. 包级别是否有package注释\n3. 复杂函数是否说明了前置条件和副作用\n4. 示例代码是否被编写（ExampleTest）", SortOrder: 78},

		{Code: "java-javadoc-missing", Name: "公共API缺少Javadoc", Category: "readability", Severity: "low", Language: "java", IsEnabled: true, Description: "public方法缺少@param @return", Prompt: "检查Java代码的Javadoc：\n1. public/protected方法是否有Javadoc\n2. @param和@return是否完整\n3. @throws是否说明了可能抛出的异常\n4. 类级别是否有作者和职责说明", SortOrder: 79},

		{Code: "py-docstring-missing", Name: "函数缺少docstring", Category: "readability", Severity: "low", Language: "python", IsEnabled: true, Description: "公共函数无Google/NumPy风格docstring", Prompt: "检查Python代码的文档：\n1. 公共模块/函数/类是否有docstring\n2. 是否遵循了Google/NumPy/PEP257风格\n3. 类型信息是否在docstring中与类型注解一致\n4. __init__.py是否有模块说明", SortOrder: 80},

		// ========== security（安全）补充 ==========
		{Code: "common-ssrf-risk", Name: "SSRF风险", Category: "security", Severity: "high", Language: "common", IsEnabled: true, Description: "服务端请求伪造", Prompt: "检查是否存在SSRF漏洞：\n1. 是否允许用户输入控制URL并进行请求\n2. 是否访问了内部服务（localhost/169.254/内网IP）\n3. 是否做了URL白名单校验\n4. 重定向响应是否被追踪到内部地址", SortOrder: 81},
		{Code: "common-authz-bypass", Name: "权限绕过", Category: "security", Severity: "critical", Language: "common", IsEnabled: true, Description: "缺少鉴权或鉴权逻辑可被绕过", Prompt: "检查权限控制实现：\n1. 敏感接口是否进行了身份验证和授权\n2. 鉴权中间件是否在路由之前执行\n3. 是否可能存在IDOR（不安全的直接对象引用）\n4. 管理员接口是否有额外的权限校验\n5. JWT/Session验证是否被绕过", SortOrder: 82},
		{Code: "common-path-traversal", Name: "路径遍历", Category: "security", Severity: "critical", Language: "common", IsEnabled: true, Description: "用户输入拼接文件路径", Prompt: "检查文件路径处理：\n1. 是否拼接了用户输入到文件路径\n2. 是否过滤了../等路径穿越字符\n3. 是否限制在特定目录范围内访问\n4. 上传文件名是否被重命名（避免覆盖）", SortOrder: 83},
		{Code: "common-log-injection", Name: "日志注入", Category: "security", Severity: "medium", Language: "common", IsEnabled: true, Description: "用户输入写入日志导致伪造/污染", Prompt: "检查日志中的用户输入：\n1. 是否直接将用户输入写入日志（可能伪造日志条目）\n2. 换行符/n是否被过滤（防止多行日志注入）\n3. 日志解析器是否能区分注入内容\n4. 是否使用了结构化日志降低注入风险", SortOrder: 84},
		{Code: "common-insecure-hash", Name: "使用不安全的哈希算法", Category: "security", Severity: "high", Language: "common", IsEnabled: true, Description: "MD5/SHA1用于密码或签名", Prompt: "检查密码和敏感数据的哈希算法：\n1. 是否使用了MD5/SHA1处理密码（应使用bcrypt/argon2/scrypt）\n2. 数字签名是否使用了SHA-256或更高\n3. HMAC密钥是否足够长且随机\n4. 是否使用了salt（盐值）", SortOrder: 85},

		{Code: "go-crypto-insecure-rand", Name: "不安全的随机数生成", Category: "security", Severity: "high", Language: "golang", IsEnabled: true, Description: "math/rand用于密码学场景", Prompt: "检查Go的随机数使用：\n1. 安全相关场景（Token/密钥/CSRF）是否使用了crypto/rand\n2. math/rand是否被用于安全目的\n3. 随机种子是否使用固定值（time.Now().Unix()）\n4. UUID生成是否使用了足够熵的库", SortOrder: 86},

		{Code: "py-insecure-crypto", Name: "使用弱加密算法", Category: "security", Severity: "high", Language: "python", IsEnabled: true, Description: "PyCryptodome/AES-ECB使用不当", Prompt: "检查Python加密使用：\n1. 是否使用了ECB模式（应使用CBC/GCM）\n2. 密钥是否硬编码或传输\n3. 是否使用了足够强度的算法（AES-256/ChaCha20）\n4. 哈希比较是否使用了hmac.compare_digest防时序攻击", SortOrder: 87},
		{Code: "py-ssrf-requests", Name: "requests访问不可信URL", Category: "security", Severity: "high", Language: "python", IsEnabled: true, Description: "requests.get(url)参数未校验", Prompt: "检查Python requests/urllib的URL：\n1. URL是否由用户输入拼接\n2. 是否允许访问内网地址\n3. 超时配置是否合理\n4. 重定向次数是否有限制", SortOrder: 88},

		{Code: "java-insecure-random", Name: "使用Random做安全用途", Category: "security", Severity: "high", Language: "java", IsEnabled: true, Description: "java.util.Random用于Token/密码", Prompt: "检查Java随机数使用：\n1. 安全场景是否使用了SecureRandom\n2. java.util.Random是否用于Token/CSRF/密码\n3. SecureRandom是否正确初始化\n4. 是否使用了UUID.randomUUID()用于安全标识", SortOrder: 89},
		{Code: "java-xxe-risk", Name: "XXE漏洞", Category: "security", Severity: "critical", Language: "java", IsEnabled: true, Description: "XML解析器未禁用外部实体", Prompt: "检查Java XML处理：\n1. DocumentBuilderFactory是否禁用了外部实体\n2. SAXParser是否配置了FEATURE_SECURE_PROCESSING\n3. Transformer是否禁用了DOCTYPE\n4. 是否使用了已知安全的XML库", SortOrder: 90},

		{Code: "frontend-csrf-missing", Name: "缺少CSRF保护", Category: "security", Severity: "high", Language: "frontend", IsEnabled: true, Description: "状态修改请求缺少CSRF Token", Prompt: "检查前端安全：\n1. 表单提交是否包含CSRF Token\n2. fetch/XHR的POST/PUT/DELETE是否携带Token\n3. SameSite Cookie属性是否正确设置\n4. 跨域写操作是否有额外保护", SortOrder: 91},
		{Code: "frontend-open-redirect", Name: "开放式重定向", Category: "security", Severity: "medium", Language: "frontend", IsEnabled: true, Description: "返回URL参数未校验直接跳转", Prompt: "检查前端跳转逻辑：\n1. 登录后回调地址是否被校验\n2. window.location.href是否被不可信URL赋值\n3. 重定向目标是否在白名单内\n4. router.push是否被恶意构造的URL调用", SortOrder: 92},

		// ========== performance（性能）补充 ==========
		{Code: "common-synchronous-io-block", Name: "同步IO阻塞请求", Category: "performance", Severity: "medium", Language: "common", IsEnabled: true, Description: "请求线程等待外部IO未异步化", Prompt: "检查同步阻塞操作：\n1. 外部API调用是否阻塞了请求线程\n2. 文件IO/数据库查询是否可异步化\n3. 批量操作是否有异步队列处理\n4. 是否使用了连接池（HTTP/DB）", SortOrder: 93},
		{Code: "common-missing-cache", Name: "可缓存数据未做缓存", Category: "performance", Severity: "medium", Language: "common", IsEnabled: true, Description: "重复计算/查询未使用缓存", Prompt: "检查缓存策略：\n1. 频繁访问且变化少的数据是否被缓存\n2. 数据库查询结果是否有缓存层\n3. 缓存过期策略是否合理\n4. 缓存穿透/击穿/雪崩是否有防护", SortOrder: 94},
		{Code: "common-memory-hotspot", Name: "内存热点对象", Category: "performance", Severity: "medium", Language: "common", IsEnabled: true, Description: "大对象频繁创建导致GC压力", Prompt: "检查内存使用：\n1. 是否频繁创建大对象（大字符串/大数组）\n2. 对象池/复用是否被考虑\n3. 循环内是否创建了不必要的对象\n4. 内存泄漏模式（事件监听未清理等）", SortOrder: 95},

		{Code: "go-heap-escape", Name: "变量逃逸到堆", Category: "performance", Severity: "low", Language: "golang", IsEnabled: true, Description: "指针返回导致GC压力", Prompt: "检查Go内存分配模式：\n1. 小对象是否通过指针返回导致堆分配\n2. 值接收者vs指针接收者选择是否恰当\n3. 局部变量是否被闭包捕获导致逃逸\n4. 大切片是否预分配了容量", SortOrder: 96},
		{Code: "go-sync-map-inefficient", Name: "sync.Map使用不当", Category: "performance", Severity: "low", Language: "golang", IsEnabled: true, Description: "读写频繁场景应使用map+Mutex", Prompt: "检查Go sync.Map使用：\n1. 读写比是否适合sync.Map（大量读少量写才适合）\n2. 类型化的map是否被interface{}替代（类型断言开销）\n3. 是否清楚sync.Map的Load/Store语义\n4. 高频计数场景是否使用了原子操作", SortOrder: 97},

		{Code: "py-list-comprehension", Name: "循环可替换为推导式", Category: "readability", Severity: "low", Language: "python", IsEnabled: true, Description: "for+append可简化为list comprehension", Prompt: "检查Python代码简洁性：\n1. 循环+append是否可用list comprehension替代\n2. filter+map是否可用generator expression\n3. dict comprehension是否被使用\n4. 复杂逻辑是否应保留显式循环（可读性优先）", SortOrder: 98},
		{Code: "py-generator-lazy", Name: "大数据集未使用生成器", Category: "performance", Severity: "medium", Language: "python", IsEnabled: true, Description: "全部加载到内存应改为yield", Prompt: "检查Python大数据处理：\n1. 大文件/数据集是否使用了yield/generator\n2. 列表是否可替换为生成器表达式\n3. itertools是否被使用优化内存\n4. pandas处理大文件是否使用了chunksize", SortOrder: 99},

		{Code: "java-stream-inefficient", Name: "Stream API低效使用", Category: "performance", Severity: "low", Language: "java", IsEnabled: true, Description: "多次stream()可合并", Prompt: "检查Java Stream使用：\n1. 多次stream过滤是否可合并为一次\n2. 是否在高频循环内创建stream\n3. parallelStream是否被滥用（线程安全问题）\n4. Optional.isPresent()+get()是否被正确使用", SortOrder: 100},
		{Code: "java-boxing-unboxing", Name: "装箱拆箱性能损耗", Category: "performance", Severity: "low", Language: "java", IsEnabled: true, Description: "基本类型与包装类混用", Prompt: "检查Java基本类型使用：\n1. 数值运算是否频繁装箱拆箱\n2. Map<Integer, Integer>是否可用Int2IntMap替代\n3. ArrayList<Integer> vs int[]性能差异\n4. Stream<Integer>是否可替换为IntStream", SortOrder: 101},

		{Code: "frontend-unnecessary-rerender", Name: "不必要的重渲染", Category: "performance", Severity: "medium", Language: "frontend", IsEnabled: true, Description: "state/props变更导致全树渲染", Prompt: "检查前端渲染性能：\n1. React中是否使用了React.memo/useMemo/useCallback避免不必要重渲染\n2. Vue中是否过度使用了深度响应式\n3. 大列表是否使用了虚拟滚动\n4. 第三方库组件是否被不必要地重新创建", SortOrder: 102},
		{Code: "frontend-bundle-size", Name: "引入过大依赖", Category: "performance", Severity: "low", Language: "frontend", IsEnabled: true, Description: "引入moment/full lodash等大包", Prompt: "检查前端包大小：\n1. 是否引入了完整的lodash而非lodash/debounce\n2. moment.js是否可替换为date-fns/dayjs\n3. 是否引入了未使用的UI组件\n4. 图标库是否按需引入", SortOrder: 103},

		// ========== readability（可读性）补充 ==========
		{Code: "common-ambiguous-name", Name: "命名语义不清", Category: "readability", Severity: "low", Language: "common", IsEnabled: true, Description: "变量/函数名无法表达意图（a1/tmp/do）", Prompt: "检查命名质量：\n1. 变量名是否能表达其含义（避免a/b/x/i/j无意义命名）\n2. 函数名是否说明了做什么（动词+名词）\n3. 布尔变量是否使用了疑问句式（is/has/should/can）\n4. 缩写是否过于晦涩", SortOrder: 104},
		{Code: "common-dead-code", Name: "死代码/未使用变量", Category: "readability", Severity: "low", Language: "common", IsEnabled: true, Description: "未引用的变量/函数/导入", Prompt: "检查代码是否存在死代码：\n1. 未使用的变量、参数、导入\n2. 未调用的私有函数\n3. 注释掉的大段代码\n4. 不可达的分支（永远false的条件）\n5. 重复的模板代码", SortOrder: 105},
		{Code: "common-inconsistent-style", Name: "代码风格不一致", Category: "readability", Severity: "info", Language: "common", IsEnabled: true, Description: "缩进/引号/命名规范与同项目冲突", Prompt: "检查代码风格一致性：\n1. 缩进（空格/Tab）是否与项目一致\n2. 引号风格（单/双）是否一致\n3. 命名规范（camel/snake/Pascal）是否一致\n4. 括号风格是否与项目统一\n5. import/order是否与项目lint规则一致", SortOrder: 106},

		{Code: "go-vet-shadow", Name: "变量遮蔽", Category: "readability", Severity: "low", Language: "golang", IsEnabled: true, Description: "内层变量与外层同名导致歧义", Prompt: "检查Go变量遮蔽：\n1. if简短语句中声明的变量是否与外部同名\n2. for循环变量是否在goroutine中被错误捕获\n3. 同名变量赋值是否导致意外行为\n4. 使用go vet -shadow检查结果", SortOrder: 107},
		{Code: "go-naked-return", Name: "裸返回可读性差", Category: "readability", Severity: "low", Language: "golang", IsEnabled: true, Description: "return未带变量名降低可读性", Prompt: "检查Go裸返回：\n1. 命名返回值是否被裸返回（不带变量名）\n2. 函数较长时裸返回是否导致理解困难\n3. return语句是否明确表达了返回意图\n4. 错误路径返回是否清晰", SortOrder: 108},

		{Code: "py-pep8-violation", Name: "PEP8风格违规", Category: "readability", Severity: "info", Language: "python", IsEnabled: true, Description: "行宽/空行/导入排序等PEP8规范", Prompt: "检查Python代码风格：\n1. 行宽是否超过79/100/120字符（根据项目约定）\n2. 导入排序是否符合isort规范\n3. 空行使用是否恰当（函数间两空行）\n4. 命名是否遵循PEP8（snake_case for function）\n5. 字符串引号风格是否一致", SortOrder: 109},

		{Code: "java-naming-convention", Name: "命名规范违规", Category: "readability", Severity: "low", Language: "java", IsEnabled: true, Description: "类名/方法名/常量未按Java规范", Prompt: "检查Java命名规范：\n1. 类名是否使用UpperCamelCase\n2. 方法/变量是否使用lowerCamelCase\n3. 常量是否使用UPPER_SNAKE_CASE\n4. 包名是否使用小写\n5. 布尔方法是否使用is/has/can前缀", SortOrder: 110},

		{Code: "frontend-props-drilling", Name: "过度Props透传", Category: "readability", Severity: "low", Language: "frontend", IsEnabled: true, Description: "超过3层props传递应使用Context", Prompt: "检查前端组件通信：\n1. props是否被透传超过3层\n2. 是否适合使用Context/Redux/Zustand替代\n3. 中间组件是否接收了不用的props只为传递\n4. 组合模式（composition）是否可替代props传递", SortOrder: 111},
		{Code: "frontend-mixed-import-style", Name: "导入风格混杂", Category: "readability", Severity: "info", Language: "frontend", IsEnabled: true, Description: "绝对/相对路径import混用", Prompt: "检查前端import风格：\n1. 绝对路径和相对路径是否混用（应统一）\n2. 第三方库import是否在自定义import之前\n3. 未使用的import是否被清理\n4. 是否使用了路径别名（@/components）", SortOrder: 112},
	}

	for _, rule := range rules {
		var existing ReviewRule
		if err := SilentFirst(DB.Where("code = ?", rule.Code), &existing); err != nil {
			// 不存在则插入
			if err := DB.Create(&rule).Error; err != nil {
				zap.L().Warn("init built-in rule failed", zap.String("code", rule.Code), zap.Error(err))
			} else {
				zap.L().Info("init built-in rule", zap.String("code", rule.Code))
			}
		} else {
			// 已存在，更新 IsEnabled 和 SortOrder（允许运行时调整）
			DB.Model(&existing).Updates(map[string]interface{}{
				"is_enabled": rule.IsEnabled,
				"sort_order": rule.SortOrder,
			})
		}
	}
	zap.L().Info("built-in review rules initialized", zap.Int("total", len(rules)))
}

// initDefaultProjectReviewConfigs 为所有现有项目生成默认规则配置
func initDefaultProjectReviewConfigs() {
	var projects []Project
	if err := DB.Find(&projects).Error; err != nil {
		zap.L().Warn("find projects for review config init failed", zap.Error(err))
		return
	}

	var count int
	for _, p := range projects {
		// 检查是否已有配置
		var cfgCount int64
		DB.Model(&ProjectReviewConfig{}).Where("project_id = ?", p.ID).Count(&cfgCount)
		if cfgCount > 0 {
			continue // 已有配置，跳过
		}

		// 查询该项目语言对应的通用规则 + 特定语言规则
		var rules []ReviewRule
		DB.Where("is_enabled = ? AND (language = 'common' OR language = ?)", true, p.Language).Find(&rules)

		for _, rule := range rules {
			config := ProjectReviewConfig{
				ProjectID: p.ID,
				RuleID:    rule.ID,
				IsEnabled: true,
				Severity:  "", // 使用规则默认级别
			}
			if err := DB.Create(&config).Error; err != nil {
				zap.L().Warn("init project review config failed",
					zap.Uint("project_id", p.ID),
					zap.Uint("rule_id", rule.ID),
					zap.Error(err))
			}
		}
		count++
	}
	zap.L().Info("default project review configs initialized", zap.Int("projects", count))
}

// initBuiltInReviewCategories 初始化内置评审维度
func initBuiltInReviewCategories() {
	categories := []ReviewCategory{
		{Code: "security", Name: "安全性", IsBuiltIn: true, SortOrder: 1},
		{Code: "performance", Name: "性能", IsBuiltIn: true, SortOrder: 2},
		{Code: "readability", Name: "可读性", IsBuiltIn: true, SortOrder: 3},
		{Code: "maintainability", Name: "可维护性", IsBuiltIn: true, SortOrder: 4},
		{Code: "test_coverage", Name: "测试覆盖", IsBuiltIn: true, SortOrder: 5},
	}
	for _, cat := range categories {
		var existing ReviewCategory
		if err := SilentFirst(DB.Where("code = ?", cat.Code), &existing); err != nil {
			if err := DB.Create(&cat).Error; err != nil {
				zap.L().Warn("init built-in category failed", zap.String("code", cat.Code), zap.Error(err))
			} else {
				zap.L().Info("init built-in category", zap.String("code", cat.Code))
			}
		} else {
			// 已存在则更新排序和名称（允许运行时微调显示名称）
			DB.Model(&existing).Updates(map[string]interface{}{
				"name":       cat.Name,
				"sort_order": cat.SortOrder,
				"is_built_in": true,
			})
		}
	}
	zap.L().Info("built-in review categories initialized", zap.Int("total", len(categories)))
}

// initSystemConfig 初始化系统配置默认记录，确保超时等配置有默认值
func initSystemConfig() {
	var cfg SystemConfig
	if err := SilentFirst(DB, &cfg); err != nil {
		cfg = SystemConfig{
			TaskTimeoutMin:          120,
			MaxParallelTask:         20,
			LogRetentionDay:         90,
			DiffTruncationThreshold: 5000,
			AlertDurationSec:        300,
			AlertCooldownSec:        3600,
			AlertNotifierID:         0,
			AlertMentionUserIDs:     "",
			JSONRetryMaxAttempts:    3,
			JSONRetryInitialDelaySec:   2,
			JSONRetryBackoffMultiplier: 2.0,
			JSONRetryMaxDelaySec:       30,
			JSONRetryFallbackStrategy:  "regex",
			DefaultDimensionWeights: `{"security":30,"code_quality":25,"readability":20,"maintainability":15,"test_coverage":10}`,
			AILogTemplate: "请先执行以下命令拉取代码：\ngit clone {{CLONE_URL}}\n\n变更摘要：\n{{MR_DIFF}}\n\n{{USER_INPUT}}\n\n请审查以上代码变更，给出审查意见。",
		}
		if err := DB.Create(&cfg).Error; err != nil {
			zap.L().Error("init system config failed", zap.Error(err))
		} else {
			zap.L().Info("system config initialized with defaults")
		}
	} else {
		zap.L().Info("system config already exists",
			zap.Int("task_timeout_min", cfg.TaskTimeoutMin),
			zap.Int("max_parallel_task", cfg.MaxParallelTask))
	}
}
