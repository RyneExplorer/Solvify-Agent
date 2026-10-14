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

### 常用检查

```bash
go test ./...
go fmt ./...
go vet ./...
```

## 分层职责

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

**缓存装饰**：高频读取的仓库（Model、ToolType、UserModelConfig、UserToolConfig）使用 `RepositoryCached` 装饰器，采用 Redis 写入失效 + TTL 兜底策略。

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

- 管理员：ToolType CRUD → ToolProvider CRUD（关联 ProviderRegistry 校验）
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

`TestContextConversationLiveHTTP` 的第12轮原58525字符样本现在应在入口拒绝，不进入上下文并且不新增持久消息；此前JSON/HTML记录仍是未限制版本的历史证据。本轮入口限制的离线回归不会自动重新运行收费的13轮真实模型评估。

普通测试包含 24 并发、192 请求的用户隔离与超限终态检查，以及最终模型输入的精确边界、Service 提示和 HTTP SSE 错误帧检查。模型使用模拟实现或本地 HTTP 服务，不访问真实模型。

120 轮连续会话与 1000 轮历史测试耗时较长，需显式启用（PowerShell）：

```powershell
$env:SOLVIFY_CONTEXT_STRESS_TEST = '1'
go test -p 1 -count=1 ./internal/agent -run '^TestContextStability' -v -timeout 300s
Remove-Item Env:SOLVIFY_CONTEXT_STRESS_TEST
```

上述大历史测试覆盖 quick 与默认旧式 deep 执行路径，不代表 PlanExecute、真实数据库负载或真实模型回答质量验收。并发样本使用分行文本；连续无分隔长中文的 token 计数仍存在性能风险。真实模型摘要延迟需要单独验证，不能以离线通过代替。

单会话真实 HTTP 联调入口为 `TestContextConversationLiveHTTP`，显式设置 `SOLVIFY_CONTEXT_CONVERSATION_LIVE_TEST=1` 后运行 `go test -race -p 1 -count=1 ./internal/api/v1/chat -run '^TestContextConversationLiveHTTP$' -v -timeout 1000s`。它使用真实 DeepSeek、数据库和 Redis，创建并清理临时 schema/key，在同一会话连续提问 13 次，逐轮通过 HTTP 读回原文，保存过程到 `tmp/context-conversation-live.json`。窗口局部限制为8192、输出1024以触发压力，不更改生产配置；真实调用有费用，普通CI默认跳过。第11轮压力样本可能因预算不足拒绝，事实和回答质量断言可能失败，必须记录实际结果，不能以工程门禁通过替代真实验收。
