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

	// 为现有项目生成默认规则配置
	initDefaultProjectReviewConfigs()

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
		{Code: "common-sql-injection", Name: "SQL注入", Category: "security", Severity: "critical", Language: "common", Description: "检查拼接SQL、未参数化查询", Prompt: "检查是否存在SQL注入漏洞。重点关注：\n1. 字符串拼接SQL查询\n2. 使用fmt.Sprintf构建SQL\n3. 未使用预编译语句的参数化查询\n4. ORM的Raw方法传入变量", SortOrder: 1},
		{Code: "common-hardcoded-secret", Name: "硬编码密钥", Category: "security", Severity: "high", Language: "common", Description: "检查硬编码API密钥、密码、Token", Prompt: "检查是否存在硬编码的敏感信息：\n1. 字符串字面量中包含 'api_key', 'secret', 'password', 'token', 'private_key' 等关键词\n2. JWT签名密钥、数据库密码、云服务凭据\n3. 配置文件中明文存储的密钥\n4. 注释中泄露的敏感信息", SortOrder: 2},
		{Code: "common-xss-vulnerability", Name: "XSS漏洞", Category: "security", Severity: "high", Language: "common", Description: "检查未转义的用户输入输出", Prompt: "检查是否存在XSS漏洞：\n1. 用户输入未转义直接输出到HTML\n2. 使用innerHTML插入不可信内容\n3. URL参数直接反射到页面\n4. 前端模板中未使用安全插值", SortOrder: 3},
		{Code: "common-unsafe-deserialization", Name: "不安全的反序列化", Category: "security", Severity: "high", Language: "common", Description: "检查不安全的反序列化操作", Prompt: "检查是否存在不安全的反序列化：\n1. 反序列化不可信来源的数据\n2. 使用存在已知漏洞的序列化库\n3. 未对反序列化结果进行类型校验\n4. 使用pickle/反序列化执行不可信数据", SortOrder: 4},
		{Code: "common-resource-leak", Name: "资源泄露", Category: "performance", Severity: "high", Language: "common", Description: "检查文件、连接未关闭", Prompt: "检查是否存在资源泄露：\n1. 文件打开后未关闭\n2. 数据库连接未释放\n3. 网络连接未关闭\n4. 锁未释放\n5. 内存未回收（循环引用等）", SortOrder: 5},
		{Code: "common-n-plus-one-query", Name: "N+1查询", Category: "performance", Severity: "medium", Language: "common", Description: "检查循环体内数据库查询", Prompt: "检查是否存在N+1查询问题：\n1. 循环体内调用数据库查询\n2. ORM懒加载导致的隐式查询\n3. 批量操作未完成的地方\n4. 应该使用JOIN或IN查询的地方使用了多次查询", SortOrder: 6},
		{Code: "common-inefficient-loop", Name: "低效循环", Category: "performance", Severity: "medium", Language: "common", Description: "检查O(n²)、重复计算", Prompt: "检查是否存在低效循环：\n1. 循环嵌套导致O(n²)或更高复杂度\n2. 循环体内重复计算不变值\n3. 不必要的循环（可用集合操作替代）\n4. 递归未优化或可能导致栈溢出", SortOrder: 7},
		{Code: "common-magic-number", Name: "魔法数字", Category: "readability", Severity: "low", Language: "common", Description: "检查未命名的常量", Prompt: "检查是否存在魔法数字：\n1. 代码中直接使用的未命名数字常量\n2. 应该定义为具名常量的值\n3. 魔法字符串（重复出现的相同字符串字面量）", SortOrder: 8},
		{Code: "common-deep-nesting", Name: "嵌套过深", Category: "maintainability", Severity: "medium", Language: "common", Description: "检查if/for嵌套>4层", Prompt: "检查是否存在嵌套过深的问题：\n1. if/for/while嵌套超过4层\n2. 回调地狱（多层嵌套回调）\n3. 应该抽取为函数的复杂嵌套逻辑\n4. 使用早期返回减少嵌套", SortOrder: 9},
		{Code: "common-too-long-function", Name: "函数过长", Category: "maintainability", Severity: "medium", Language: "common", Description: "检查函数行数>100", Prompt: "检查是否存在函数过长的问题：\n1. 函数行数超过100行\n2. 函数职责不单一（应拆分为多个函数）\n3. 参数过多（超过5个）\n4. 圈复杂度过高", SortOrder: 10},

		// --- golang ---
		{Code: "go-error-handling", Name: "错误处理不当", Category: "maintainability", Severity: "medium", Language: "golang", Description: "检查未wrap错误或裸返回", Prompt: "检查Go代码的错误处理是否符合最佳实践：\n1. 错误返回时是否使用了fmt.Errorf(\"...: %w\", err)进行wrap\n2. 是否避免了只写 `if err != nil { return err }` 而未添加上下文\n3. 是否在错误路径上记录了足够的信息\n4. 是否避免了panic/recover的错误处理模式", SortOrder: 11},
		{Code: "go-context-propagation", Name: "Context未传递", Category: "maintainability", Severity: "medium", Language: "golang", Description: "检查context是否正确传递", Prompt: "检查Go代码的Context传递：\n1. 函数是否接收并传递了context.Context参数\n2. HTTP handlers是否正确使用request context\n3. 数据库操作是否传入了context\n4. 跨goroutine时context是否正确传播", SortOrder: 12},
		{Code: "go-goroutine-leak", Name: "Goroutine泄露", Category: "performance", Severity: "high", Language: "golang", Description: "检查goroutine未正确退出", Prompt: "检查是否存在Goroutine泄露：\n1. 启动的goroutine是否都有退出条件\n2. 是否使用了sync.WaitGroup正确等待\n3. channel是否可能阻塞导致goroutine无法退出\n4. 是否使用了context取消信号", SortOrder: 13},
		{Code: "go-interface-compliance", Name: "接口实现未显式校验", Category: "readability", Severity: "low", Language: "golang", Description: "检查接口实现是否显式声明", Prompt: "检查Go代码的接口实现：\n1. 是否使用了 `var _ Interface = (*Type)(nil)` 显式声明接口实现\n2. 接口定义是否清晰\n3. 接口方法数量是否合理（接口隔离原则）", SortOrder: 14},
		{Code: "go-concurrency-race", Name: "共享状态未保护", Category: "security", Severity: "high", Language: "golang", Description: "检查并发访问共享状态", Prompt: "检查Go代码的并发安全：\n1. 共享变量在多个goroutine中访问是否使用了sync.Mutex/RWMutex\n2. 是否使用了原子操作\n3. map在并发环境中是否安全\n4. 是否存在数据竞争", SortOrder: 15},
		{Code: "go-panic-recovery", Name: "不当使用panic", Category: "security", Severity: "high", Language: "golang", Description: "检查panic/recovery使用", Prompt: "检查Go代码中panic的使用：\n1. 是否在生产代码中不当使用panic\n2. 是否有必要的recover机制\n3. panic信息是否泄露了敏感信息\n4. 是否应该返回error而非panic", SortOrder: 16},
		{Code: "go-prepared-statement", Name: "未使用预编译", Category: "security", Severity: "medium", Language: "golang", Description: "检查数据库预编译语句", Prompt: "检查Go代码的数据库操作：\n1. SQL查询是否使用了参数化查询/预编译语句\n2. 是否使用fmt.Sprintf拼接SQL\n3. ORM查询是否安全", SortOrder: 17},
		{Code: "go-struct-tag", Name: "JSON tag格式错误", Category: "readability", Severity: "low", Language: "golang", Description: "检查struct tag格式", Prompt: "检查Go代码的struct tag：\n1. json tag是否使用正确的驼峰命名\n2. tag格式是否正确（无多余空格）\n3. omitempty使用是否恰当\n4. 是否遗漏了必要的tag", SortOrder: 18},
		{Code: "go-channel-close", Name: "Channel未正确关闭", Category: "performance", Severity: "medium", Language: "golang", Description: "检查channel关闭逻辑", Prompt: "检查Go代码的channel使用：\n1. channel是否由发送方正确关闭\n2. 是否向已关闭的channel发送数据\n3. 是否重复关闭channel\n4. select语句是否处理了所有case", SortOrder: 19},
		{Code: "go-nil-pointer", Name: "潜在空指针访问", Category: "security", Severity: "high", Language: "golang", Description: "检查nil指针解引用", Prompt: "检查Go代码中的nil指针风险：\n1. 接口值为nil但底层类型非nil的情况\n2. 函数返回值未检查直接使用\n3. map查找结果直接访问\n4. 类型断言未检查ok值", SortOrder: 20},
		{Code: "go-string-concat-loop", Name: "循环内字符串拼接", Category: "performance", Severity: "low", Language: "golang", Description: "检查循环中使用+拼接字符串", Prompt: "检查Go代码中的字符串拼接：\n1. 循环内是否使用了+拼接字符串（应使用strings.Builder）\n2. 大量字符串拼接是否使用了bytes.Buffer\n3. 格式化字符串是否使用了fmt.Sprintf（性能敏感场景）", SortOrder: 21},
		{Code: "go-defer-in-loop", Name: "循环内使用defer", Category: "performance", Severity: "medium", Language: "golang", Description: "检查循环内defer资源泄漏", Prompt: "检查Go代码中defer的使用：\n1. 循环内使用defer可能导致资源延迟释放\n2. defer是否包裹了不应该defer的操作\n3. defer的执行顺序是否正确", SortOrder: 22},

		// --- python ---
		{Code: "py-bare-except", Name: "裸except", Category: "maintainability", Severity: "medium", Language: "python", Description: "检查except未指定异常类型", Prompt: "检查Python代码的异常处理：\n1. 是否使用了裸except（应指定异常类型）\n2. except Exception是否过度宽泛\n3. 是否捕获了异常但未处理\n4. finally块是否正确使用", SortOrder: 23},
		{Code: "py-mutable-default-arg", Name: "可变默认参数", Category: "security", Severity: "high", Language: "python", Description: "检查可变的默认参数值", Prompt: "检查Python函数默认参数：\n1. 是否使用了可变对象（list/dict）作为默认参数\n2. 默认参数是否在多次调用间共享状态\n3. 应使用None作为默认值并在函数内初始化", SortOrder: 24},
		{Code: "py-type-hint-missing", Name: "缺少类型注解", Category: "readability", Severity: "low", Language: "python", Description: "检查函数缺少类型注解", Prompt: "检查Python代码的类型注解：\n1. 函数参数是否缺少类型注解\n2. 返回值类型是否标注\n3. 复杂类型是否使用了typing模块\n4. 类型注解是否准确", SortOrder: 25},
		{Code: "py-sql-string-format", Name: "SQL字符串格式化", Category: "security", Severity: "critical", Language: "python", Description: "检查字符串格式化SQL", Prompt: "检查Python代码的SQL拼接：\n1. 是否使用字符串格式化（%, f-string, .format）拼接SQL\n2. 是否使用了参数化查询\n3. ORM查询是否安全\n4. 存储过程调用是否参数化", SortOrder: 26},
		{Code: "py-global-mutable", Name: "全局可变对象滥用", Category: "maintainability", Severity: "medium", Language: "python", Description: "检查全局可变状态", Prompt: "检查Python代码的全局状态：\n1. 全局可变对象是否被多个函数修改\n2. 单例模式实现是否线程安全\n3. 模块级变量是否被意外修改\n4. 应使用函数参数传递而非全局状态", SortOrder: 27},
		{Code: "py-eval-exec", Name: "使用eval/exec", Category: "security", Severity: "critical", Language: "python", Description: "检查危险的内置函数使用", Prompt: "检查Python代码中的危险函数：\n1. 是否使用了eval()或exec()处理用户输入\n2. 是否使用了compile() + exec\n3. subprocess或os.system是否拼接了用户输入\n4. 模板引擎中是否存在SSTI（服务器端模板注入）", SortOrder: 28},

		// --- frontend ---
		{Code: "frontend-xss-innerHTML", Name: "直接插入innerHTML", Category: "security", Severity: "high", Language: "frontend", Description: "检查危险的DOM操作", Prompt: "检查前端代码的DOM操作：\n1. 是否直接使用innerHTML插入不可信内容\n2. 是否使用了document.write\n3. 是否对URL参数未过滤直接渲染\n4. React中是否使用了dangerouslySetInnerHTML", SortOrder: 29},
		{Code: "frontend-memory-leak", Name: "未清理事件监听", Category: "performance", Severity: "medium", Language: "frontend", Description: "检查组件卸载未清理", Prompt: "检查前端代码的内存管理：\n1. 组件卸载时是否清理了事件监听\n2. 定时器/setInterval是否在unmount时清除\n3. 订阅是否在销毁时取消\n4. 闭包是否持有过期引用", SortOrder: 30},
		{Code: "frontend-callback-hell", Name: "回调地狱", Category: "readability", Severity: "low", Language: "frontend", Description: "检查嵌套回调", Prompt: "检查前端代码的异步处理：\n1. 是否存在多层嵌套回调（回调地狱）\n2. 是否使用了Promise/async-await替代\n3. Promise链是否过长\n4. 错误处理是否完善", SortOrder: 31},
		{Code: "react-missing-key", Name: "列表缺少key", Category: "performance", Severity: "low", Language: "frontend", Description: "检查React列表渲染", Prompt: "检查React代码的列表渲染：\n1. map遍历是否提供了key属性\n2. key是否使用了稳定的唯一标识\n3. 是否使用了index作为key（不推荐）\n4. 列表项重排时key是否正确", SortOrder: 32},
		{Code: "vue-mutate-prop", Name: "直接修改props", Category: "maintainability", Severity: "medium", Language: "frontend", Description: "检查Vue props修改", Prompt: "检查Vue代码的props使用：\n1. 是否直接修改了props值\n2. 是否通过emit通知父组件更新\n3. 是否使用了computed处理派生状态\n4. 是否使用了v-model错误地修改prop", SortOrder: 33},
		{Code: "frontend-cors-misconfig", Name: "CORS配置过于宽松", Category: "security", Severity: "high", Language: "frontend", Description: "检查CORS配置", Prompt: "检查前端/后端CORS配置：\n1. 是否允许了所有来源（*）\n2. 是否允许了危险的方法（PUT/DELETE）\n3. 是否暴露了敏感Header\n4. credentials配置是否正确", SortOrder: 34},
		{Code: "frontend-hardcoded-api-key", Name: "前端硬编码API Key", Category: "security", Severity: "critical", Language: "frontend", Description: "检查前端泄露密钥", Prompt: "检查前端代码的密钥管理：\n1. 是否在前端代码中硬编码了API Key\n2. 是否将密钥提交到版本控制\n3. 环境变量是否正确使用\n4. 构建配置中是否泄露了敏感信息", SortOrder: 35},

		// --- java ---
		{Code: "java-null-pointer", Name: "NPE潜在风险", Category: "security", Severity: "high", Language: "java", Description: "检查空指针风险", Prompt: "检查Java代码的NPE风险：\n1. Optional使用是否恰当\n2. 链式调用是否检查了中间null值\n3. 方法参数是否进行了null校验\n4. 集合操作是否检查了空值", SortOrder: 36},
		{Code: "java-resource-leak", Name: "未用try-with-resources", Category: "performance", Severity: "medium", Language: "java", Description: "检查资源关闭", Prompt: "检查Java代码的资源管理：\n1. 是否使用了try-with-resources\n2. Closeable资源是否在finally中关闭\n3. 数据库连接是否及时归还\n4. 文件流是否正确关闭", SortOrder: 37},
		{Code: "java-concurrent-modification", Name: "并发修改异常", Category: "security", Severity: "high", Language: "java", Description: "检查集合并发修改", Prompt: "检查Java代码的并发安全：\n1. 是否在迭代时修改了集合\n2. 并发环境是否使用了线程安全的集合\n3. 是否使用了CopyOnWriteArrayList等并发集合\n4. synchronized使用是否正确", SortOrder: 38},
		{Code: "java-string-concat-loop", Name: "循环内String拼接", Category: "performance", Severity: "medium", Language: "java", Description: "检查低效字符串操作", Prompt: "检查Java代码的字符串操作：\n1. 循环内是否使用了+拼接String\n2. 是否使用了StringBuilder/StringBuffer\n3. 大量字符串拼接的场景优化", SortOrder: 39},
		{Code: "java-raw-type", Name: "使用泛型原始类型", Category: "maintainability", Severity: "low", Language: "java", Description: "检查泛型使用", Prompt: "检查Java代码的泛型使用：\n1. 是否使用了原始类型（raw type）\n2. 泛型参数是否完整声明\n3. @SuppressWarnings是否必要\n4. 类型转换是否安全", SortOrder: 40},
		{Code: "java-transactional-misuse", Name: "事务注解使用不当", Category: "security", Severity: "high", Language: "java", Description: "检查@Transactional使用", Prompt: "检查Java代码的事务管理：\n1. @Transactional是否在public方法上\n2. 事务传播行为是否恰当\n3. 事务边界是否合理\n4. 异常回滚配置是否正确", SortOrder: 41},
		{Code: "java-magic-number", Name: "魔法数字", Category: "readability", Severity: "low", Language: "java", Description: "检查未命名常量", Prompt: "检查Java代码的常量使用：\n1. 是否存在魔法数字\n2. 是否使用了static final常量\n3. 枚举类型使用是否恰当\n4. 配置值是否提取到配置文件", SortOrder: 42},
		{Code: "java-singleton-race", Name: "单例模式并发问题", Category: "security", Severity: "high", Language: "java", Description: "检查单例实现线程安全", Prompt: "检查Java代码的单例模式：\n1. 懒汉式单例是否线程安全\n2. 双重检查锁定是否正确\n3. 枚举单例是否被使用\n4. 单例状态是否被共享修改", SortOrder: 43},
	}

	for _, rule := range rules {
		var existing ReviewRule
		if err := DB.Where("code = ?", rule.Code).First(&existing).Error; err != nil {
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
