# Solvify-Agent

企业级知识管理 Agent 系统，基于 **Go + Gin + Eino（ReAct Agent）+ PostgreSQL（pgvector）+ Redis**，提供知识库管理、智能问答（快速检索 + 深度 Agent 推理）、多源数据同步（钉钉）、工具编排等能力。

## 功能概览

| 模块 | 说明 |
|------|------|
| 用户认证 | 邮箱注册/登录、JWT Token、验证码、密码重置 |
| 知识库管理 | 自建/同步/联网搜索三种知识库，CRUD + 统计 |
| 文档处理 | 上传 → 解析 → 分块 → 向量化（1024 维） → 索引 |
| 智能问答 | 快速检索（Hybrid + Rerank）+ 深度推理（ReAct Agent） |
| 会话管理 | 多轮对话、历史消息、SSE 流式输出 |
| 模型配置 | 系统默认模型 + 用户自定义模型，OpenAI 兼容 |
| RAG 管线 | 混合检索（向量+关键词+RRF）→ 重排序 → 分块扩展 |
| 工具系统 | Template 驱动 HTTP Provider，Agent 动态加载 |
| 钉钉集成 | OAuth 登录、文档同步 |
| 多源同步 | SyncSource/SyncJob/SyncItem 全量增量同步 |
| 后台管理 | 用户管理、会话管理、模型管理、工具管理 |

## 项目结构

```text
.
├── cmd/server/main.go           # 服务入口
├── cmd/seed/main.go             # 数据库种子
├── configs/
│   ├── config.yaml              # 主配置（含真实值，已 gitignore）
│   └── config.yaml.example      # 配置模板（无密钥）
├── .env.example                 # 环境变量模板
├── docs/
│   ├── architecture.md          # 架构说明
│   ├── DEVELOPMENT.md           # 开发指南
│   └── PRD.md                   # 产品需求文档
├── internal/
│   ├── agent/                   # eino ReAct Agent 引擎
│   ├── api/                     # Gin 路由 + 13 个 Controller 模块
│   ├── app/                     # 应用装配中心
│   ├── integration/dingtalk/    # 钉钉集成
│   ├── llm/                     # LLM 客户端工厂 + Embedding
│   ├── middleware/               # Auth / CORS / Logger / Recovery
│   ├── model/                   # Entity + DTO (Request/Response)
│   ├── rag/                     # RAG 检索管线 (Hybrid / Rerank / Expand)
│   ├── repository/              # 数据访问层 + Redis 缓存装饰器
│   ├── service/                 # 业务服务层
│   └── tool/                    # 工具系统 (Provider / AgentTool)
├── pkg/
│   ├── cache/redis_cache.go     # Redis 缓存封装
│   ├── captcha/                 # 图片验证码
│   ├── config/                  # 配置加载 (YAML + .env + env)
│   ├── database/                # PostgreSQL + Redis 连接
│   ├── email/                   # SMTP 邮件
│   ├── errors/                  # 50+ 业务错误码
│   ├── jwt/                     # JWT 令牌
│   ├── logger/                  # zap + lumberjack 日志
│   ├── response/                # 统一 API 响应
│   ├── tokenutil/               # 随机 Token 生成
│   └── upload/                  # 文件上传
├── scripts/init_knowledge_schema.sql  # 建表 SQL
└── go.mod
```

## 快速开始

### 1. 环境要求

- Go 1.26+
- Python 3.10+（解析 docx/pdf 时需要）
- PostgreSQL 15+（需 pgvector 扩展）
- Redis 7+

### 2. 创建配置

```bash
# 复制配置模板
cp configs/config.yaml.example configs/config.yaml
cp .env.example .env

# 编辑配置文件，填入真实的数据库连接等信息
vim configs/config.yaml
```

### 3. 初始化数据库

```bash
# 确保 PostgreSQL 已运行并已创建数据库
# 方式一：使用 SQL 脚本
psql -U postgres -d solvify_agent -f scripts/init_knowledge_schema.sql

# 方式二：使用 seed 程序（含种子数据）
go run cmd/seed/main.go
```

### 4. 启动服务

```bash
go mod tidy
pip install -r pkg/documentparser/python/requirements.txt
go run cmd/server/main.go
```

服务默认监听 `http://localhost:8080`。

### 5. 验证

```bash
# 健康检查
curl http://localhost:8080/health
```

## 配置方式

项目支持 **三种配置方式**，优先级从低到高：

```
代码默认值  <  config.yaml 文件  <  .env 文件  <  系统环境变量
```

### 方式一：config.yaml 文件（推荐）

将 `configs/config.yaml.example` 复制为 `configs/config.yaml`，修改对应配置：

```bash
cp configs/config.yaml.example configs/config.yaml
```

**注意**：`config.yaml` 可能包含真实密钥，不应提交到 Git。

### 方式二：.env 文件

创建项目根目录下的 `.env` 文件（参考 `.env.example`）：

```bash
cp .env.example .env
```

.env 文件中的变量会覆盖 `config.yaml` 中的对应配置。

### 方式三：系统环境变量

直接在 shell 中设置，优先级最高：

```bash
# Linux / macOS
export SERVER_PORT=9090
export POSTGRES_PASSWORD=your-password
go run cmd/server/main.go

# Windows PowerShell
$env:SERVER_PORT="9090"
$env:POSTGRES_PASSWORD="your-password"
go run cmd/server/main.go
```

### 自定义配置文件路径

```bash
# 通过环境变量指定
export CONFIG_PATH=/path/to/my-config.yaml
go run cmd/server/main.go
```

### 环境变量对照表

所有 `config.yaml` 节点均支持对应环境变量覆盖：

| config.yaml 路径 | 环境变量 | 默认值 |
|-------------------|----------|--------|
| `app.env` | `APP_ENV` | `development` |
| `app.mode` | `APP_MODE` | `release` |
| `server.port` | `SERVER_PORT` | `8080` |
| `server.host` | `SERVER_HOST` | `""` |
| `server.shutdown_timeout_seconds` | `SHUTDOWN_TIMEOUT_SECONDS` | `20` |
| `jwt.secret` | — | — (仅 config.yaml) |
| `jwt.expire_hours` | — | — (仅 config.yaml) |
| `log.level` | `LOG_LEVEL` | `info` |
| `log.filename` | `LOG_FILENAME` | `logs/solvify-agent.log` |
| `log.max_size` | `LOG_MAX_SIZE` | `100` |
| `log.max_backups` | `LOG_MAX_BACKUPS` | `7` |
| `log.max_age` | `LOG_MAX_AGE` | `30` |
| `log.compress` | `LOG_COMPRESS` | `true` |
| `agent.max_iterations` | — | — (仅 config.yaml) |
| `agent.score_threshold` | — | — (仅 config.yaml) |
| `llm.provider` | `LLM_PROVIDER` | `mock` |
| `llm.model` | `LLM_MODEL` | `mock-knowledge-assistant` |
| `llm.api_key` | `LLM_API_KEY` | `""` |
| `llm.base_url` | `LLM_BASE_URL` | `""` |
| `llm.temperature` | `LLM_TEMPERATURE` | `0.7` |
| `llm.max_tokens` | `LLM_MAX_TOKENS` | `2000` |
| `llm.timeout` | `LLM_TIMEOUT` | `30` |
| `embedding.provider` | `EMBEDDING_PROVIDER` | `openai` |
| `embedding.model` | `EMBEDDING_MODEL` | `text-embedding-v4` |
| `embedding.api_key` | `EMBEDDING_API_KEY` | `""` |
| `embedding.base_url` | `EMBEDDING_BASE_URL` | `""` |
| `embedding.dimension` | `EMBEDDING_DIMENSION` | `1024` |
| `rag.enabled` | `RAG_ENABLED` | `true` |
| `rag.reranker.enabled` | `RERANKER_ENABLED` | `false` |
| `rag.reranker.endpoint` | `RERANKER_ENDPOINT` | `""` |
| `rag.reranker.model` | `RERANKER_MODEL` | `""` |
| `rag.reranker.api_key` | `RERANKER_API_KEY` | `""` |
| `rag.reranker.top_n` | `RERANKER_TOP_N` | `3` |
| `rag.reranker.timeout` | `RERANKER_TIMEOUT` | `10` |
| `rag.reranker.score_threshold` | `RERANKER_SCORE_THRESHOLD` | `0.5` |
| `rag.expander.enabled` | `EXPANDER_ENABLED` | `false` |
| `rag.expander.window_size` | `EXPANDER_WINDOW_SIZE` | `1` |
| `rag.expander.max_chunk_tokens` | `EXPANDER_MAX_CHUNK_TOKENS` | `1000` |
| `rag.expander.dedup_threshold` | `EXPANDER_DEDUP_THRESHOLD` | `0.8` |
| `tools.enabled` | `TOOLS_ENABLED` | `true` |
| `dingtalk.app_key` | `DINGTALK_APP_KEY` | `""` |
| `dingtalk.app_secret` | `DINGTALK_APP_SECRET` | `""` |
| `dingtalk.oauth_redirect_uri` | `DINGTALK_OAUTH_REDIRECT_URI` | — |
| `document_parser.python_path` | `DOCUMENT_PARSER_PYTHON_PATH` | `"python"` |
| `document_parser.script_path` | `DOCUMENT_PARSER_SCRIPT_PATH` | `"pkg/documentparser/python/parse_document.py"` |
| `document_parser.timeout_seconds` | `DOCUMENT_PARSER_TIMEOUT_SECONDS` | `30` |
| `database.postgres.host` | `POSTGRES_HOST` | `127.0.0.1` |
| `database.postgres.port` | `POSTGRES_PORT` | `5432` |
| `database.postgres.username` | `POSTGRES_USERNAME` | `postgres` |
| `database.postgres.password` | `POSTGRES_PASSWORD` | `""` |
| `database.postgres.database` | `POSTGRES_DATABASE` | `solvify_agent` |
| `database.postgres.timezone` | `POSTGRES_TIMEZONE` | `Asia/Shanghai` |
| `database.postgres.max_idle_conns` | `POSTGRES_MAX_IDLE_CONNS` | `5` |
| `database.postgres.max_open_conns` | `POSTGRES_MAX_OPEN_CONNS` | `20` |
| `database.postgres.conn_max_lifetime_minutes` | `POSTGRES_CONN_MAX_LIFETIME_MINUTES` | `60` |
| `database.postgres.enable_pgvector` | `POSTGRES_ENABLE_PGVECTOR` | `true` |
| `database.redis.host` | `REDIS_HOST` | `127.0.0.1` |
| `database.redis.port` | `REDIS_PORT` | `6379` |
| `database.redis.password` | `REDIS_PASSWORD` | `""` |
| `database.redis.db` | `REDIS_DB` | `0` |
| `database.redis.pool_size` | `REDIS_POOL_SIZE` | `10` |

> **注意**：JWT 配置 (`jwt.secret`, `jwt.expire_hours`) 和 Agent 部分配置 (`agent.max_iterations`, `agent.score_threshold`) 仅在 config.yaml 中设置，没有对应的环境变量覆盖。邮箱配置 (`email.*`) 仅通过 config.yaml 设置。

## API 概览

所有 API 返回统一结构：

```json
{"code": 0, "message": "成功", "data": {}}
```

| 模块 | 路由前缀 | 认证 | 说明 |
|------|----------|------|------|
| 健康检查 | `GET /health` | 无 | — |
| 认证 | `/api/v1/auth/*` | 无/需要 | 注册、登录、登出、刷新、验证码、邮箱、密码重置 |
| 用户 | `/api/v1/user/*` | 需要 | Profile CRUD、修改密码 |
| 管理员-用户 | `/api/v1/admin/users/*` | 需要+Admin | 用户管理 |
| 知识库 | `/api/v1/knowledge-bases/*` | 需要 | CRUD + 统计 |
| 文档 | `/api/v1/knowledge-bases/:id/documents`、`/api/v1/documents/*` | 需要 | 上传、列表、下载、删除、处理、版本、重建索引 |
| 分块 | `/api/v1/chunks/:id` | 需要 | 详情 |
| 问答 | `/api/v1/chat/sessions/*` | 需要 | 会话 CRUD + SSE 流式消息 |
| 管理-会话 | `/api/v1/admin/sessions/*` | 需要+Admin | 会话管理 |
| 搜索 | `/api/v1/search` | 需要 | 统一搜索 |
| 模型 | `/api/v1/models`、`/api/v1/user/model-configs` | 需要 | 系统模型 + 用户自定义 |
| 同步 | `/api/v1/sync-sources/*`、`/api/v1/sync-jobs/*`、`/api/v1/sync-items/*` | 需要 | 同步源/任务/文件项 |
| 钉钉 | `/api/v1/dingtalk/*` | 需要 | OAuth、工作空间、节点 |
| 存储 | `/api/v1/storage/quota` | 需要 | 配额查询 |
| 工具 | `/api/v1/admin/tool-types/*`、`/api/v1/user/tool-configs` | 需要 | 工具管理 + 用户配置 |

## 测试

```bash
go test ./...
```

## 更多文档

- [架构说明](docs/architecture.md) — 分层架构、RAG 管线、Chat 双模式、Tool 系统
- [开发指南](docs/DEVELOPMENT.md) — 分层职责、新增模块流程、提交前检查
- [产品需求文档](docs/PRD.md) — 功能需求、业务规则、边界条件
