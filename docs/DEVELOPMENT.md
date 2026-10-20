# 开发指南

本文说明 Solvify-Agent 的开发步骤。开发约束统一见 [AGENTS.md](../AGENTS.md)，架构见 [architecture.md](architecture.md)。项目采用 **Go + Gin + GORM + Eino + PostgreSQL（pgvector）+ Redis**；现状描述不表示已满足全部新增约束。

## 本地准备

### 环境依赖

- Go 最低版本以 `go.mod` 为准（当前 1.25.0）；部署镜像当前默认 1.26.2，调整版本时同步检查 CI、镜像和兼容性
- Python 3.10+（docx/pdf 文档解析）
- PostgreSQL 15+（需启用 pgvector 扩展）
- Redis 7+
- （可选）钉钉企业应用凭证

### 安装与启动

```bash
# 安装依赖
go mod download
pip install -r pkg/documentparser/python/requirements.txt

# 对新数据库执行初始化脚本（替换连接参数）
psql -h localhost -U <数据库用户> -d <数据库名> -f scripts/init_knowledge_schema.sql

# 启动服务
go run ./cmd/server
```

当前没有 `cmd/seed` 入口。已有数据库升级需单独确认升级 SQL，不能将新库初始化等同于迁移；依赖变更后再运行 `go mod tidy` 并检查 diff。

用户只配置MCP连接，管理员维护第三方MCP预置；普通HTTP工具入口与执行器已移除。已有库在部署新版本前执行：

```bash
psql -v ON_ERROR_STOP=1 -h localhost -U <数据库用户> -d <数据库名> -f scripts/migrations/20261017_mcp_connections.sql
psql -v ON_ERROR_STOP=1 -h localhost -U <数据库用户> -d <数据库名> -f scripts/migrations/20261019_mcp_templates.sql
```

两步脚本均可重复执行：第一步升级用户连接（已执行过可跳过）；第二步创建独立MCP模板，保留原模板和连接ID、配置与工具前缀，重建用户绑定外键。新库直接初始化新表，不再创建工具类型和供应商表。只迁移旧MCP配置并保留原ID，不覆盖后续编辑、不重新导入已删除连接。没有迁移标记的旧库首次升级前，应检查同一用户是否重复绑定同一预置；新表对该组合建立唯一约束，冲突会使整个迁移回滚。启动检查MCP连接和模板表是否存在，不自动升级业务schema。

若启动提示 `MCP连接表不存在` 或 `MCP模板表不存在`，表示该进程连接的数据库尚未完成上述升级；不能对已有库重跑完整初始化脚本，也不能跳过缺表检查。先核对最终生效的数据库目标（根目录 `.env` 和进程环境变量可能覆盖YAML），在该库执行迁移后再启动。可用 `SELECT current_database(), to_regclass('public.mcp_connections'), to_regclass('public.mcp_templates');` 核对目标与表是否存在；升级完成后，`app_schema_migrations` 应有版本 `20261017_mcp_connections` 与 `20261019_mcp_templates`。

用户点击“保存”时，个人连接地址、认证请求头或预置字段会持久写入PostgreSQL的 `mcp_connections`；重启后按用户归属读取。仅点击“测试连接”不会保存草稿。升级时旧MCP配置从 `user_tool_configs` 迁入新表，保留ID、用户配置及启停状态，旧表不会被此迁移清空。

配置方式、搜索API Key与接口见[MCP配置](mcp-configuration.md)。内置工具使用 `tools.default_timeout`（默认30秒，`TOOLS_DEFAULT_TIMEOUT`可覆盖）；MCP分别配置connect_timeout/call_timeout，默认30/300秒，旧timeout兼容读取。部署预置的管理员超时在启动同步时保留，详见[工具执行时限](tool-timeouts.md)。

### 聊天消息向量任务

消息正文与 `embedding_status=queued` 在同一条INSERT中提交，HTTP保存不等待向量服务。`database.postgres.enable_pgvector=false` 时不登记也不执行任务。App管理后台Worker，停止HTTP后取消并等待Worker归还任务，再关闭数据库等依赖。启动只消费已登记任务，不扫描或自动登记旧消息；当前问题用于语义召回的查询向量仍按请求计算，失败降级关键词。

已有库在启动此版本前执行（新库初始化脚本已包含字段、约束和索引）：

```bash
psql -v ON_ERROR_STOP=1 -h localhost -U <数据库用户> -d <数据库名> -f scripts/migrations/20261017_message_embedding_jobs.sql
```

迁移可重复执行，旧消息默认 `none`，不改正文和已有向量。新任务状态为 `queued → running → succeeded/failed`，可重试故障退避后回到 `queued`。默认 `embedding.message_jobs`：并发2、轮询2秒、单次超时10秒、最多尝试3次、初始退避15秒（指数增长）；配置项见示例YAML。后台请求直接使用可取消的Embedding客户端，不经过查询侧共享缓存。Worker租约为单次超时加30秒，通过令牌拒绝过期写入；崩溃占用一次尝试，正常停机归还时不消耗次数。网络、超时和HTTP408/429/500/502/503/504重试，认证、非法配置、维度或数值错误直接失败。失败不会删除正文，达到上限后不会在重启时无限重试。

确认向量服务可用后，从项目根目录显式登记历史缺向量消息（读取 `CONFIG_PATH`/默认配置及环境覆盖，必须匹配数据库名）：

```bash
go run ./cmd/backfill-message-embeddings --database <数据库名> --limit 1000
# 修复故障后，同时重置并重新登记失败任务：
go run ./cmd/backfill-message-embeddings --database <数据库名> --limit 1000 --retry-failed
```

命令只入队，服务运行后异步处理；已嵌入、空文本和处理中任务不重复入队，每次最多10000条。运维可通过 `SELECT embedding_status, count(*) FROM chat_messages GROUP BY embedding_status;` 查看状态，不记录消息正文或上游敏感错误。隔离真实PostgreSQL验证使用 `SOLVIFY_EMBEDDING_DB_TEST=1 go test -race ./internal/repository ./internal/app -run 'TestMessageEmbedding(PostgresRecovery|AppPostgresLifecycle)' -count=1 -v`；测试创建并清理临时schema，App测试向量HTTP为本地模拟，不发送真实聊天。

2026-10-19验收：独立数据库、真实浏览器、真实聊天模型与 `bge-m3`（1024维）验证发送、刷新、用户/助手向量入库；受控HTTP503验证3次后失败且正文保留；强制结束测试进程后由新进程在租约自然过期时恢复，尝试次数1→2并完成。维护命令分别验证旧消息登记和 `--retry-failed` 后收敛。隔离数据库竞争/事务/迁移/独立进程测试、相关六包race、全量Go测试/vet、前端31项及构建通过。新库基线既有会话澄清字段和checkpoint表缺口另行处理，浏览器测试仅在临时库补齐会话字段；本轮不代表语义召回质量或checkpoint恢复验收。

### 常用检查命令

```bash
go test ./...
go fmt ./...
go vet ./...
```

## 分层职责

### 模型调用重试

直接创建的 Eino ChatModelAgent（快速、普通深度及 PlanExecute 专业子 Agent）通过 `ModelRetryConfig` 重试临时模型故障，`agent.model_max_retries` 默认 3，可用 `AGENT_MODEL_MAX_RETRIES` 覆盖；允许 0–5，0 关闭。3 次重试表示单个模型节点最多 4 次调用，不是整个 Agent 重跑。

通用临时故障重试仅处理网络超时、连接重置/拒绝、意外 EOF 及 HTTP 408/429/500/502/503/504；认证、非法参数、上下文预算不足及请求取消不走该重试。已产生任何模型消息的失败流不重试，避免重复输出；沿用 Eino 默认指数退避与抖动，以及原请求 context 和输入预算校验，不重新执行已完成工具。

上下文有独立的有界处理：调用前先减少可选材料，再按总窗口与独立输入上限调整本次输出预留，最低512 tokens（原额度更小时不提高），实际请求参数同步调整。端点明确拒绝上下文且没有输出时，允许降低本次输出预留再调用一次；流已返回后不做此恢复。专业任务的额外调用计入原有额度并随 checkpoint 保存，控制节点单次最多恢复一次。此策略不修改模型持久配置、不扩大已知窗口；必要输入仍超限时明确失败。

当前 Eino v0.9.14 的 prebuilt PlanExecute Planner/Executor/Replanner 配置未暴露 `ModelRetryConfig`，这些控制节点没有通用临时故障重试，但同样经过调用级输出分配与单次上下文恢复。本项不调整 HTTP 客户端超时。

深度计划的 `deep_max_plan_execute_rounds` 限制执行批次，页面显示“执行批次”；它与 `deep_max_subagent_iterations` 的专业任务模型调用额度独立。后者默认12次，包含模型重试、审批/澄清调用和最终回答；显式配置仍生效。每次调用告知剩余额度，最后一次必须处理已有结果，继续请求工具则在工具执行前以额度耗尽终止。该计数与原上限随子 checkpoint 保存，恢复不重置；旧 checkpoint 缺少新增计数时继续由 Eino 已保存的剩余迭代次数约束。它不是用户对话轮数，工具并行调用也不等于多次模型调用。已有部署显式配置为3时需要调整配置并重启服务。

失败回复在同一条消息中保留部分正文、任务计划及错误说明；历史消息通过 `execution_error` 读取已保存的失败原因，正文及既有 HTTP 状态和业务码不变。

PlanExecute 保留 Eino 官方 `plan`/`respond`。研究只提供事实与来源，审查只提供要点、大纲和证据缺口（通常600中文字以内），Replanner 生成最终正文。普通回答默认结论前置、3—5个重点、通常300—600中文字；明确要求详细内容、代码或指定篇幅时按需展开，不用字符串裁切压短正文。快速、普通深度、控制节点、专业任务及最终流必须有正常且与文本/工具形态一致的 `finish_reason`；`length`、`content_filter`、缺失/未知/不匹配结束分别返回输出上限、过滤或模型响应异常，禁止异常工具请求实际执行。部分最终正文与错误说明按既有失败记录保存，不发送成功终态，也不自动续写或重试；运行摘要结束异常时保留原上下文。

相关离线验证：`go test ./internal/agent ./internal/llm -run 'Test(ModelCompletion|SingleAgentCompletion|ContextRecovery|AdaptiveOutput|FitOutput|PlanExecute|PETask|QuickSummary)'` 与 `go test ./internal/service -run TestAgentModePreservesInterruptedAnswer`。模拟模型结合真实 Engine/Eino 及本地 HTTP 验证终态、实际输出参数、实时首段、恢复预算、工具边界、部分结果及保存行为；普通/详细/证据缺口固定样本只验证提示词装配，实际文案质量、时延和 token 收益仍需独立真实模型评估。

### Controller（`internal/api/v1/<module>`）

每个模块含 `controller.go` 和 `routes.go`，只负责：

- 读取请求上下文、路径参数
- 绑定 request DTO
- 调用 Service
- 使用 `pkg/response` 返回统一响应

**禁止**：直接访问数据库、直接调用 Repository、编排 Agent/RAG/Tool 流程。

**命名约定**：
- Gin 上下文 → `c`
- Service 字段 → `模块名Svc`（如 `knowledgeBaseSvc`）
- Controller 调用 Service 时传递 `c.Request.Context()`

### Service（`internal/service`）

Service 实现文件使用 `xxx_service.go`；需要独立接口文件时使用 `xxx_interface.go`。接口按真实消费需求设计，具体约束见 AGENTS.md。

- 执行业务校验
- 调用 Repository、Agent 或外部能力适配器
- Entity ↔ DTO 转换
- 透传或包装业务错误

**禁止**：直接写 GORM 查询、直接拼 SQL、处理 HTTP 细节。

**命名约定**：Repository 字段 → `模块名Repo`（如 `knowledgeBaseRepo`）。

### Repository（`internal/repository`）

接口文件 `xxx_interface.go`，实现文件 `xxx_repository.go`：

- 封装 GORM 查询
- 返回 Entity 或统计结果
- 屏蔽 `gorm.ErrRecordNotFound`

**禁止**：写 HTTP、DTO 转换、权限判断、业务编排、日志输出。

**缓存装饰**：高频读取的仓库（Model、UserModelConfig）使用 `RepositoryCached` 装饰器，采用 Redis 写入失效 + TTL 兜底策略。

### DTO（`internal/model/dto`）

- 请求 DTO → `internal/model/dto/request/`
- 响应 DTO → `internal/model/dto/response/`
- 表达 HTTP API 边界，**不直接复用 Entity**

### Entity（`internal/model/entity`）

- 数据库表字段和表名映射
- **不绑定 HTTP 请求语义，不写业务流程**

### Agent 能力层

| 包 | 职责 |
|----|------|
| `internal/agent` | 快速 Agent 与深度 plan-execute 编排（引擎、执行、回调、提示词、类型定义） |
| `internal/rag` | RAG 检索管线（Retriever 接口 → HybridRetriever → 装饰器链） |
| `internal/tool` | 工具系统（Provider 注册表 → HTTP Provider → AgentTool 适配器） |
| `internal/llm` | LLM 客户端工厂（OpenAI 兼容，连接池复用，有界 LRU 客户端缓存） |

Controller **不直接调用**这些包，业务入口统一经过 Service。

LLM 客户端在首次使用模型配置时按需创建，后续相同配置复用进程内缓存（最多 64 个条目，LRU 淘汰）。启动时不预热模型客户端；创建客户端不发起模型请求，也不验证远端连接。MCP按实际用户连接按需初始化和发现工具，启动时不预热MCP工具。

## 新增业务模块流程

### 1. 明确模块归属

两条核心链路：

- **知识资产链路**：用户 → 知识库 → 文档 → 在线编辑 → 多源同步 → 向量入库 → 存储配额
- **智能应用链路**：问答 → 检索模式 → Agent → 模型配置 → 搜索工具 → 会话 → 后台管理

### 2. 定义 Entity

在 `internal/model/entity/` 新建文件：

```go
type KnowledgeBase struct {
    ID     string `gorm:"column:id;type:uuid;primaryKey"`
    UserID string `gorm:"column:user_id;type:uuid;not null"`
    Name   string `gorm:"column:name;size:128;not null"`
}

func (KnowledgeBase) TableName() string { return "knowledge_bases" }
```

新增表时同步更新 `scripts/init_knowledge_schema.sql`。

### 3. 定义 DTO

```go
// request/create_knowledge_base.go
type CreateKnowledgeBaseRequest struct {
    Name        string `json:"name" binding:"required,max=128"`
    Description string `json:"description"`
}

// response/knowledge_base.go
type KnowledgeBaseResponse struct {
    ID          string `json:"id"`
    Name        string `json:"name"`
    Description string `json:"description"`
}
```

### 4. 定义 Repository（接口 + 实现）

```go
// internal/repository/knowledge_base_interface.go
type KnowledgeBaseRepo interface {
    Create(ctx context.Context, kb *entity.KnowledgeBase) error
    GetByID(ctx context.Context, userID, kbID string) (*entity.KnowledgeBase, error)
}

// internal/repository/knowledge_base_repository.go
func (r *KnowledgeBaseRepository) Create(ctx context.Context, kb *entity.KnowledgeBase) error {
    return dbFor(ctx, r.db).Create(kb).Error
}
```

### 5. 定义 Service（接口 + 实现）

```go
// internal/service/knowledge_base_service.go
func (s *knowledgeBaseService) Create(ctx context.Context, userID string, req request.CreateKnowledgeBaseRequest) (response.KnowledgeBaseResponse, error) {
    kb := &entity.KnowledgeBase{UserID: userID, Name: req.Name}
    if err := s.repo.Create(ctx, kb); err != nil {
        return response.KnowledgeBaseResponse{}, err
    }
    return toKBResponse(kb), nil
}
```

### 6. 定义 Controller 和路由

```text
internal/api/v1/knowledgebase/
  ├── controller.go
  └── routes.go
```

```go
// routes.go
func (ctrl *Controller) RegisterRoutes(router *gin.RouterGroup) {
    group := router.Group("/knowledge-bases")
    group.POST("", ctrl.Create)
    group.GET("", ctrl.List)
}
```

### 7. 接入应用装配

在 App 的依赖装配处创建服务，再交给 Router/Controller。以下为局部示意：

```go
knowledgeBaseRepo := repository.NewKnowledgeBaseRepository(a.postgresqlDB)
knowledgeBaseSvc := service.NewKnowledgeBaseService(knowledgeBaseRepo)
// 将 knowledgeBaseSvc 传给路由构造函数，参数以当前 Router 定义为准。
```

在 `internal/api/router.go` 中添加 Controller 字段并注册路由。

## 现有模块维护要点

### 认证模块（`internal/api/v1/auth`）

- 路由：`POST /api/v1/auth/register`、`POST /api/v1/auth/login`、`POST /api/v1/auth/refresh`、`POST /api/v1/auth/logout`、`GET /api/v1/auth/captcha`、`POST /api/v1/auth/email/code`、`POST /api/v1/auth/password/reset`
- JWT Blacklist：登出时 Token 的 SHA-256 摘要写入 Redis，TTL 为 Token 剩余有效期；刷新与用户状态的撤销闭环仍需按规范完善
- 图形验证码：内存存储；邮箱验证码：Redis 存储，5 分钟有效期
- 密码：bcrypt 加密

### 用户模块（`internal/api/v1/user`）

- 路由：`GET /api/v1/user/profile`、`PUT /api/v1/user/profile`、`POST /api/v1/user/change-password`
- 状态：1=正常, 2=禁用, 3=已注销, 4=待验证
- 管理端路由（RequireAdmin）：用户 CRUD + 状态管理 + 密码重置

### 知识库管理（`internal/api/v1/knowledgebase`）

- 路由：CRUD + `GET /:id/stats`
- 来源类型：`local`（自建）/ `sync`（同步）/ `web_search`（联网搜索）
- 删除：软删除（status=2），保留 30 天
- 同步知识库为只读，不可手动编辑/删除

### 文档处理（`internal/api/v1/document`）

- 路由：上传、列表、下载、删除、处理、版本管理、重建索引
- 状态流转：1 已上传 → 2 处理中 → 3 已就绪 / 4 处理失败 / 5 已删除
- 可解析格式：Txt / Markdown / HTML / CSV / JSON / DOCX / PDF（PDF 仅支持可提取文本）
- 可上传但暂不解析：DOC / Excel / PPT / 图片
- 处理管线：解析 → 分块（Chunk）→ 向量化（Embedding）→ 建立索引
- 单文件 ≤ 100MB，自动计算 Token 数

### 文档分块（`internal/api/v1/chunks`）

- 分块存储 `document_chunks` 表，含 1024 维 pgvector embedding
- 向量索引：IVFFlat，余弦距离
- 关键词：中文分词（gse）+ 去停用词 + GIN 索引

### 智能问答（`internal/api/v1/chat`）

- 快速模式（`search_mode=quick`）：快速 Agent 执行，按工具能力检索与生成，输出 SSE 事件流
- 深度模式（`search_mode=smart-reasoning`）：计划生成 → 任务执行/必要重规划 → 汇总，支持工具审批与 checkpoint 恢复
- 来源标注：`<kb>` 蓝色知识库，`<web>` 绿色网络
- 上下文传递：历史消息 + 模型配置 + 知识库 ID

### 模型配置（`internal/api/v1/model`）

- 系统模型：管理员 CRUD（`model_service.go`）
- 用户自定义模型：用户 CRUD（`user_model_config_service.go`）
- 支持厂商：OpenAI / DeepSeek / 智谱 / 通义 等 OpenAI 兼容 API
- LLM 客户端：有界 LRU 缓存，HTTP 连接池复用
- Redis 缓存：ModelRepositoryCached、UserModelConfigRepositoryCached

### 钉钉集成（`internal/api/v1/dingtalk`）

- OAuth 登录/绑定/解绑
- 工作空间列表、节点列表
- 文档导出 → 自动创建同步知识库
- 同步：全量/增量 Job，状态追踪

### 多源同步（`internal/api/v1/sync`）

- SyncSource CRUD：绑定平台和知识库
- SyncJob：全量/增量执行，success/failure 计数
- SyncItem：外部文件树，支持选择性导入
- 导入时自动解析 → 分块 → 向量化

### 存储配额（`internal/api/v1/storage`）

- 默认 10GB / 用户，`storage_quotas` 表存储
- 上传前检查配额，不足返回错误
- GET 不隐式写库（无记录返回默认值）

### 搜索（`internal/api/v1/search`）

- `GET /api/v1/search?q=...&type=chat|document|all`
- 统一搜索：chat_messages.content + document_chunks.content
- 支持全文检索

### 工具管理（`internal/api/v1/tool`）

- 管理员：MCPTemplate CRUD → MCP客户端静态配置校验；探测只发现工具，不执行tools/call。
- 用户：查看模板 → 创建/管理 UserToolConfig
- Provider 类型：`http`（Template 驱动）、`mcp`（连接池与工具发现）

### 后台管理（`internal/api/v1/chat` admin routes）

- 管理员会话列表、删除、清理过期
- 管理员用户管理（`internal/api/v1/user` admin routes）

## 数据库表

所有表结构位于 `scripts/init_knowledge_schema.sql`：

| 表 | 说明 |
|----|------|
| `users` | 用户基础表 |
| `knowledge_bases` | 知识库主表 |
| `documents` | 文档主表 |
| `document_versions` | 文档版本表 |
| `document_chunks` | 文档分块和向量表（pgvector） |
| `document_processing_jobs` | 文档处理任务表 |
| `storage_quotas` | 用户存储配额表 |
| `sync_sources` | 同步源配置表 |
| `sync_jobs` | 同步任务表 |
| `sync_items` | 同步文件项表 |
| `dingtalk_user_bindings` | 钉钉用户绑定表 |

新库使用初始化 SQL；已有库升级按明确的 schema 升级步骤执行。启动时有部分 schema/索引检查，不应将这些检查视为完整迁移体系。

## 响应和错误

API 统一使用 `pkg/response`：

当前业务错误使用 HTTP 200 + 响应体业务码，测试与监控需检查业务码。改变该契约需单独规划客户端迁移。

```go
response.Success(ctx, data)
response.BizError(ctx, err)
response.BadRequest(ctx, "参数错误")
```

业务错误使用 `pkg/errors`，50+ 错误码按类别分组：

| 错误码范围 | 类别 |
|-----------|------|
| 0 | 成功 |
| 4xx | HTTP 通用 |
| 5xx | 服务器错误 |
| 1xxx | 用户 / 认证 |
| 2xxx | 参数校验 |
| 3xxx | RAG |
| 4xxx | 工具 |
| 5xxx | Agent / LLM |
| 6xxx | 知识库 |
| 7xxx | 模型配置 |
| 8xxx | 会话 |
| 9xxx | 文档 / 存储 / 同步 |
| 10xxx | 工具管理 |
| 11xxx | 钉钉 |

新增错误码时同步补充默认中文错误消息。

## 日志规范

- 日志初始化由 `internal/app` 负责
- 业务日志使用 `pkg/logger`（zap + lumberjack）
- JSON 格式，控制台 + 文件双输出
- 日志文本使用简洁中文
- **禁止**打印密钥、Token、密码、完整连接串等敏感信息

## 配置规范

- 配置结构：`pkg/config/config.go`
- 默认值：`config.Default()`
- 主配置：`configs/config.yaml`（含所有节点）
- 示例配置：`configs/config.yaml.example`（不含真实密钥）
- 环境变量模板：`.env.example`
- 配置优先级：**代码默认值 < config.yaml < .env 文件 < 系统环境变量**
- 新增配置项时同步更新：配置结构体 → Default() → config.yaml.example → .env.example → applyEnv()

## 文档同步

新增、删除或调整接口时，至少同步检查：

- `README.md`
- `docs/architecture.md`
- `docs/DEVELOPMENT.md`（本文档）
- `docs/PRD.md`
- `configs/config.yaml.example`
- `.env.example`

文档必须以真实路由、真实 Service、真实表结构为准，不写尚未实现的能力为"已完成"。

## 测试规范

```bash
go test ./...
```

- Repository 测试：验证 GORM 查询和 SQL 行为
- Service 测试：使用假 Repository 验证业务编排
- Controller 测试：关注 HTTP 请求、响应码和路由行为
- 使用 `httptest.NewServer` mock 外部 API（参考 `internal/integration/dingtalk/client_test.go`）

**不要**为覆盖率写无意义测试，**不要**对实现细节做脆弱断言。

## Agent 变更验证流程

专项规则见 [AGENTS.md 的应用 Agent 运行约束](../AGENTS.md#应用-agent-运行约束)。修改 Agent 时按以下步骤交付：

1. 固定代码、模型、提示词、工具配置和评估样本基线，说明修改目标与允许变化的行为。
2. 用模拟模型和工具验证正常路径、非法输出、预算耗尽、权限失败、取消、审批拒绝与重复恢复；新增持久状态用独立进程验证。
3. 修改并发或共享状态时运行相关 `go test -race`；完成 Go 变更后运行 `go test ./...`、`go vet ./...` 并检查 diff。
4. 提示词、模型或检索策略改动比较固定样本上的正确性、引用、召回、耗时和消耗；真实模型结果记录配置与随机性，不替代离线行为测试。
5. 明确实际执行的检查及未覆盖的数据库、外部模型、重启和生产副作用行为；仅修改文档时检查事实、链接和 diff。

现有 CI 已执行 race 测试、vet、构建和部署配置检查；新增质量门禁需落实为可运行的命令，不能仅在文档中宣称存在。

## 模型配置升级

模型能力契约、旧记录确认步骤与可重复 schema 升级见 [模型配置说明](model-configuration.md)。上线前补齐使用中的系统/用户模型能力，不回填猜测窗口；真实模型能力验收与离线协议测试分开。

## 上下文稳定性专项测试

聊天发送入口限制单条 `content` 最多4000个Unicode码点（中文、英文字母和单码点表情各计1），完整JSON请求体最多64KiB。超限返回HTTP200、业务码400和中文提示，在建立SSE、保存消息、摘要、向量化或模型调用之前拒绝；Service直接调用也执行字符上限校验。正常消息不截断、不改写。该上限不等同于模型token额度，合法输入仍须在历史压缩/裁剪后重新核算完整模型输入及输出预留。

`TestContextConversationLiveHTTP` 的第12轮58525字符样本应在入口拒绝，不进入上下文并且不新增持久消息，第13轮继续同一会话。普通离线测试不会自动运行收费的13轮真实模型评估；不同日期的JSON/HTML可能记录不同代码版本，不能混为一次测试。

普通测试包含 4 并发、12 请求的用户隔离、摘要与超限终态检查，以及按项目计数器构造的最终输入边界、Service 提示和 HTTP SSE 错误帧检查。每个并发用户执行1次超限拒绝和2次正常请求，使用真实Engine和项目token计数器；完整24并发、192请求通过下述压力测试开关运行，避免race与覆盖率插桩下将机器吞吐差异误判为功能失败。模型使用模拟实现或本地 HTTP 服务，不访问真实模型。项目BPE与兼容回退计数不等同于所有供应商的官方tokenizer或计费认证。

摘要和记忆正文以独立的 `ContextData` 数据消息进入Agent，而非拼进system；召回和已裁剪的历史片段也标记材料类型，并通过来源ID避免与历史重复。最终预算保护减少可丢普通问答后，仍超硬上限时依次减少可选召回、历史片段和记忆，每步重新计数。当前问题、必要指令、摘要与已识别的失败/拒绝事实保留，仍放不下时明确拒绝，不擅自改写当前问题。工具定义和输出预留另由实际模型请求保护核对。

当前Eino的 `BeforeModelRewriteState` 收到的消息已包含 `GenModelInput` 生成的system，运行摘要水位和预算保护均按实际消息计算，不另外叠加Instruction。`TestContextRunnerCountsSystemInstructionOnce` 用真实Runner验证恰好满足输入上限时不误拒绝。可设置 `SOLVIFY_CONTEXT_REPLAY_REPORT` 为本地失败JSON绝对路径，执行 `go test -p 1 ./internal/agent -run '^TestContextConversationFailureReplay$' -v` 离线重放第11轮；该用例需要含运行摘要的报告，不访问真实模型。

`context_degradation_test.go` 覆盖旧工具轨迹不阻断后续裁剪、失败/审批事实保护、可选材料降级与合法长问题；`context_data_test.go` 验证摘要/记忆的消息角色边界；`context_persistence_test.go` 验证记忆写入错误透传、空白摘要不推进覆盖。数据消息和提示词约束并不等于完全防提示词注入，语义事实正确性需真实模型重复评估。

120 轮连续会话、1000轮历史及24并发192请求测试耗时较长，需显式启用（PowerShell）：

```powershell
$env:SOLVIFY_CONTEXT_STRESS_TEST = '1'
go test -p 1 -count=1 ./internal/agent -run '^TestContextStability' -v -timeout 300s
Remove-Item Env:SOLVIFY_CONTEXT_STRESS_TEST
```

上述大历史测试覆盖 quick 与默认旧式 deep 执行路径，不代表 PlanExecute、真实数据库负载或真实模型回答质量验收。并发样本使用分行文本；另以同一开关运行 `go test -p 1 -count=1 ./pkg/tokenutil -run '^TestContextTokenCountingAtInputLimit$' -v`，验证4000连续中文字符的24并发计数和本机耗时。更大的旧历史/工具材料仍有计数性能风险，本机耗时不是生产SLO。

显式设置 `SOLVIFY_CONTEXT_DB_TEST=1` 后运行 `go test -race -p 1 -count=1 ./internal/repository -run '^TestContextArtifactPostgresMigrationQuotaAndCleanup$' -v -timeout 180s`，验证隔离PostgreSQL schema中的事务回滚、独立连接配额竞争、摘要版本竞争与产物清理。普通测试默认跳过；这不等于生产进程崩溃恢复或真实副作用的恰好执行一次认证。

单会话真实 HTTP 联调入口为 `TestContextConversationLiveHTTP`，显式设置 `SOLVIFY_CONTEXT_CONVERSATION_LIVE_TEST=1` 后运行 `go test -race -p 1 -count=1 ./internal/api/v1/chat -run '^TestContextConversationLiveHTTP$' -v -timeout 1000s`。它使用真实 DeepSeek、数据库和 Redis，创建并清理临时 schema/key，在同一会话连续提问13次，逐轮通过HTTP读回原文，保存过程到 `tmp/context-conversation-live.json`。设置 `SOLVIFY_CONTEXT_REPORT_NAME` 为独立的 `.json` 文件名可避免覆盖先前失败证据。窗口局部限制为8192、输出1024以触发压力，不更改生产配置；真实调用有费用，普通CI默认跳过。第11轮3677字符样本应在可选上下文降级后正常回答；预算或事实断言失败必须保留实际结果。测试每轮等待后台摘要/记忆完成，不代表立即连发、浏览器UI、真实业务工具/MCP、向量服务或生产负载已验收。真实联调一次通过不等于重复稳定性或真实模型race已通过。

### 模型执行策略升级（2026-10-14）

模型配置普通入口仅填写连接信息与部署额度；执行参数统一managed_v1。已有库依次执行`scripts/migrations/20261013_model_limits.sql`、`scripts/migrations/20261014_model_managed_policy.sql`，先备份JSONB并核对记录及非config字段；两个升级可重复执行。后一个仅清理用户执行参数，不改模型额度或凭据，也不为旧记录认证工具能力。契约见`docs/model-configuration.md`。
隔离数据库验证：`SOLVIFY_MODEL_CONFIG_DB_TEST=1 go test -vet=off -p 1 ./scripts/migrations`；普通测试无需该开关。

### 普通模型配置两项收口

用户连接信息之外仅调整上下文窗口与思考模式。none/always/unknown分别禁用、锁定、沿用默认；optional须已识别协议才允许切换。额度/协议资料来自同身份旧配置、管理员完全匹配部署或服务商元数据/已核实目录。用户API不能伪造能力。管理员折叠资料可补第三方输出上限及协议；无明确额度不猜测。编辑测试携带config_id并按当前用户归属读取；当前模式保存前仍实测工具。
本轮沿用JSONB，无新增schema迁移；已核对当前系统1条/用户1条配置与新解析兼容，保留原思考关闭协议。当前契约见docs/model-configuration.md。


## 2026-10-20 文档正文保真修复后的已有数据处理

文档正文现在只统一换行、清除文件头BOM及外层空行，保留数值标点、URL、版本号、Markdown空行/行尾换行标记与代码缩进。HTML按节点提取可读正文并保留段落/表格边界，跳过脚本、样式等非正文节点；关键词仍使用公共分词器，向量与引用使用保真分块。

新上传、笔记创建和新版本自动使用新处理方式。已经入库的受损分块不会因代码更新自动恢复，也不能从`129 95`猜回`129.95`。部署后按文档逐个执行现有 `POST /api/v1/documents/:id/reindex`：此入口读取该用户当前可编辑文档的最新版本正文，先生成全部新分块与向量，再经现有事务替换检索数据，不新增文档版本。若最新版本正文已经受损，先从原始文件恢复保真正文，再创建新版本，不能对受损文本反复重建。

重建会替换当前分块ID，旧回答中的分块引用可能无法继续打开；操作前明确需要重建的文档范围，并保留原始文件和已有版本。此次代码修复未自动批量重建用户文档或修改运行模型配置。

验收命令在本指南既有Go与前端检查之外，可显式运行 `SOLVIFY_KNOWLEDGE_SCOPE_DB_TEST=1 go test ./internal/tool -run TestDocumentToolsPostgresKnowledgeBaseScope -count=1`（随机临时schema，仅本地数据库）；真实模型合成样本使用 `SOLVIFY_CONTEXT_LIVE_TEST=1 go test ./internal/agent -run TestContextLiveDeepSeek -count=1 -v`，包含压缩与两轮引用追问，需模型额度，不在普通CI执行。
