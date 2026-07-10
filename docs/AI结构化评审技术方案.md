# CodeGuard - AI 结构化评审技术方案

> 版本: v1.0  
> 更新日期: 2026-07-10  
> 状态: 需求分析与技术方案设计  
> 后续阶段: 数据库 Migration → 后端开发 → 前端开发 → 测试验收

---

## 目录

1. [需求背景与目标](#1-需求背景与目标)
2. [核心架构设计](#2-核心架构设计)
3. [数据库表设计](#3-数据库表设计)
4. [接口设计](#4-接口设计)
5. [前端布局方案](#5-前端布局方案)
6. [Prompt 工程方案](#6-prompt-工程方案)
7. [JSON Schema 与结构化输出](#7-json-schema-与结构化输出)
8. [重试与降级策略](#8-重试与降级策略)
9. [GitLab 评论模板系统](#9-gitlab-评论模板系统)
10. [ Migration 与兼容性策略](#10-migration-与兼容性策略)
11. [实施里程碑](#11-实施里程碑)

---

## 1. 需求背景与目标

### 1.1 现状痛点

当前 AI 评审返回纯文本，通过正则 `"AI评分：(\d+)分"` 提取分数，存在以下问题：

- **格式不稳定**：大模型偶发不按要求输出，导致分数提取失败
- **数据不可查**：总分、维度、问题列表、建议全部揉在一段文本里，无法按严重程度/文件筛选
- **无法做行内评论**：缺少文件路径和行号等结构化信息
- **模板过于自由**：用户自行编写整段 prompt，不同项目输出格式不一致

### 1.2 目标

实现 AI 评审的结构化输出，达成以下目标：

1. **结构化评分**：多维度评分（安全性、代码质量、可读性、可维护性、测试覆盖），总分自动计算
2. **issue 明细**：每条 issue 含文件路径、行号、严重级别、问题描述、改进建议
3. **规则化评审**：不再支持用户编写整段 prompt，改为勾选系统内置评审规则（按语言分类）
4. **Markdown 评论可定制**：后端从结构化数据组装 Markdown 评论，支持模板变量
5. **可扩展行内评论**：issue 表已经预留 GitLab discussion ID 字段，后续实现行内评论时无需改表
6. **OpenCode 深度评审不变**：`task_type='chat'` 保持现有纯文本流式输出逻辑

### 1.3 范围边界

| 涉及 | 不涉及 |
|------|--------|
| `task_type='review'`（AI 自动评审） | `task_type='chat'`（OpenCode 深度评审） |
| OpenAI 兼容格式（OpenAI / DeepSeek / Azure / VLLM） | Anthropic Claude |
| 新数据走结构化链路 | 历史纯文本数据保持兼容展示 |

---

## 2. 核心架构设计

### 2.1 数据流全景

```
GitLab Webhook / Manual Trigger
  │
  ▼
┌─────────────────────────────────────────────────────────────────┐
│  ① ProjectResolver                                            │
│     - 读取项目配置（模板 ID、语言、模型 ID）                    │
│     - 根据语言筛选 ReviewRule（内置 common + 对应语言）       │
│     - 读取 ProjectReviewConfig（启用/禁用/覆盖严重级别）       │
└─────────────────────────────────────────────────────────────────┘
  │
  ▼
┌─────────────────────────────────────────────────────────────────┐
│  ② DimensionConfigLoader                                      │
│     - 读取模板维度权重（ProjectTemplate.DimensionWeights）      │
│     - 或使用系统默认权重                                        │
└─────────────────────────────────────────────────────────────────┘
  │
  ▼
┌─────────────────────────────────────────────────────────────────┐
│  ③ TemplatePromptBuilder                                      │
│     ├─ System: "你是资深代码审查专家，输出严格按 JSON Schema"   │
│     ├─ 项目自定义说明（Template.CustomInstruction）             │
│     ├─ 维度权重说明                                             │
│     ├─ 已启用规则列表（按 Category 分组，最多 N 条，可配置）      │
│     ├─ Diff + Commits + MR Title                              │
│     └─ Output: JSON Schema + 输出约束                           │
└─────────────────────────────────────────────────────────────────┘
  │
  ▼ (单次 or 分批 → Summary)
┌─────────────────────────────────────────────────────────────────┐
│  ④ LLM Client (ChatCompletion)                                │
│     ├─ Request.ResponseFormat = json_schema (strict=true)       │
│     ├─ Schema 由 invopop/jsonschema 从 Go struct 反射生成      │
│     ├─ 走主备链路（主模型 → 备用1 → 备用2）                  │
│     └─ VLLM fallback：json_object 或 prompt-level               │
└─────────────────────────────────────────────────────────────────┘
  │
  ▼
┌─────────────────────────────────────────────────────────────────┐
│  ⑤ ResponseParser                                             │
│     ├─ Refusal 检测                                             │
│     ├─ JSON Unmarshal → AIReviewResult                          │
│     ├─ 失败 → Sanitize（去代码块标记等）                        │
│     ├─ 仍失败 → Retry（指数退避，最多 N 次）                    │
│     └─ 达上限 → Fallback（正则/保留原始文本/失败）              │
└─────────────────────────────────────────────────────────────────┘
  │
  ▼
┌─────────────────────────────────────────────────────────────────┐
│  ⑥ DataPersistor                                              │
│     ├─ Task.AIResponseJSON = reviewResult JSON                  │
│     ├─ Task.DimensionScores = dimensions JSON                   │
│     ├─ Task.IssueCount = len(issues)                            │
│     ├─ Task.ScoreValue = reviewResult.TotalScore               │
│     ├─ INSERT INTO review_issues（每 issue 一行）               │
│     └─ Task.AIResponse = ""（等待组装器填充）                   │
└─────────────────────────────────────────────────────────────────┘
  │
  ▼
┌─────────────────────────────────────────────────────────────────┐
│  ⑦ MarkdownCommentAssembler（text/template 引擎）              │
│     ├─ 读取 GitLab Comment Template                             │
│     │   优先级：项目级 > 系统级 > 内置默认                       │
│     ├─ 组装 CommentTemplateContext                              │
│     ├─ 预渲染辅助字段（DimensionsTable, IssuesList 等）         │
│     └─ 渲染 → Markdown String                                   │
└─────────────────────────────────────────────────────────────────┘
  │
  ▼
┌─────────────────────────────────────────────────────────────────┐
│  ⑧ GitLab API                                                  │
│     └─ POST /projects/{id}/merge_requests/{iid}/notes          │
│        发送 Markdown 评论                                       │
└─────────────────────────────────────────────────────────────────┘
  │
  ▼
┌─────────────────────────────────────────────────────────────────┐
│  ⑨ ThresholdTrigger                                            │
│     └─ if ScoreValue < SystemConfig.ScoreThreshold              │
│        → Queue OpenCode Deep Review（task_type='chat'）         │
│        （完全保留现有逻辑）                                     │
└─────────────────────────────────────────────────────────────────┘
```

### 2.2 分层架构

```
┌─────────────────────────────────────────┐
│           路由层 (Handler)               │
│  /admin/review-rules, /projects/:id/... │
├─────────────────────────────────────────┤
│           服务层 (Service)               │
│  ReviewRuleService, ProjectReviewService│
│  TemplatePromptBuilder, CommentAssembler │
├─────────────────────────────────────────┤
│           核心引擎 (Engine)              │
│  ResponseParser, MarkdownAssembler      │
├─────────────────────────────────────────┤
│           客户端层 (Client)              │
│  LLM Client (+json_schema), GitLab API  │
├─────────────────────────────────────────┤
│           数据层 (Model)                 │
│  review_rules, review_issues, ...       │
└─────────────────────────────────────────┘
```

---

## 3. 数据库表设计

### 3.1 新建表

#### 3.1.1 `review_rules` — 评审规则库

```sql
CREATE TABLE review_rules (
    id              BIGINT UNSIGNED PRIMARY KEY AUTO_INCREMENT,
    code            VARCHAR(64) NOT NULL COMMENT '唯一编码，如 go-error-handling',
    name            VARCHAR(100) NOT NULL COMMENT '显示名称',
    category        VARCHAR(32) NOT NULL COMMENT '维度：security/performance/readability/maintainability/test_coverage',
    severity        VARCHAR(16) NOT NULL COMMENT '默认严重级别：critical/high/medium/low/info',
    language        VARCHAR(32) NOT NULL DEFAULT 'common' COMMENT '编程语言：golang/python/frontend/java/common',
    description     TEXT COMMENT '规则说明（给用户看）',
    prompt          TEXT NOT NULL COMMENT '让 LLM 执行检查的 prompt 片段',
    sort_order      INT NOT NULL DEFAULT 0 COMMENT '排序权重',
    is_built_in     BOOLEAN NOT NULL DEFAULT TRUE COMMENT '是否平台内置',
    is_enabled      BOOLEAN NOT NULL DEFAULT TRUE COMMENT '全局默认启用',
    created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    
    UNIQUE INDEX idx_code (code),
    INDEX idx_category (category),
    INDEX idx_language (language),
    INDEX idx_is_enabled (is_enabled)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='AI 评审规则库';
```

#### 3.1.2 `project_review_configs` — 项目规则配置

```sql
CREATE TABLE project_review_configs (
    id              BIGINT UNSIGNED PRIMARY KEY AUTO_INCREMENT,
    project_id      BIGINT UNSIGNED NOT NULL COMMENT '关联 projects.id',
    rule_id         BIGINT UNSIGNED NOT NULL COMMENT '关联 review_rules.id',
    is_enabled      BOOLEAN NOT NULL DEFAULT TRUE COMMENT '本项目是否启用此规则',
    severity        VARCHAR(16) COMMENT '项目覆盖的严重级别（NULL=使用默认值）',
    created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    updated_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    
    UNIQUE INDEX idx_project_rule (project_id, rule_id),
    INDEX idx_project_id (project_id),
    INDEX idx_rule_id (rule_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='项目评审规则启用配置';
```

#### 3.1.3 `review_issues` — 结构化评审结果明细

```sql
CREATE TABLE review_issues (
    id                      BIGINT UNSIGNED PRIMARY KEY AUTO_INCREMENT,
    task_id                 BIGINT UNSIGNED NOT NULL COMMENT '关联 tasks.id',
    rule_id                 BIGINT UNSIGNED COMMENT '关联 review_rules.id（NULL=AI自主发现）',
    rule_code               VARCHAR(64) COMMENT '冗余存储规则编码，方便查询',
    category                VARCHAR(32) COMMENT '分类（冗余，方便按维度统计）',
    severity                VARCHAR(16) NOT NULL COMMENT '严重级别',
    file                    VARCHAR(255) COMMENT '文件路径',
    line_start              INT NOT NULL DEFAULT 0 COMMENT '起始行号',
    line_end                INT NOT NULL DEFAULT 0 COMMENT '结束行号',
    code_snippet            TEXT COMMENT '相关代码片段',
    message                 TEXT NOT NULL COMMENT '问题描述',
    suggestion              TEXT COMMENT '改进建议',
    gitlab_discussion_id    VARCHAR(64) COMMENT 'GitLab Discussion ID（行内评论预留）',
    is_resolved             BOOLEAN NOT NULL DEFAULT FALSE COMMENT '是否已解决（行内评论预留）',
    created_at              DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
    
    INDEX idx_task_id (task_id),
    INDEX idx_rule_id (rule_id),
    INDEX idx_severity (severity),
    INDEX idx_file (file),
    INDEX idx_line_start (line_start)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='AI 评审结构化 issue 明细';
```

### 3.2 改造表

#### 3.2.1 `projects` — 新增语言字段

```sql
ALTER TABLE projects
ADD COLUMN language VARCHAR(32) NOT NULL DEFAULT 'golang' COMMENT '项目主要编程语言：golang/python/frontend/java',
ADD INDEX idx_language (language);
```

#### 3.2.2 `project_templates` — 重构为配置驱动

```sql
-- 保留旧字段兼容运行（一个版本后删除）
-- ALTER TABLE project_templates ADD COLUMN prompt_legacy TEXT COMMENT '旧版自定义 prompt（废弃）';

ALTER TABLE project_templates
ADD COLUMN description            VARCHAR(255) COMMENT '模板描述',
ADD COLUMN custom_instruction     TEXT COMMENT '项目特殊说明（如"支付核心模块"）',
ADD COLUMN dimension_weights      JSON COMMENT '维度权重配置，如 {"security":30,"code_quality":25,...}',
ADD COLUMN max_rules_per_review   INT NOT NULL DEFAULT 5 COMMENT '每次评审最多输出规则数',
ADD COLUMN is_default             BOOLEAN NOT NULL DEFAULT FALSE COMMENT '是否为系统默认模板',
ADD COLUMN gitlab_comment_template TEXT COMMENT 'GitLab 评论模板（支持 text/template 变量）';
```

> **dimension_weights JSON 结构示例：**
> ```json
> {
>   "security": {"weight": 30, "label": "安全性"},
>   "code_quality": {"weight": 25, "label": "代码质量"},
>   "readability": {"weight": 20, "label": "可读性"},
>   "maintainability": {"weight": 15, "label": "可维护性"},
>   "test_coverage": {"weight": 10, "label": "测试覆盖"}
> }
> ```

#### 3.2.3 `tasks` — 增加结构化输出字段

```sql
ALTER TABLE tasks
ADD COLUMN ai_response_json    JSON COMMENT 'AI 结构化评审原始 JSON',
ADD COLUMN dimension_scores    JSON COMMENT '各维度评分结果 JSON',
ADD COLUMN issue_count         INT NOT NULL DEFAULT 0 COMMENT '本次评审 issue 总数';
```

> **dimension_scores JSON 结构示例：**
> ```json
> {
>   "security": 95,
>   "code_quality": 80,
>   "readability": 75,
>   "maintainability": 85,
>   "test_coverage": 90
> }
> ```

#### 3.2.4 `system_configs` — 增加 JSON 重试配置

```sql
ALTER TABLE system_configs
ADD COLUMN json_retry_max_attempts       INT NOT NULL DEFAULT 3 COMMENT 'JSON 解析失败最大重试次数',
ADD COLUMN json_retry_initial_delay_sec  INT NOT NULL DEFAULT 2 COMMENT '重试初始延迟（秒）',
ADD COLUMN json_retry_backoff_multiplier FLOAT NOT NULL DEFAULT 2.0 COMMENT '退避乘数',
ADD COLUMN json_retry_max_delay_sec      INT NOT NULL DEFAULT 30 COMMENT '最大延迟（秒）',
ADD COLUMN json_retry_fallback_strategy  VARCHAR(20) NOT NULL DEFAULT 'regex' COMMENT 'fallback 策略：regex/markdown/fail',
ADD COLUMN default_dimension_weights     JSON COMMENT '默认维度权重（项目未配置时使用）',
ADD COLUMN default_gitlab_comment_template TEXT COMMENT '默认 GitLab 评论模板';
```

### 3.3 数据迁移脚本

```sql
-- Migrate 1: 初始化内置规则（共约 35-40 条）
-- 由 Go migration 脚本在程序启动时 INSERT IGNORE

-- Migrate 2: 为所有现有项目创建默认规则配置
INSERT INTO project_review_configs (project_id, rule_id, is_enabled)
SELECT p.id, r.id, TRUE
FROM projects p
CROSS JOIN review_rules r
WHERE r.is_enabled = TRUE AND r.language IN ('common', p.language);

-- Migrate 3: 为现有模板迁移旧 prompt
-- UPDATE project_templates SET prompt_legacy = prompt WHERE prompt IS NOT NULL;
-- UPDATE project_templates SET custom_instruction = SUBSTRING(prompt, 1, 500) WHERE prompt IS NOT NULL;
```

---

## 4. 接口设计

### 4.1 评审规则管理（管理员）

#### 4.1.1 获取规则库列表

```
GET /api/v1/admin/review-rules
```

**Query Parameters：**
- `category` (可选)：按维度过滤
- `language` (可选)：按语言过滤
- `is_enabled` (可选)：按启用状态过滤
- `keyword` (可选)：按 name/code 搜索

**Response (200)：**
```json
{
  "code": 0,
  "data": [
    {
      "id": 1,
      "code": "go-error-handling",
      "name": "错误处理不当",
      "category": "maintainability",
      "severity": "medium",
      "language": "golang",
      "description": "检查是否正确使用 errors.Wrap 或 fmt.Errorf",
      "is_built_in": true,
      "is_enabled": true,
      "created_at": "2026-07-10T10:00:00Z"
    }
  ],
  "total": 42
}
```

#### 4.1.2 更新规则启用状态（批量）

```
PUT /api/v1/admin/review-rules/batch-enable
```

**Request Body：**
```json
{
  "rule_ids": [1, 2, 3],
  "is_enabled": false
}
```

#### 4.1.3 编辑自定义规则（仅用户创建的非内置规则）

```
PUT /api/v1/admin/review-rules/:id
```

**Request Body：**
```json
{
  "name": "自定义规则",
  "category": "security",
  "severity": "high",
  "description": "...",
  "prompt": "..."
}
```

> **约束：** `is_built_in=true` 的规则只能改 `is_enabled`，不能改 `name`/`prompt`。

#### 4.1.4 创建自定义规则

```
POST /api/v1/admin/review-rules
```

**Request Body：**
```json
{
  "code": "custom-rule-001",
  "name": "自定义检查",
  "category": "security",
  "severity": "high",
  "language": "golang",
  "description": "...",
  "prompt": "..."
}
```

#### 4.1.5 获取按维度/语言分组的规则树

```
GET /api/v1/admin/review-rules/tree
```

**Response (200)：**
```json
{
  "code": 0,
  "data": {
    "golang": {
      "security": [{"id": 1, "name": "SQL注入", "severity": "critical"}],
      "performance": [{"id": 2, "name": "N+1查询", "severity": "medium"}]
    },
    "common": {
      "security": [{"id": 3, "name": "硬编码密钥", "severity": "high"}]
    }
  }
}
```

### 4.2 项目规则配置

#### 4.2.1 获取项目已配置规则

```
GET /api/v1/projects/:id/review-rules
```

**Response (200)：**
```json
{
  "code": 0,
  "data": {
    "project_id": 5,
    "language": "golang",
    "rules": [
      {
        "rule_id": 1,
        "code": "go-error-handling",
        "name": "错误处理不当",
        "category": "maintainability",
        "is_enabled": true,
        "default_severity": "medium",
        "project_severity": null
      },
      {
        "rule_id": 2,
        "code": "go-goroutine-leak",
        "name": "Goroutine 泄露",
        "category": "performance",
        "is_enabled": false,
        "default_severity": "high",
        "project_severity": "critical"
      }
    ]
  }
}
```

#### 4.2.2 批量更新项目规则配置

```
PUT /api/v1/projects/:id/review-rules
```

**Request Body：**
```json
{
  "rules": [
    {"rule_id": 1, "is_enabled": true, "severity": null},
    {"rule_id": 2, "is_enabled": false, "severity": "critical"}
  ]
}
```

#### 4.2.3 重置为默认规则配置

```
POST /api/v1/projects/:id/review-rules/reset
```

> 删除该项目的所有自定义配置，重新生成默认配置（common + 对应语言的全部启用规则）。

### 4.3 项目模板管理

#### 4.3.1 获取模板详情（含规则配置）

```
GET /api/v1/admin/project-templates/:id
```

**Response (200)：**
```json
{
  "code": 0,
  "data": {
    "id": 1,
    "name": "标准 Go 项目模板",
    "description": "适用于标准后端 Go 项目",
    "custom_instruction": "本项目使用 Go 1.26，请重点关注并发安全",
    "dimension_weights": {
      "security": {"weight": 30, "label": "安全性"},
      "code_quality": {"weight": 25, "label": "代码质量"},
      "readability": {"weight": 20, "label": "可读性"},
      "maintainability": {"weight": 15, "label": "可维护性"},
      "test_coverage": {"weight": 10, "label": "测试覆盖"}
    },
    "max_rules_per_review": 5,
    "is_default": true,
    "gitlab_comment_template": "...",
    "created_at": "..."
  }
}
```

#### 4.3.2 创建/更新模板

```
POST /api/v1/admin/project-templates
PUT /api/v1/admin/project-templates/:id
```

**Request Body：**
```json
{
  "name": "安全敏感项目模板",
  "description": "适用于支付/金融类项目",
  "custom_instruction": "支付核心模块，安全性要求极高",
  "dimension_weights": {
    "security": {"weight": 40, "label": "安全性"},
    "code_quality": {"weight": 25, "label": "代码质量"},
    "readability": {"weight": 15, "label": "可读性"},
    "maintainability": {"weight": 10, "label": "可维护性"},
    "test_coverage": {"weight": 10, "label": "测试覆盖"}
  },
  "max_rules_per_review": 5,
  "gitlab_comment_template": "..."
}
```

### 4.4 结构化评审结果查询

#### 4.4.1 获取 Task 结构化评审结果

```
GET /api/v1/tasks/:id/structured-review
```

**Response (200)：**
```json
{
  "code": 0,
  "data": {
    "total_score": 85,
    "dimensions": {
      "security": 95,
      "code_quality": 80,
      "readability": 75,
      "maintainability": 85,
      "test_coverage": 90
    },
    "summary": "本次MR整体质量良好...",
    "issues": [
      {
        "id": 1,
        "rule_code": "security-hardcoded-secret",
        "category": "security",
        "severity": "high",
        "file": "pkg/service/auth.go",
        "line_start": 45,
        "line_end": 52,
        "code_snippet": "const API_KEY = \"sk-1234567890abcdef\"",
        "message": "此处使用了硬编码密钥",
        "suggestion": "建议改从环境变量读取"
      }
    ],
    "recommendations": ["建议增加单元测试"],
    "markdown_comment": "## 🤖 AI 代码评审报告\n\n**综合评分：85/100**..."
  }
}
```

#### 4.4.2 按文件/严重级别筛选 issue

```
GET /api/v1/tasks/:id/issues?severity=high&file=pkg/service/auth.go
```

**Response (200)：**
```json
{
  "code": 0,
  "data": [
    {
      "id": 1,
      "severity": "high",
      "file": "pkg/service/auth.go",
      "line_start": 45,
      "message": "此处使用了硬编码密钥"
    }
  ],
  "total": 1
}
```

### 4.5 系统配置（扩展）

#### 4.5.1 更新 JSON 重试配置

```
PUT /api/v1/admin/system-config/json-retry
```

**Request Body：**
```json
{
  "json_retry_max_attempts": 3,
  "json_retry_initial_delay_sec": 2,
  "json_retry_backoff_multiplier": 2.0,
  "json_retry_max_delay_sec": 30,
  "json_retry_fallback_strategy": "regex"
}
```

### 4.6 系统管理

#### 4.6.1 预览模板渲染效果

```
POST /api/v1/admin/templates/:id/preview-comment
```

**Request Body：**
```json
{
  "total_score": 85,
  "dimensions": {"security": 95, "code_quality": 80},
  "issues": [
    {"severity": "high", "rule_code": "xxx", "message": "测试消息", "file": "test.go", "line_start": 10}
  ]
}
```

**Response：** 渲染后的 Markdown 字符串

---

## 5. 前端布局方案

### 5.1 新增/改造页面总览

| 页面 | 新建/改造 | 说明 |
|------|----------|------|
| `review-rules.html` | **新建** | 评审规则库管理（admin 专属） |
| `project-review-rules.html` | **新建** | 项目级规则配置 |
| `project-templates.html` | 改造 | 模板管理（从自由文本改为配置化） |
| `task-detail.html` | 改造 | 任务详情展示结构化 issue |
| `settings.html` | 改造 | 增加 JSON 重试配置 |

### 5.2 评审规则库管理页（`review-rules.html`）

**路径：** `/review-rules.html`（admin only）

**布局：**

```
┌─────────────────────────────────────────────────────────────┐
│  头部：CodeGuard Logo + 导航栏                               │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  页面标题：AI 评审规则库          [+ 创建自定义规则]         │
│                                                             │
│  ┌─────────────────────────────────────────────────────┐   │
│  │ 搜索框 │ 语言筛选 [全部▼] │ 维度筛选 [全部▼] │ 状态 ▼│   │
│  └─────────────────────────────────────────────────────┘   │
│                                                             │
│  ┌─────────────────────────────────────────────────────────┐│
│  │ 规则卡片列表（Grid 布局，每行 3-4 张卡片）               ││
│  │                                                         ││
│  │ ┌─────────────┐ ┌─────────────┐ ┌─────────────┐        ││
│  │ │ 🔒 SQL注入   │ │ 🚀 N+1查询  │ │ 📖 错误处理  │        ││
│  │ │ critical    │ │ medium      │ │ medium      │        ││
│  │ │ [Go] [通用]  │ │ [通用]      │ │ [Go]        │        ││
│  │ │ 检查...     │ │ 检查...     │ │ 检查...     │        ││
│  │ │ [开关]      │ │ [开关]      │ │ [开关]      │        ││
│  │ └─────────────┘ └─────────────┘ └─────────────┘        ││
│  │                                                         ││
│  │ 分页                                                      ││
│  └─────────────────────────────────────────────────────────┘│
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

**卡片元素：**
- 左侧彩色边框：按 category 着色（security=红色, performance=橙色, readability=蓝色, maintainability=绿色, test_coverage=紫色）
- 严重级别徽章：`critical`(🔴) / `high`(🟠) / `medium`(🟡) / `low`(🔵) / `info`(⚪)
- 语言标签：`Go` / `Python` / `前端` / `Java` / `通用`
- 内置/自定义标识：内置规则显示 "🏛️ 内置" 标签，不可编辑，只能开关
- 开关：Toggle 控制 `is_enabled`
- 点击卡片 → 展开详情 drawer，展示 `description` 和 `prompt`（只读）

**创建自定义规则 Drawer：**
```
┌────────────────────────────┐
│ 创建自定义规则        [×]  │
├────────────────────────────┤
│ 规则编码 *                 │
│ [________________]         │
│ 规则名称 *                 │
│ [________________]         │
│ 所属维度 *                 │
│ [安全性 ▼]                │
│ 编程语言 *                 │
│ [通用 ▼]                  │
│ 严重级别 *                 │
│ [高危 ▼]                  │
│ 规则说明                   │
│ [                        ] │
│ [                        ] │
│ Prompt 片段 *              │
│ [                        ] │
│ [                        ] │
│ [      取消  ] [ 创建 ]    │
└────────────────────────────┘
```

### 5.3 项目规则配置页（`project-review-rules.html`）

**路径：** 从项目管理页 → 点击项目名称 → 选择 "评审规则" Tab

**布局（Desktop 双栏）：**

```
┌─────────────────────────────────────────────────────────────┐
│  CodeGuard > 项目管理 > my-api-project > 评审规则           │
├──────────────────────────┬──────────────────────────────────┤
│                          │                                  │
│  ① 左侧：维度分组        │  ② 右侧：规则详情编辑区           │
│                          │                                  │
│  + 安全性 (5)            │  ┌──────────────────────────────┐│
│    - SQL注入         ✓   │  │ hardcoded-secret             ││
│    - XSS漏洞         ✗   │  │ 硬编码密钥                    ││
│    - 硬编码密钥      ✓   │  │                                ││
│  + 性能 (3)              │  │ 严重级别                       ││
│    - N+1查询        ✓    │  │ 系统：高危  项目：[高危 ▼]   ││
│    - ...                 │  │                                ││
│  + 可读性 (4)            │  │ 规则说明                       ││
│  + 可维护性 (6)          │  │ [使用 eval/exec 会导致任意...] ││
│  + 测试覆盖 (2)          │  │                                ││
│                          │  │ Prompt 片段（只读）            ││
│  [重置为默认]            │  │ [检查是否使用 eval...     ]   ││
│                          │  │                                ││
│                          │  │ [   禁用此规则   ]            ││
│                          │  └──────────────────────────────┘│
│                          │                                  │
└──────────────────────────┴──────────────────────────────────┘
```

**交互：**
1. 左侧树按维度分组，每个叶子节点是规则，带 Checkbox 表示是否启用
2. 点击规则 → 右侧展示详情，可修改严重级别覆盖
3. 未选中的规则在 prompt 中不发送给 LLM
4. 提示 "本项目已启用 X/Y 条规则（其中 Go 专属 Z 条）"

**移动端适配：** 左侧树折叠为顶部 Tab 切换（安全性/性能/可读性...）。

### 5.4 项目模板管理页改造（`project-templates.html`）

**Tab 切换：** ① 基本信息 ② 评审规则 ③ 维度权重 ④ GitLab 评论模板

#### Tab 1: 基本信息
```
模板名称 *    [______________________]
模板描述      [______________________]
项目说明      [
  本项目使用 Go 1.26，请重点关注并发安全。
  支付核心模块，不允许出现任意代码执行。
]
最多规则数    [5 ▼]  （每批评审最多输出 N 个规则相关 issue）
              提示：大模型最多返回 5 条 issue，防止输出过长
```

#### Tab 3: 维度权重
```
安全性        [==========] 30%  [编辑]
代码质量      [========  ] 25%  [编辑]
可读性        [======    ] 20%  [编辑]
可维护性      [====      ] 15%  [编辑]
测试覆盖      [===       ] 10%  [编辑]
              ─────────────────
合计：        100% ✅
```

交互：
- 滑块拖动调整权重（0-100，步进 5）
- 实时计算合计，不为 100% 时显示红色警告
- 下方可选 "使用系统默认权重"

#### Tab 4: GitLab 评论模板
```
┌────────────────────────────────────────────────────────┐
│ 评论模板编辑器                                          │
│ ┌────────────────────────────────────────────────────┐ │
│ │ {{.TotalScore}} ▶ 综合评分                          │ │
│ │ {{.IssuesList}} ▶ 预渲染 issue 列表                 │ │
│ │ ...                                                │ │
│ └────────────────────────────────────────────────────┘ │
│ [ 查看可用变量列表 ]                                     │
│                                                        │
│ 预览面板                                               │
│ ┌────────────────────────────────────────────────────┐ │
│ │ ## 🤖 AI 代码评审报告                               │ │
│ │ （用示例数据实时渲染）                                │ │
│ └────────────────────────────────────────────────────┘ │
└────────────────────────────────────────────────────────┘
```

**可用变量列表 Hint：**
```
Score:      {{.TotalScore}}, {{.Summary}}
Dimensions: {{.DimensionsTable}}, {{range .Dimensions}}...
Issues:     {{.IssueCount}}, {{.CriticalCount}}, {{.HighCount}}...
            {{.IssuesList}} 或 {{range .Issues}}
            每个 Issue: {{.SeverityLabel}}, {{.RuleName}}, {{.File}}, {{.LineStart}}, {{.Message}}, {{.Suggestion}}
Recommendations: {{.Recommendations}}, {{.RecommendationsList}}
Utility:    {{"\n\n"}} (BR 换行)
```

### 5.5 任务详情页改造（`task-detail.html`）

新增 "结构化评审" Tab，已有 "完整对话" Tab。

```
┌─────────────────────────────────────────────────────────────┐
│  Task #12345 - my-api-project!45                            │
│  [概览] [结构化评审] [完整对话] [人工复核]                   │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  📊 综合评分：85/100                                        │
│                                                             │
│  ┌────────┬────────┬────────┬────────┬────────┐            │
│  │ 安全性 │代码质量│可读性  │可维护性│测试覆盖│            │
│  │  95    │  80    │  75    │  85    │  90    │            │
│  └────────┴────────┴────────┴────────┴────────┘            │
│                                                             │
│  ┌──────────────────────────────────────────────────────┐  │
│  │ 筛选：严重级别 [全部▼]  文件 [全部▼]  搜索 [____]    │  │
│  ├──────────────────────────────────────────────────────┤  │
│  │                                                      │  │
│  │ 🔴 高危 (2)                                          │  │
│  │ ┌──────────────────────────────────────────────────┐ │  │
│  │ │ [security-hardcoded-secret] 硬编码密钥         │ │  │
│  │ │ pkg/service/auth.go (第 45-52 行)                │ │  │
│  │ │ 此处使用了硬编码密钥                             │ │  │
│  │ │ 建议：...                                        │ │  │
│  │ │ ```go                                           │ │  │
│  │ │ const API_KEY = "sk-..."                         │ │  │
│  │ │ ```                                             │ │  │
│  │ └──────────────────────────────────────────────────┘ │  │
│  │                                                      │  │
│  │ 🟡 中危 (3)                                          │  │
│  │ ...                                                  │  │
│  │                                                      │  │
│  └──────────────────────────────────────────────────────┘  │
│                                                             │
│  💡 改进建议（2 条）                                        │
│  - 建议增加单元测试覆盖                                     │
│  - ...                                                      │
│                                                             │
└─────────────────────────────────────────────────────────────┘
```

**Issue 卡片元素：**
- 严重级别彩色左侧边框
- 规则名称 + 编码（点击可跳转规则库详情）
- 文件路径 + 行号（可点击复制）
- 代码片段（Syntax Highlight）
- 问题描述 + 改进建议

**筛选交互：**
- 严重级别筛选：high/medium/low 多选 Chip
- 文件筛选：下拉列表（列出本次涉及的所有文件）
- 搜索：文本搜索 message + suggestion

**"完整对话" Tab：** 展示 `Task.AIResponseJSON` 的原始 JSON（可折叠），方便调试。

### 5.6 项目管理页改造

在 `projects.html` 的表格中新增列：

```
项目名称    │ 语言    │ 模板           │ 规则数 │ AI 评审 │ 状态
────────────┼─────────┼────────────────┼────────┼─────────┼───────
my-api      │ Go      │ 标准 Go 模板   │ 15/18  │ 启用    │ ✓
payment     │ Go      │ 安全敏感模板   │ 20/22  │ 启用    │ ✓
frontend    │ 前端    │ 标准前端模板   │ 12/15  │ 启用    │ ✓
```

---

## 6. Prompt 工程方案

### 6.1 Prompt 组装顺序

```go
func BuildReviewPrompt(ctx PromptContext) string {
    var sb strings.Builder
    
    // 1. System Role
    sb.WriteString("你是一名资深代码审查专家。请对以下代码变更进行严格审查。\n\n")
    
    // 2. JSON Schema 约束（非 VLLM fallback 时）
    if ctx.UseJSONSchema {
        sb.WriteString("【重要】你的响应必须严格符合以下 JSON Schema，不要包含任何 Markdown 代码块标记（如 ```json）或额外解释文字：\n")
        sb.WriteString(ctx.JSONSchemaDescription)
        sb.WriteString("\n")
    }
    
    // 3. 项目自定义说明
    if ctx.CustomInstruction != "" {
        sb.WriteString("【项目特殊要求】\n")
        sb.WriteString(ctx.CustomInstruction)
        sb.WriteString("\n\n")
    }
    
    // 4. 维度评分权重说明
    sb.WriteString("【评分维度及权重】\n")
    for _, dim := range ctx.Dimensions {
        sb.WriteString(fmt.Sprintf("- %s（权重 %d%%）：%s\n", 
            dim.Label, dim.Weight, dim.Description))
    }
    sb.WriteString("\n")
    
    // 5. 评审规则列表（最多 N 条）
    sb.WriteString("【重点关注以下评审规则】\n")
    for i, rule := range ctx.Rules {
        sb.WriteString(fmt.Sprintf("%d. [%s] %s（严重级别：%s）：%s\n",
            i+1, rule.Category, rule.Name, rule.Severity, rule.Prompt))
    }
    sb.WriteString("\n对于未在规则列表中的其他问题，也可以一并指出。\n\n")
    
    // 6. 待评审代码
    sb.WriteString("【待评审的代码变更】\n")
    for _, file := range ctx.Files {
        sb.WriteString(fmt.Sprintf("### 文件：%s\n```diff\n%s\n```\n\n",
            file.Path, file.Diff))
    }
    
    // 7. Commit 信息
    sb.WriteString(fmt.Sprintf("\n【Commit 历史】\n%s\n", ctx.CommitsText))
    
    // 8. MR 名称
    sb.WriteString(fmt.Sprintf("\n【MR 标题】%s\n", ctx.MRTitle))
    
    // 9. 输出示例（可选，增加格式稳定性）
    sb.WriteString("\n【输出示例】\n")
    sb.WriteString(ctx.JSONExample)
    
    return sb.String()
}
```

### 6.2 规则截断策略

当项目启用的规则数 > `max_rules_per_review` 时，截断逻辑：

```go
func selectTopRules(rules []ReviewRule, max int) []ReviewRule {
    // 按严重级别排序：critical > high > medium > low > info
    severityOrder := map[string]int{"critical":5, "high":4, "medium":3, "low":2, "info":1}
    sort.Slice(rules, func(i, j int) bool {
        return severityOrder[rules[i].Severity] > severityOrder[rules[j].Severity]
    })
    if len(rules) > max {
        return rules[:max]
    }
    return rules
}
```

### 6.3 Summary Prompt 特殊处理

分批评审的汇总步骤同样走 JSON schema，但 prompt 增加 "合并" 语义：

```
你收到了之前对多个文件批次的评审结果。请基于以下各批次评审意见，
生成最终的综合评审报告。要求：
1. 合并各批次中相同文件/相同类型的问题，避免重复
2. 计算各维度综合评分（不是平均值，要加权评估影响面）
3. 输出严格遵循以下 JSON Schema：
```

---

## 7. JSON Schema 与结构化输出

### 7.1 Go Struct 定义（Strict Mode，无 omitempty）

```go
package engine

// AIReviewResult LLM 必须输出的 JSON 结构
// 所有字段必须有值（Strict Mode），Issues 为空时输出 "[]"
type AIReviewResult struct {
    SchemaVersion   string                  `json:"schema_version" jsonschema_description:"Schema 版本，固定为 1.0"`
    TotalScore      int                     `json:"total_score" jsonschema_description:"综合评分，0-100"`
    Dimensions      map[string]Dimension    `json:"dimensions" jsonschema_description:"各维度评分及权重"`
    Summary         string                  `json:"summary" jsonschema_description:"评审总结，100字以内"`
    Issues          []AIReviewIssue         `json:"issues" jsonschema_description:"发现的问题列表，无问题填 []"`
    Recommendations []string                `json:"recommendations" jsonschema_description:"改进建议列表，无建议填 []"`
}

type Dimension struct {
    Score  int `json:"score" jsonschema_description:"该维度得分 0-100"`
    Weight int `json:"weight" jsonschema_description:"该维度权重百分比"`
}

type AIReviewIssue struct {
    RuleCode      string `json:"rule_code" jsonschema_description:"规则编码，如果不属于已知规则填 null"`
    Severity      string `json:"severity" jsonschema_description:"严重级别：critical/high/medium/low/info"`
    Category      string `json:"category" jsonschema_description:"所属维度"`
    File          string `json:"file" jsonschema_description:"文件路径"`
    LineStart     int    `json:"line_start" jsonschema_description:"起始行号，不确定时填 0"`
    LineEnd       int    `json:"line_end" jsonschema_description:"结束行号，单行为 0"`
    CodeSnippet   string `json:"code_snippet" jsonschema_description:"相关代码片段，不超过 500 字符"`
    Message       string `json:"message" jsonschema_description:"问题描述"`
    Suggestion    string `json:"suggestion" jsonschema_description:"改进建议"`
}
```

### 7.2 Schema 生成器

```go
package engine

import (
    "github.com/invopop/jsonschema"
)

func GetReviewJSONSchema() interface{} {
    r := &jsonschema.Reflector{
        AllowAdditionalProperties: false,  // strict=true 要求不输出额外字段
        DoNotReference:            true,   // 内联展开，减少复杂度
    }
    return r.Reflect(AIReviewResult{})
}
```

### 7.3 Request Builder

```go
func BuildStructuredChatRequest(messages []llm.Message, schema interface{}) *llm.ChatRequest {
    return &llm.ChatRequest{
        Model:       "gpt-4o", // 或项目配置的模型
        Messages:    messages,
        Temperature: 0.1,      // 低温度增加确定性
        MaxTokens:   4096,
        ResponseFormat: &llm.ResponseFormat{
            Type: "json_schema",
            JSONSchema: &llm.JSONSchema{
                Name:   "code_review_result",
                Strict: true,
                Schema: schema,
            },
        },
    }
}
```

### 7.4 VLLM Fallback

```go
func BuildFallbackChatRequest(messages []llm.Message, schema interface{}) *llm.ChatRequest {
    // VLLM 不支持 json_schema 时，使用 json_object 或 prompt-level 约束
    return &llm.ChatRequest{
        Model:       "vllm-model",
        Messages:    messages,
        Temperature: 0.1,
        MaxTokens:   4096,
        ResponseFormat: &llm.ResponseFormat{
            Type: "json_object", // fallback level 2
        },
    }
}
```

### 7.5 Provider 兼容性矩阵

| Provider | `json_schema` | `json_object` | Prompt-level | 推荐策略 |
|----------|--------------|---------------|-------------|---------|
| OpenAI | ✅ | ✅ | ✅ | `json_schema` + strict |
| Azure OpenAI | ✅ | ✅ | ✅ | `json_schema` + strict |
| DeepSeek | ✅ | ✅ | ✅ | `json_schema` + strict |
| VLLM (最新版) | ⚠️ 可能不支持 | ⚠️ 可能不支持 | ✅ | fallback 到 prompt-level |

---

## 8. 重试与降级策略

### 8.1 指数退避 Retry

```go
type RetryConfig struct {
    MaxAttempts       int
    InitialDelay      time.Duration
    BackoffMultiplier float64
    MaxDelay          time.Duration
}

func retryWithBackoff(cfg RetryConfig, fn func() error) error {
    delay := cfg.InitialDelay
    for attempt := 1; attempt <= cfg.MaxAttempts; attempt++ {
        err := fn()
        if err == nil {
            return nil
        }
        if attempt == cfg.MaxAttempts {
            return fmt.Errorf("after %d attempts: %w", cfg.MaxAttempts, err)
        }
        time.Sleep(delay)
        delay = time.Duration(float64(delay) * cfg.BackoffMultiplier)
        if delay > cfg.MaxDelay {
            delay = cfg.MaxDelay
        }
    }
    return nil
}
```

### 8.2 完整解析流程

```
raw = LLM response content

Step 1: Refusal Check
  if raw.Refusal != "" → return error("model refused")

Step 2: Preprocess
  content = sanitizeJSON(raw.Content)
  // 去掉 ```json ... ``` 包裹、前导空格、BOM

Step 3: Unmarshal
  var result AIReviewResult
  err = json.Unmarshal([]byte(content), &result)
  if err == nil → success

Step 4: Retry (if attempt < max)
  delay → re-call LLM
  goto Step 1

Step 5: Fallback
  strategy = SystemConfig.JSONRetryFallbackStrategy
  
  "regex":
    score, ok = extractScoreFromReport(raw.Content)
    result = AIReviewResult{TotalScore: score}
    // issue_count = 0，dimension_scores = empty
    
  "markdown":
    result = AIReviewResult{}
    // 保存原始文本到 Task.AIResponse，score = 0
    
  "fail":
    return error, 标记 task failed
```

### 8.3 Sanitize 函数实现

```go
func sanitizeJSON(raw string) string {
    // 1. 去掉 BOM
    raw = strings.TrimPrefix(raw, "\ufeff")
    
    // 2. 去掉 markdown 代码块标记
    raw = strings.TrimSpace(raw)
    raw = strings.TrimPrefix(raw, "```json")
    raw = strings.TrimPrefix(raw, "```")
    raw = strings.TrimSuffix(raw, "```")
    raw = strings.TrimSpace(raw)
    
    // 3. 找第一个 { 和最后一个 }
    start := strings.Index(raw, "{")
    end := strings.LastIndex(raw, "}")
    if start >= 0 && end > start {
        raw = raw[start:end+1]
    }
    
    // 4. 处理常见 Unicode 转义问题
    raw = strings.ReplaceAll(raw, "\\u0000", "")
    
    return raw
}
```

---

## 9. GitLab 评论模板系统

### 9.1 模板上下文结构

```go
type CommentTemplateContext struct {
    TaskID             uint
    ProjectName        string
    MRTitle            string
    MRAuthor           string
    TotalScore         int
    Summary            string
    DimensionsTable    template.HTML  // 预渲染
    Dimensions         []DimensionContext
    IssuesList         template.HTML  // 预渲染
    Issues             []IssueContext
    IssueCount         int
    CriticalCount      int
    HighCount          int
    MediumCount        int
    LowCount           int
    InfoCount          int
    Recommendations    []string
    RecommendationsList template.HTML // 预渲染
    BR                 string        // "\n\n"
}

type DimensionContext struct {
    Name        string
    Label       string
    Score       int
    Weight      int
    WeightLabel string // "30%"
}

type IssueContext struct {
    Severity        string
    SeverityLabel   string
    SeverityEmoji   string
    RuleCode        string
    RuleName        string
    Category        string
    File            string
    LineStart       int
    LineEnd         int
    CodeSnippet     string
    Message         string
    Suggestion      string
}
```

### 9.2 预渲染函数

```go
func (ctx *CommentTemplateContext) PreRender() {
    ctx.DimensionsTable = buildDimensionsTable(ctx)
    ctx.IssuesList = buildIssuesList(ctx)
    ctx.RecommendationsList = buildRecommendationsList(ctx)
    ctx.BR = "\n\n"
}

func buildDimensionsTable(ctx *CommentTemplateContext) template.HTML {
    var b strings.Builder
    b.WriteString("| 维度 | 得分 | 权重 |\n")
    b.WriteString("|------|------|------|\n")
    for _, d := range ctx.Dimensions {
        b.WriteString(fmt.Sprintf("| %s | %d | %s |\n",
            d.Label, d.Score, d.WeightLabel))
    }
    return template.HTML(b.String())
}

func buildIssuesList(ctx *CommentTemplateContext) template.HTML {
    var b strings.Builder
    grouped := groupIssuesBySeverity(ctx.Issues)
    for _, sev := range []string{"critical","high","medium","low","info"} {
        if issues, ok := grouped[sev]; ok && len(issues) > 0 {
            b.WriteString(fmt.Sprintf("#### %s (%d)\n\n", severityLabel(sev), len(issues)))
            for _, issue := range issues {
                b.WriteString(fmt.Sprintf("**[%s] %s**\n", issue.RuleCode, issue.Message))
                if issue.File != "" {
                    b.WriteString(fmt.Sprintf("- **文件**：`%s`", issue.File))
                    if issue.LineStart > 0 {
                        b.WriteString(fmt.Sprintf(" (第 %d", issue.LineStart))
                        if issue.LineEnd > issue.LineStart {
                            b.WriteString(fmt.Sprintf("-%d", issue.LineEnd))
                        }
                        b.WriteString(" 行)")
                    }
                    b.WriteString("\n")
                }
                if issue.CodeSnippet != "" {
                    b.WriteString(fmt.Sprintf("```\n%s\n```\n", issue.CodeSnippet))
                }
                b.WriteString(fmt.Sprintf("- **建议**：%s\n\n", issue.Suggestion))
            }
        }
    }
    return template.HTML(b.String())
}
```

### 9.3 默认内置模板

```markdown
## 🤖 AI 代码评审报告

{{if gt .CriticalCount 0}}
⚠️ **发现 {{.CriticalCount}} 个严重问题，请立即处理！**
{{end}}

**综合评分：{{.TotalScore}}/100**

### 📊 维度评分
{{.DimensionsTable}}

{{if gt .IssueCount 0}}
### ⚠️ 发现的问题（共 {{.IssueCount}} 个）
{{.IssuesList}}
{{end}}

{{if .Recommendations}}
### 💡 改进建议
{{.RecommendationsList}}
{{end}}
```

### 9.4 模板渲染示例

**输入：**
- TotalScore: 85
- CriticalCount: 0
- IssueCount: 2
- Issues: [{severity:"high", rule_code:"hardcoded-secret", message:"硬编码密钥", file:"auth.go", line_start:45}, ...]

**输出：**

```markdown
## 🤖 AI 代码评审报告

**综合评分：85/100**

### 📊 维度评分
| 维度 | 得分 | 权重 |
|------|------|------|
| 安全性 | 95 | 30% |
| 代码质量 | 80 | 25% |
| 可读性 | 75 | 20% |
| 可维护性 | 85 | 15% |
| 测试覆盖 | 90 | 10% |

### ⚠️ 发现的问题（共 2 个）
#### 高危 (1)

**[security-hardcoded-secret] 硬编码密钥**
- **文件**：`pkg/service/auth.go` (第 45 行)
```
const API_KEY = "sk-1234567890abcdef"
```
- **建议**：建议改从环境变量读取，如 os.Getenv("API_KEY")

#### 中危 (1)
...

### 💡 改进建议
- 建议增加单元测试覆盖
- 考虑将密码学操作抽取为独立包
```

---

## 10. Migration 与兼容性策略

### 10.1 数据库 Migration 顺序

```sql
-- Phase 1: 新增表（无风险）
_run_migration_001_create_review_rules()
_run_migration_002_create_project_review_configs()
_run_migration_003_create_review_issues()

-- Phase 2: 改造表（加列，不影响现有数据）
_run_migration_004_alter_projects_add_language()
_run_migration_005_alter_project_templates_add_columns()
_run_migration_006_alter_tasks_add_structured_columns()
_run_migration_007_alter_system_configs_add_retry_columns()

-- Phase 3: 数据迁移（Go 代码在 InitDB 中执行）
_run_migration_008_init_built_in_rules()      -- INSERT IGNORE 35-40 条规则
_run_migration_009_init_project_rule_configs() -- 为所有现有项目生成默认配置
_run_migration_010_migrate_template_weights()  -- 如果旧模板有 weight 相关文本，解析迁移

-- Phase 4: 废弃标记（下一版本清理）
-- ALTER TABLE project_templates DROP COLUMN prompt;  -- 不要现在做
```

### 10.2 运行时兼容性

| 场景 | 处理策略 |
|------|---------|
| 旧 Task 无 `AIResponseJSON` | Task 详情页检测为空 → 走老逻辑渲染纯文本 |
| 旧 Template 无 `dimension_weights` | 使用 `SystemConfig.default_dimension_weights` 兜底 |
| 旧 Project 无 `language` | 默认 `"golang"`，提示管理员补填 |
| `review_issues` 表为空 | Markdown 组装器正常渲染（显示 "未发现明确问题"） |

### 10.3 双轨运行期

从发布到完全废弃旧 prompt 模板，预留 **2 个版本**（约 1 个月）：

- **v4.4**：新功能上线，旧 `ProjectTemplate.Prompt` 字段保留但不再读取
  - 前端 UI 改为新配置方式，旧 prompt 文本显示为只读
  - 所有新 Task 走结构化链路
- **v4.5**：确认无问题后，清理废弃字段
  - 删除 `project_templates.prompt` 列
  - 删除 `tasks` 中纯文本评分的兼容逻辑

---

## 11. 实施里程碑

### 里程碑 1：基础设施（Week 1）

- [ ] 完成所有数据库 migration 脚本（新表 + 改表）
- [ ] 内置规则初始化脚本（35-40 条规则，Go/Python/前端/Java + common）
- [ ] `AIReviewResult` struct + `GetReviewJSONSchema()` 实现
- [ ] LLM Client 改造：`ResponseFormat` + `JSONSchema` 字段

**交付物：**
- `backend/internal/model/migrations/` migration 脚本
- `backend/pkg/llm/schema.go` JSON schema 生成器
- `backend/pkg/llm/client.go` 扩展（向后兼容）

### 里程碑 2：核心引擎（Week 2）

- [ ] `TemplatePromptBuilder` 实现（规则组装 + 维度权重 + 截断）
- [ ] `ResponseParser` 实现（sanitize + unmarshal + retry + fallback）
- [ ] `DataPersistor` 实现（Task 字段更新 + review_issues 入库）
- [ ] `MarkdownCommentAssembler` 实现（text/template + 预渲染函数）
- [ ] 分批评审的 Summary Prompt 适配

**交付物：**
- `backend/internal/engine/builder.go` Prompt 组装
- `backend/internal/engine/parser.go` 响应解析
- `backend/internal/engine/assembler.go` Markdown 组装

### 里程碑 3：API 与后端接口（Week 3）

- [ ] 评审规则管理 API（CRUD + 批量启用 + 树形查询）
- [ ] 项目规则配置 API（GET/PUT/RESET）
- [ ] 项目模板管理 API 改造（dimension_weights, max_rules, comment_template）
- [ ] Task 结构化评审结果查询 API
- [ ] 系统配置 API 扩展（JSON 重试配置）
- [ ] 模板预览 API

**交付物：**
- `backend/internal/handler/review_rule.go`
- `backend/internal/handler/project_review.go`
- `backend/internal/handler/template.go`（改造）

### 里程碑 4：前端页面（Week 4）

- [ ] `review-rules.html` — 评审规则库管理页
- [ ] `project-review-rules.html` — 项目级规则配置页（Tab 形式）
- [ ] `project-templates.html` 改造 — 配置化模板管理
- [ ] `task-detail.html` 改造 — 结构化评审结果展示
- [ ] `settings.html` 改造 — JSON 重试配置
- [ ] `projects.html` 改造 — 新增语言/规则数列

**交付物：**
- `prototype/review-rules.html`
- `prototype/project-review-rules.html`
- 相关 `.js` 文件

### 里程碑 5：集成测试与发布（Week 5）

- [ ] 端到端测试：Webhook → Prompt 组装 → LLM → JSON 解析 → Markdown → GitLab 评论
- [ ] 多 Provider 测试（OpenAI / DeepSeek / Azure / VLLM fallback）
- [ ] 边界测试：空 diff、超长 diff、无 issue、重试耗尽、fallback 策略
- [ ] 数据迁移验证：旧 Task 兼容展示、旧模板平滑过渡
- [ ] 性能测试：规则数对 token 消耗的影响

---

## 附录 A：内置规则完整清单

### A.1 通用规则（common）

| # | 编码 | 名称 | 分类 | 严重级别 |
|---|------|------|------|---------|
| 1 | `common-sql-injection` | SQL注入 | security | **critical** |
| 2 | `common-hardcoded-secret` | 硬编码密钥 | security | **high** |
| 3 | `common-xss-vulnerability` | XSS漏洞 | security | **high** |
| 4 | `common-unsafe-deserialization` | 不安全的反序列化 | security | **high** |
| 5 | `common-resource-leak` | 资源泄露 | performance | **high** |
| 6 | `common-n-plus-one-query` | N+1查询 | performance | **medium** |
| 7 | `common-inefficient-loop` | 低效循环 | performance | **medium** |
| 8 | `common-magic-number` | 魔法数字 | readability | **low** |
| 9 | `common-deep-nesting` | 嵌套过深 | maintainability | **medium** |
| 10 | `common-too-long-function` | 函数过长 | maintainability | **medium** |

### A.2 Go 专用规则（golang）

| # | 编码 | 名称 | 分类 | 严重级别 |
|---|------|------|------|---------|
| 11 | `go-error-handling` | 错误处理不当（未 wrap） | maintainability | **medium** |
| 12 | `go-context-propagation` | Context 未传递 | maintainability | **medium** |
| 13 | `go-goroutine-leak` | Goroutine 泄露 | performance | **high** |
| 14 | `go-interface-compliance` | 接口实现未显式校验 | readability | **low** |
| 15 | `go-concurrency-race` | 共享状态未保护 | security | **high** |
| 16 | `go-panic-recovery` | 不当使用 panic | security | **high** |
| 17 | `go-prepared-statement` | 未使用预编译 | security | **medium** |
| 18 | `go-struct-tag` | JSON tag 格式错误 | readability | **low** |
| 19 | `go-channel-close` | Channel 未正确关闭 | performance | **medium** |
| 20 | `go-nil-pointer` | 潜在空指针访问 | security | **high** |
| 21 | `go-string-concat-loop` | 循环内字符串拼接 | performance | **low** |
| 22 | `go-defer-in-loop` | 循环内使用 defer | performance | **medium** |

### A.3 Python 专用规则（python）

| # | 编码 | 名称 | 分类 | 严重级别 |
|---|------|------|------|---------|
| 23 | `py-bare-except` | 裸 except | maintainability | **medium** |
| 24 | `py-mutable-default-arg` | 可变默认参数 | security | **high** |
| 25 | `py-type-hint-missing` | 缺少类型注解 | readability | **low** |
| 26 | `py-sql-string-format` | SQL 字符串格式化 | security | **critical** |
| 27 | `py-global-mutable` | 全局可变对象滥用 | maintainability | **medium** |
| 28 | `py-list-comprehension` | 未使用列表推导式 | readability | **info** |
| 29 | `py-eval-exec` | 使用 eval/exec | security | **critical** |

### A.4 前端专用规则（frontend）

| # | 编码 | 名称 | 分类 | 严重级别 |
|---|------|------|------|---------|
| 30 | `frontend-xss-innerHTML` | 直接插入 innerHTML | security | **high** |
| 31 | `frontend-memory-leak` | 未清理事件监听/定时器 | performance | **medium** |
| 32 | `frontend-callback-hell` | 回调地狱 | readability | **low** |
| 33 | `react-missing-key` | 列表缺少 key | performance | **low** |
| 34 | `vue-mutate-prop` | 直接修改 props | maintainability | **medium** |
| 35 | `frontend-cors-misconfig` | CORS 配置过于宽松 | security | **high** |
| 36 | `frontend-hardcoded-api-key` | 前端硬编码 API Key | security | **critical** |

### A.5 Java 专用规则（java）

| # | 编码 | 名称 | 分类 | 严重级别 |
|---|------|------|------|---------|
| 37 | `java-null-pointer` | NPE 潜在风险 | security | **high** |
| 38 | `java-resource-leak` | 未用 try-with-resources | performance | **medium** |
| 39 | `java-concurrent-modification` | 并发修改异常 | security | **high** |
| 40 | `java-string-concat-loop` | 循环内 String 拼接 | performance | **medium** |
| 41 | `java-raw-type` | 使用泛型原始类型 | maintainability | **low** |
| 42 | `java-transactional-misuse` | 事务注解使用不当 | security | **high** |
| 43 | `java-magic-number` | 魔法数字 | readability | **low** |
| 44 | `java-singleton-race` | 单例模式并发问题 | security | **high** |

---

## 附录 B：Prompt 片段示例

### B.1 `go-error-handling`

```text
检查 Go 代码的错误处理是否符合最佳实践：
1. 错误返回时是否使用了 fmt.Errorf("...: %w", err) 进行 wrap？
2. 是否避免了只写 `if err != nil { return err }` 而未添加上下文？
3. 是否在错误路径上记录了足够的信息（如参数值）？
4. 是否避免了 panic/recover 的错误处理模式？
```

### B.2 `common-hardcoded-secret`

```text
检查是否存在硬编码的敏感信息：
1. 字符串字面量中包含 'api_key', 'secret', 'password', 'token', 'private_key' 等关键词
2. JWT 签名密钥、数据库密码、云服务凭据
3. 配置文件中的明文密码
4. 注释中泄露的敏感信息
注意：区分测试数据和真实密钥（测试数据可标记为 low）。
```

### B.3 `python-eval-exec`

```text
检查 Python 代码中是否存在不安全的动态代码执行：
1. 使用 eval() 或 exec() 处理不可信输入
2. 使用 compile() + exec 的动态执行模式
3. subprocess 或 os.system 拼接用户输入
4. 模板引擎中的 SSTI（服务器端模板注入）
```

---

*文档结束*
