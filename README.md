<div align="center">

# 🤖 CodeGuard / AI CodeGuard

**AI 驱动的智能代码审查门禁系统**
<br>
<em>AI-Powered Code Review Gate System</em>

[![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go&logoColor=white)](https://go.dev)
[![Gin](https://img.shields.io/badge/Gin-v1.9+-009485?logo=go&logoColor=white)](https://gin-gonic.com)
[![TailwindCSS](https://img.shields.io/badge/TailwindCSS-3.x-06B6D4?logo=tailwindcss&logoColor=white)](https://tailwindcss.com)
[![MySQL](https://img.shields.io/badge/MySQL-8.0-4479A1?logo=mysql&logoColor=white)](https://mysql.com)
[![Docker](https://img.shields.io/badge/Docker-Supported-2496ED?logo=docker&logoColor=white)](https://docker.com)

</div>

---

## 📖 项目简介 / Overview

**CodeGuard**（又称 **AI CodeGuard**）是一款面向企业级 GitLab 工作流的 AI 智能代码审查平台。系统通过 GitLab Webhook 自动触发 AI 代码审查，基于多 LLM 模型进行多维度评分（0-100 分），并根据阈值策略自动触发深度审查，帮助团队在生产代码合入前拦截潜在风险。

> CodeGuard is an enterprise-grade AI-powered code review platform designed for GitLab workflows. It automatically triggers AI code review via GitLab Webhooks, performs multi-dimensional scoring (0-100) using multiple LLM models, and auto-invokes deep review based on configurable thresholds to intercept risks before merging.

---

## ✨ 核心特性 / Features

| 特性 | Feature | 说明 |
|------|---------|------|
| 🔗 | **GitLab Webhook** | Merge Request 自动触发 AI 审查；支持 Note 评论触发 |
| 🤖 | **AI 评分系统** | 0-100 分智能评分，低于阈值自动触发深度审查 |
| 🧠 | **多 LLM 模型管理** | 支持配置多个模型提供商，**支持主模型/备用模型角色切换与自动故障切换** |
| 🏊 | **资源池管理** | OpenCode 服务资源池的统一接入、调度与健康监控 |
| 📊 | **Dashboard 看板** | KPI 总览、雷达图、趋势分析、任务分布统计 |
| 📈 | **MR 统计** | 独立的 MR 聚合统计页面，展示状态分布、平均评分、低质量 MR 等 |
| 💬 | **实时 AI 对话** | 基于 SSE 的流式事件，任务执行过程可实时与 AI 交互 |
| 📧 | **邮件报告系统** | 周报/月报自动生成，Outlook 兼容 HTML 邮件模板，支持分组发送 |
| 🔔 | **企业微信通知** | WeCom 群机器人实时推送审查结果 |
| 👤 | **成员映射** | Git 用户名与企业微信 IM 用户的自动映射 |
| 🔐 | **完整认证体系** | bcrypt 密码加密 + JWT Token 鉴权 + 操作日志审计 |
| ⚙️ | **系统配置化** | 全量系统参数 Web 端可配置，无需重启服务 |
| 📈 | **资源池/模型监控告警** | 异常持续达阈值后自动发企业微信告警，恢复后发送恢复通知 |
| 🔄 | **MR 状态同步** | 定时轮询 GitLab，刷新本地 opened MR 的合并/关闭状态 |
| 🛡️ | **Diff 截断保护** | 超阈值大代码块入库前自动截断，防止 DB 存储爆炸 |
| 🏷️ | **任务模型追踪** | Review 任务记录实际使用的 LLM 模型，列表展示 `[主]/[备用N] model_id` |

---

## 🖼️ 产品截图 / Screenshots

### 首页统计 Dashboard

![首页统计 Dashboard](docs/img/HomePage.png)

### 大模型管理

![大模型管理](docs/img/model.png)

### MR 代码提交统计

![MR 代码提交统计](docs/img/mr.png)

### 任务管理

![任务管理](docs/img/task.png)

### AI评审（MR评论）

![AI评审](docs/img/comment-ai-codereview.png)

### AI深度评审（MR评论）

![AI深度评审](docs/img/comment-ai-deep-codereview.png)

---

## 🏗️ 技术架构 / Architecture

```text
+-------------------------------------------------------------------+
|                          Client Browser                             |
|               (Vanilla HTML + TailwindCSS Frontend)                |
+-------------------------------+-----------------------------------+
                                |  HTTP / API
+-------------------------------v-----------------------------------+
|                        Gin HTTP Server                              |
|   Static File Serving (prototype/)                                   |
|   JWT Auth Middleware                                               |
|   CORS / Logger / Recovery                                          |
+---------------+---------------+---------------+-------------------+
                |               |               |
     +----------v----+  +------v------+  +------v------+
     |   Webhook     |  |  Dashboard  |  |   Report    |
     |   Handler     |  |   Handler   |  |   Handler   |
     +-------+-------+  +------+------+  +------+------+
             |                |               |
+------------v----------------v---------------v---------------------+
|                         Service Layer                               |
|   ProjectService  TaskService  PoolService  ModelService           |
|   ReportService  NotifierService  MemberMappingService             |
|   LLMService  UserService  MRReviewLogService                       |
+------------+--------------+--------------+--------------+---------+
             |              |              |              |
   +---------v------+ +----v------+ +----v------+ +----v----------+
   |    GORM ORM     | |   Cron    | |  GitLab   | |  SMTP /      |
   |  (MySQL)        | | Scheduler | |  API      | |  WeCom       |
   |                 | |           | |  Client   | |  Webhook     |
   +-----------------+ +-----------+ +-----------+ +--------------+
           |                                              |
   +-------v-------+                            +--------v---------+
   |  codeguard    |                            | External Services|
   |   (Main DB)   |                            | - OpenCode Pool  |
   +---------------+                            | - LLM APIs       |
                                                | - GitLab CE/EE   |
                                                | - WeCom Bot      |
                                                | - Mail Server    |
                                                +------------------+
```

---

## 🚀 快速开始 / Quick Start


### 1. Clone 项目

```bash
git clone <your-repo-url> codeguard
cd codeguard
```

### 2. 配置环境变量

```bash
cat > .env <<EOF
DB_HOST=your_mysql_host
DB_NAME=your_mysql_db_name
DB_PASSWORD=your_mysql_password
ENCRYPTION_KEY=your_32_byte_encryption_key_here!!
EOF
```

> ⚠️ `ENCRYPTION_KEY` 必须为 **32 字节**，用于敏感数据 AES 加密存储，可以不用配置。

### 3. 启动服务

```bash
docker run -d -p 8080:8080 \
  --env-file .env \
  codeguard:latest
```

- 构建并启动 CodeGuard 服务（请先构建好容器镜像）

### 4. 访问系统

| 端点 | 说明 |
|------|------|
| `http://localhost:8080` | Dashboard 看板（默认首页） |
| `http://localhost:8080/login.html` | 登录页面 |
| `http://localhost:8080/api/v1/webhooks/gitlab` | GitLab Webhook 接收地址 |

默认管理员账号：`admin / admin123`（首次登录后请立即修改密码）

---


## ⚙️ 环境变量 / Environment Variables

### 必填项 / Required

| 变量 | 示例 | 说明 |
|------|------|------|
| `DB_PASSWORD` | `change_me` | 数据库密码 |
| `ENCRYPTION_KEY` | `your_32_byte_key_here!!` | AES 加密密钥，必须为 32 字节 |

### 数据库 / Database

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `DB_HOST` | `127.0.0.1` | 数据库主机 |
| `DB_PORT` | `3306` | 数据库端口 |
| `DB_USER` | `root` | 数据库用户 |
| `DB_NAME` | `ai_optimizer` | 业务数据库名 |
| `DATABASE` | `mysql` | 数据库类型：`mysql` / `postgres` |
| `DSN` | `""` | 完整 DSN（配置此项可跳过上述拆分配置） |

### GitLab & 业务配置

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `GITLAB_TOKEN` | `""` | GitLab API Token，当项目未单独配置 AccessToken 时作为全局 fallback 用于请求 GitLab API |
| `PROJECT_BASE_DIR` | `/data/gitlab/` | 项目代码克隆存储目录 |

### 任务与系统

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `TASK_TIMEOUT_MIN` | `30` | 单个任务超时时间（分钟） |
| `MAX_PARALLEL_TASK` | `20` | 系统最大并行任务数 |

### 应用通用

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `PORT` | `8080` | HTTP 服务端口 |
| `DEBUG` | `false` | 调试模式（开启后输出 Debug 级别日志） |
| `FRONTEND_PATH` | `/app/prototype` | 前端静态文件目录（Docker 镜像内路径） |

> 生产环境请务必设置 `ENCRYPTION_KEY` 和 `DB_PASSWORD`。

---

## 🛠️ 开发环境 / Development Setup

### 前置依赖

- Go 1.26+
- MySQL 8.0（或 PostgreSQL）
- Node.js（可选，仅用于 TailwindCSS CLI 构建前端）

### 本地运行步骤

```bash
# 1. 进入后端目录
cd backend

# 2. 安装依赖
go mod download

# 3. 复制环境变量模板
cp .env.example .env
# 编辑 .env 配置你的数据库连接

# 4. 启动服务
go run ./cmd/main.go
```

服务启动后访问：`http://localhost:8080`

### 前端开发

前端采用 Vanilla HTML + TailwindCSS CDN，无需构建步骤。直接修改 `prototype/` 下的 `.html` 文件即可生效。刷新页面即可看到变更。

---

## 🔌 API 接口概览

### 公开接口（无需认证）

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/api/v1/login` | 用户登录 |
| POST | `/api/v1/logout` | 用户登出 |
| POST | `/api/v1/webhooks/gitlab` | GitLab Webhook 统一入口 |
| POST | `/api/v1/tasks/callback` | 任务回调 |
| GET  | `/health` | 健康检查 |

### 主要业务接口（需 JWT Bearer Token）

| 模块 | 路径前缀 | 核心能力 |
|------|----------|----------|
| Dashboard | `/api/v1/dashboard/*` | 统计指标、趋势、雷达图数据 |
| Project | `/api/v1/projects` | 项目管理、任务列表、自动同步 |
| Task | `/api/v1/tasks` | 任务 CRUD、执行、重试、SSE 事件流、AI 对话 |
| Pool | `/api/v1/pools` | 资源池 CRUD、连通性测试、默认池切换 |
| Model | `/api/v1/models` | LLM 模型 CRUD、API 健康检查 |
| Notifier | `/api/v1/notifiers` | 企业微信机器人配置、测试、开关 |
| Member | `/api/v1/member-mappings` | Git 用户与 IM 账号映射 |
| Report | `/api/v1/reports/*` | SMTP 配置、接收人管理、报告预览与发送 |
| System | `/api/v1/system/*` | 系统配置、操作日志 |
| MR Log | `/api/v1/mr-review-logs` | MR 审查记录、统计、项目/作者筛选 |

> 完整路由定义请参见 `backend/cmd/main.go` 中的 `setupRouter` 函数。

---

## 📦 Docker 镜像构建

### 方式一：多阶段构建（推荐，有外网环境）

```bash
docker build -t codeguard:latest .
```

### 方式二：本地预编译 + 快速构建（离线/CI 环境）

```bash
cd backend
go build -ldflags="-w -s" -o ai-optimizer ./cmd/main.go
cd ..
docker build -f Dockerfile.quick -t codeguard:latest .
```

### 方式三：一键脚本

```bash
./scripts/build-docker.sh 1.2.0 latest
```

---

## 🔒 安全说明 / Security

| 项目 | 实现 |
|------|------|
| 密码存储 | bcrypt 哈希 |
| 敏感配置 | AES-256-CBC 加密存储（数据库中密文保存） |
| 接口鉴权 | JWT Bearer Token（含有效期） |
| Webhook 校验 | GitLab Secret Token 校验 |
| 操作审计 | 全量操作日志记录（请求 IP、操作人、结果） |

---


## 📄 License

MIT License © 2026 Li Hu, UNICLOUD

---

## 注意

纯AI Coding项目

---


<div align="center">

**AI CodeGuard** — 让每一次代码合并都更安全 🛡️

<em>Make every code merge safer with AI.</em>

</div>
