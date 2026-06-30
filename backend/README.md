# CodeGuard Backend

> Go backend for AI 代码智能门禁

## 项目简介

CodeGuard 后端服务，接收 GitLab Webhook、管理 AI 审查任务、资源池、报表调度等。

## Logo

```
  ██████╗ ██████╗ ██████╗ ███████╗██╗   ██╗ █████╗ ██████╗ ██████╗ 
 ██╔════╝██╔═══██╗██╔══██╗██╔════╝██║   ██║██╔══██╗██╔══██╗██╔══██╗
 ██║     ██║   ██║██║  ██║█████╗  ██║   ██║███████║██████╔╝██║  ██║
 ██║     ██║   ██║██║  ██║██╔══╝  ╚██╗ ██╔╝██╔══██║██╔══██╗██║  ██║
 ╚██████╗╚██████╔╝██████╔╝███████╗ ╚████╔╝ ██║  ██║██║  ██║██████╔╝
  ╚═════╝ ╚═════╝ ╚═════╝ ╚══════╝  ╚═══╝  ╚═╝  ╚═╝╚═╝  ╚═╝╚═════╝ 
```

## 技术架构

```
GitLab ──▶ CodeGuard Backend (Go + Gin + GORM + cron)
              ├── Webhook Handler / Dashboard / 认证
              ├── 项目/任务 / 资源池/模型 / 企微通知
               ├── 定时任务: 资源池检查 | 模型检查 | 超时检测 | 报表投递
               ├── 数据库: MySQL (主库)
               └── 前端静态文件: prototype/ (HTML + TailwindCSS)
                        │
                        ▼
              OpenCode Service / LLM API
```

## 环境要求

- Go 1.21+
- MySQL 5.7+
- Docker & Docker Compose (可选)
- GitLab (用于 Webhook 触发)

## 项目结构

```
backend/
├── cmd/
│   └── main.go              # 入口: 初始化日志/DB/加密/cron/Router
├── config/
│   └── config.go            # 环境变量 + .env 加载
├── internal/
│   ├── handler/             # HTTP Handler (REST API)
│   ├── middleware/          # 中间件: 认证/JWT/CORS/日志
│   ├── model/               # GORM models + auto-migration
│   └── service/             # 业务逻辑: 任务/资源池/报表等
├── pkg/
│   ├── encrypt/             # AES-256-GCM
│   ├── gitlab/              # GitLab API Client
│   └── llm/                 # OpenAI/Anthropic/Azure 通用封装
├── Dockerfile
├── Makefile
└── README.md
```

## 快速开始

### 1. 配置

```bash
cp .env.example .env
# 按需编辑 .env
```

关键配置项:

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `DSN` | 见 .env.example | MySQL DSN |
| `ENCRYPTION_KEY` | `""` | **32 字节 AES 密钥（生产必填）** |
| `TASK_TIMEOUT_MIN` | `30` | 任务超时（分钟） |
| `PROJECT_BASE_DIR` | `/data/gitlab/` | 本地项目存放目录 |

### 2. 启动

```bash
make dev              # 开发模式 (go run)
make build            # 本地编译到 build/codeguard
make build-linux      # Linux AMD64 交叉编译
```

默认端口: `8080`

### 3. Dockerfile 构建说明

`Dockerfile` 位于 `backend/`，但构建上下文必须是项目根目录，因为需要复制 `prototype` 前端静态文件：

```bash
cd /data/ai-bug-fix   # 或者你的项目根目录
docker build -f backend/Dockerfile -t codeguard:latest .
```

## API 端点

### 公开接口（无需认证）

| 方法 | 路径 | 说明 |
|------|------|------|
| `POST` | `/api/v1/login` | 登录，返回 Bearer Token |
| `POST` | `/api/v1/logout` | 登出 |
| `POST` | `/api/v1/webhooks/gitlab` | GitLab Webhook 入口（Note / Merge Request） |
| `POST` | `/api/v1/tasks/callback` | OpenCode 任务回调 |
| `GET`  | `/health` | 健康检查 |

### 认证接口

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET`  | `/api/v1/users/me` | 当前登录用户信息 |
| `PUT`  | `/api/v1/users/password` | 修改密码 |

### Dashboard

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET` | `/api/v1/dashboard/stats` | 统计卡片 |
| `GET` | `/api/v1/dashboard/trends` | 近 30 天任务趋势 |
| `GET` | `/api/v1/dashboard/recent-projects` | 最近活跃项目 |
| `GET` | `/api/v1/dashboard/recent-failures` | 最近失败任务 |
| `GET` | `/api/v1/dashboard/task-distribution` | 任务状态分布 |

### 项目管理

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET`  | `/api/v1/projects` | 项目列表 |
| `POST` | `/api/v1/projects` | 新增项目 |
| `GET`  | `/api/v1/projects/:id` | 项目详情 |
| `PUT`  | `/api/v1/projects/:id` | 编辑项目 |
| `DELETE` | `/api/v1/projects/:id` | 删除项目 |
| `GET`  | `/api/v1/projects/:id/tasks` | 项目关联任务 |

### 模板管理

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET`  | `/api/v1/templates` | 模板列表 |
| `POST` | `/api/v1/templates` | 创建模板 |
| `GET`  | `/api/v1/templates/:id` | 模板详情 |
| `PUT`  | `/api/v1/templates/:id` | 更新模板 |
| `DELETE` | `/api/v1/templates/:id` | 删除模板 |
| `POST` | `/api/v1/templates/:id/clone` | 克隆模板 |

### 任务管理

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET`  | `/api/v1/tasks` | 任务列表 |
| `POST` | `/api/v1/tasks` | 创建任务 |
| `GET`  | `/api/v1/tasks/:id` | 任务详情 |
| `POST` | `/api/v1/tasks/:id/execute` | 执行任务 |
| `POST` | `/api/v1/tasks/:id/retry` | 重试任务 |
| `POST` | `/api/v1/tasks/:id/stop` | 停止任务 |
| `GET`  | `/api/v1/tasks/:id/logs` | 获取日志 |
| `GET`  | `/api/v1/tasks/:id/messages` | 消息历史 |
| `POST` | `/api/v1/tasks/:id/messages` | 发送消息 |
| `GET`  | `/api/v1/tasks/:id/events` | SSE 实时事件流 |
| `DELETE` | `/api/v1/tasks/:id/session` | 删除会话 |

### 资源池管理

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET`  | `/api/v1/pools` | 资源池列表 |
| `POST` | `/api/v1/pools` | 创建资源池 |
| `GET`  | `/api/v1/pools/:id` | 详情 |
| `PUT`  | `/api/v1/pools/:id` | 更新 |
| `DELETE` | `/api/v1/pools/:id` | 删除 |
| `POST` | `/api/v1/pools/test` | 连通性测试 |
| `POST` | `/api/v1/pools/:id/check` | 检查单个资源池 |
| `PUT`  | `/api/v1/pools/:id/toggle` | 启用/禁用 |
| `PUT`  | `/api/v1/pools/:id/default` | 设为默认 |
| `DELETE` | `/api/v1/pools/:id/default` | 取消默认 |
| `GET`  | `/api/v1/pools/:id/skills` | 获取资源池技能列表 |

### 大模型管理

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET`  | `/api/v1/models` | 模型列表 |
| `GET`  | `/api/v1/models/default` | 默认模型 |
| `GET`  | `/api/v1/models/:id` | 模型详情 |
| `GET`  | `/api/v1/models/:id/edit` | 用于编辑的模型信息 |
| `POST` | `/api/v1/models` | 创建模型 |
| `POST` | `/api/v1/models/test` | 测试模型连通性 |
| `PUT`  | `/api/v1/models/:id` | 更新模型 |
| `DELETE` | `/api/v1/models/:id` | 删除模型 |
| `PUT`  | `/api/v1/models/:id/default` | 设为默认 |
| `DELETE` | `/api/v1/models/:id/default` | 取消默认 |
| `POST` | `/api/v1/models/:id/check` | 检查 API 可用性 |

### 企业微信通知

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET`  | `/api/v1/notifiers` | 通知配置列表 |
| `GET`  | `/api/v1/notifiers/:id` | 详情 |
| `POST` | `/api/v1/notifiers` | 创建 |
| `PUT`  | `/api/v1/notifiers/:id` | 更新 |
| `DELETE` | `/api/v1/notifiers/:id` | 删除 |
| `POST` | `/api/v1/notifiers/:id/test` | 发送测试消息 |
| `PUT`  | `/api/v1/notifiers/:id/template` | 更新消息模板 |
| `PUT`  | `/api/v1/notifiers/:id/toggle` | 启用/禁用 |

### 成员映射 (Git 用户名 ↔ IM)

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET`  | `/api/v1/member-mappings` | 列表 |
| `POST` | `/api/v1/member-mappings` | 创建映射 |
| `GET`  | `/api/v1/member-mappings/:id` | 详情 |
| `PUT`  | `/api/v1/member-mappings/:id` | 更新 |
| `DELETE` | `/api/v1/member-mappings/:id` | 删除 |
| `GET`  | `/api/v1/member-mappings/git-users` | 获取 Git 用户列表 |
| `GET`  | `/api/v1/member-mappings/check` | 检查映射完整性 |

### 系统管理

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET`  | `/api/v1/system/config` | 系统配置 |
| `PUT`  | `/api/v1/system/config` | 更新配置 |
| `GET`  | `/api/v1/system/info` | 系统信息 |
| `GET`  | `/api/v1/system/logs` | 操作日志 |
| `DELETE` | `/api/v1/system/logs` | 清空日志 |
| `GET`  | `/api/v1/system/sync-logs` | ~~同步日志（已废弃）~~ |

### MR 审查日志

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET`  | `/api/v1/mr-review-logs` | 日志列表 |
| `POST` | `/api/v1/mr-review-logs/:id/mark-as-draft` | 标记为 Draft |
| `POST` | `/api/v1/mr-review-logs/:id/mark-as-ready` | 标记为 Ready |
| `GET`  | `/api/v1/mr-review-logs/projects` | 有数据的项目 |
| `GET`  | `/api/v1/mr-review-logs/authors` | 作者列表 |
| `GET`  | `/api/v1/mr-review-logs/statistics` | 统计报表 |

### 报表管理

| 方法 | 路径 | 说明 |
|------|------|------|
| `GET`  | `/api/v1/reports/smtp` | SMTP 配置 |
| `PUT`  | `/api/v1/reports/smtp` | 保存 SMTP |
| `POST` | `/api/v1/reports/smtp/test` | 测试 SMTP |
| `GET`  | `/api/v1/reports/recipients` | 接收人列表 |
| `POST` | `/api/v1/reports/recipients` | 添加接收人 |
| `PUT`  | `/api/v1/reports/recipients/:id` | 更新接收人 |
| `DELETE` | `/api/v1/reports/recipients/:id` | 删除接收人 |
| `GET`  | `/api/v1/reports/config/:type` | 报表配置 (weekly / monthly) |
| `PUT`  | `/api/v1/reports/config/:type` | 保存报表配置 |
| `GET`  | `/api/v1/reports/preview/:type` | 预览报表 |
| `POST` | `/api/v1/reports/send/:type` | 手动发送报表 |
| `GET`  | `/api/v1/reports/logs` | 发送日志 |
| `DELETE` | `/api/v1/reports/logs/:id` | 删除日志 |
| `GET`  | `/api/v1/reports/logs/:id/html` | 查看 HTML 内容 |

## 定时任务

| 任务 | 调度 | 说明 |
|------|------|------|
| 任务超时检测 | `@every 10s` | 将超过 `TASK_TIMEOUT_MIN` 的任务标记为 `timeout` |
| 资源池健康检查 | 后台守护协程 | 每秒轮询各资源池，更新状态、活跃任务数 |
| 模型健康检查 | 后台守护协程 | 每秒轮询各 LLM 模型，更新 API 可用性 |
| 报表定时投递 | cron 表达式 (配置在 `ReportConfig`) | 按周/月自动生成并发送邮件报表，支持热重载 |

> 资源池与模型的健康检查使用 `time.Ticker` 后台协程实现，非 cron 表达式调度。

## 数据库模型

主库 (`ai_optimizer`) 核心表:

| 模型 | 表名 | 说明 |
|------|------|------|
| `Project` | `projects` | 项目信息、关联模板/资源池/模型 |
| `Task` | `tasks` | AI 任务（chat / review）、MR 关联、状态机 |
| `ProjectTemplate` | `project_templates` | Prompt 模板 |
| `ResourcePool` | `resource_pools` | OpenCode 服务连接配置 |
| `LLMModel` | `llm_models` | 大模型配置（OpenAI / vLLM / Azure...） |
| `WeComNotifier` | `we_com_notifiers` | 企业微信 Webhook 配置 |
| `MemberMapping` | `member_mappings` | Git 用户名 ↔ 企业微信 UserID 映射 |
| `User` | `users` | 后台管理员 |
| `Token` | `tokens` | 登录 Token（7 天有效期） |
| `SystemConfig` | `system_configs` | 全局配置（评分阈值、超时等） |
| `OperationLog` | `operation_logs` | 后台操作审计 |
| `MergeRequestReviewLog` | `merge_request_review_logs` | MR 审查评分与统计（本系统 DB 写入） |
| `SMTPConfig` | `smtp_configs` | 邮件服务配置 |
| `ReportConfig` | `report_configs` | 周/月报表 cron 与开关 |
| `ReportRecipient` | `report_recipients` | 邮件接收人 |
| `ReportLog` | `report_logs` | 报表生成/发送历史 |

> `MergeRequestReviewLog` 为 AI 评审任务完成后直接写入本系统数据库，**不再依赖外部 codereview 库**。

## 认证

- 登录: `POST /api/v1/login` → bcrypt 校验 → 生成 Token 入库
- 鉴权: `Authorization: Bearer <token>` Header
- Token 有效期: 7 天
- 前端通过 `js/auth.js` 统一拦截 `fetch`，401 自动跳转 `/login.html`

## 常用命令

```bash
make help             # 查看帮助
make dev              # 开发运行 (go run)
make build            # 本地编译
make build-linux      # Linux 交叉编译
make build-all        # 全平台编译
make release          # 打包发布 (tar.gz)
make test             # 运行测试
make fmt              # 格式化代码
make lint             # 运行 linter
make docker-build     # Docker 构建 (需项目根目录上下文)
make clean            # 清理构建产物
```

## 许可证

MIT License © 2026 Li Hu, UNICLOUD
