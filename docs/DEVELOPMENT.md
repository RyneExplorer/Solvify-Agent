# 开发指南

本文说明在 Solvify-Agent 中新增或维护功能时应遵循的本地开发流程。项目采用 Go + Gin + Eino，当前按 Controller、Service、Repository、DTO、Entity、Agent 能力层组织。新增能力必须先明确模块边界，再按现有目录和职责落地。

## 本地准备

安装依赖：

```bash
go mod tidy
```

启动服务：

```bash
go run cmd/server/main.go
```

常用检查：

```bash
go test ./...
go fmt ./...
go vet ./...
```

如果当前环境不能写默认 Go 缓存目录，可临时指定缓存目录后再测试：

```powershell
$env:GOMODCACHE="$env:TEMP\solvify-gomod"
$env:GOCACHE="$env:TEMP\solvify-gobuild"
go test ./...
```

## 分层职责

### Controller

Controller 位于 `internal/api/v1/<module>`，只负责：

- 读取请求上下文，例如 `X-User-ID`
- 绑定 request DTO
- 调用 Service
- 使用 `pkg/response` 返回统一响应

Controller 不直接访问数据库，不直接依赖 Agent 内部结构，不编排 RAG、Tool、LLM 流程。
Controller 中持有的 Service 字段按 `模块名Svc` 命名，例如 `knowledgeBaseSvc`、`storageSvc`。

### 上下文命名

- Gin 请求上下文统一命名为 `c`，例如 `func (ctrl *Controller) Create(c *gin.Context)`
- Go 协程和链路上下文统一命名为 `ctx`，例如 `func (svc *Service) Create(ctx context.Context, ...)`
- Controller 调用 Service 时使用 `c.Request.Context()` 传递链路上下文

### Service

Service 位于 `internal/service`，负责业务用例编排：

- 执行业务校验
- 调用 Repository、Agent 或外部能力适配器
- 做 DTO、Entity、Agent 入参出参之间的转换
- 透传或转换业务错误

Service 不直接写 GORM 查询，不直接拼 SQL，不处理 HTTP 细节。
Service 中持有的 Repository 字段按 `模块名Repo` 命名，例如 `knowledgeBaseRepo`、`storageQuotaRepo`。

### Repository

Repository 位于 `internal/repository`，负责数据访问：
接口文件命名为 `xxx_interface.go`，实现文件命名为 `xxx_repository.go`。
- 封装 GORM 查询
- 返回 Entity 或基础统计结果
- 屏蔽 `gorm.ErrRecordNotFound` 等存储细节

Repository 不写 HTTP、DTO 转换、权限判断、业务编排和日志输出。

### DTO

- 请求 DTO 放在 `internal/model/dto/request`
- 响应 DTO 放在 `internal/model/dto/response`
- DTO 表达 HTTP API 边界，不直接复用 Entity
- 不为临时内部变量创建 DTO

### Entity

Entity 位于 `internal/model/entity`，只表达数据库表字段和表名映射。Entity 不绑定 HTTP 请求语义，不写业务流程。

### Agent 能力层

Agent 相关逻辑位于：

- `internal/agent`：Agent 编排
- `internal/rag`：知识检索能力
- `internal/tool`：工具调用
- `internal/llm`：LLM 客户端封装

Controller 不直接调用这些包，业务入口统一经过 Service。

## 新增业务模块流程

### 1. 明确模块边界

新增接口前先确认模块归属：

- 知识资产建设链路：用户、知识库、文档、在线编辑、多源同步、向量入库、存储配额
- 智能应用与运营链路：问答、检索模式、Agent、Wiki、模型配置、搜索工具、会话、后台日志

接口路径必须按模块分组，不直接堆在 `/api/v1`。

### 2. 定义 Entity

数据库表映射放在 `internal/model/entity`。

```go
package entity

// KnowledgeBase 映射知识库主表
type KnowledgeBase struct {
	ID     string `gorm:"column:id;type:uuid;primaryKey"`
	UserID string `gorm:"column:user_id;type:uuid;not null"`
	Name   string `gorm:"column:name;size:128;not null"`
}

// TableName 返回知识库表名
func (KnowledgeBase) TableName() string {
	return "knowledge_bases"
}
```

新增或调整表结构时，同步更新 `scripts/init_knowledge_schema.sql`。

### 3. 定义 DTO

请求结构放在 `internal/model/dto/request`，响应结构放在 `internal/model/dto/response`。

```go
package request

// CreateKnowledgeBaseRequest 创建知识库请求
type CreateKnowledgeBaseRequest struct {
	Name        string `json:"name" binding:"required,max=128"`
	Category    string `json:"category" binding:"max=64"`
	Description string `json:"description"`
}
```

### 4. 定义 Repository

Repository 负责数据访问，Service 通过接口依赖 Repository。

```go
type knowledgeBaseRepository interface {
	Create(ctx context.Context, kb *entity.KnowledgeBase) error
	FindNormal(ctx context.Context, userID, kbID string, status int16) (entity.KnowledgeBase, bool, error)
}
```

实现放在 `internal/repository`：

```go
// Create 创建知识库记录
func (r *KnowledgeBaseRepository) Create(ctx context.Context, kb *entity.KnowledgeBase) error {
	return r.db.WithContext(ctx).Create(kb).Error
}
```

### 5. 定义 Service

Service 只做业务用例编排和转换。

```go
// Create 创建本地知识库
func (s *KnowledgeBaseService) Create(ctx context.Context, userID string, req request.CreateKnowledgeBaseRequest) (response.KnowledgeBaseResponse, error) {
	kb := entity.KnowledgeBase{
		UserID: userID,
		Name:   req.Name,
		Status: 1,
	}
	if err := s.repo.Create(ctx, &kb); err != nil {
		return response.KnowledgeBaseResponse{}, err
	}
	return knowledgeBaseResponse(kb), nil
}
```

不要在 Service 中写 `db.Where(...)`、`db.Create(...)`、`db.Table(...)` 等数据访问代码。

### 6. 定义 Controller 和 routes

模块目录使用 `internal/api/v1/<module>`，常见结构：

```text
internal/api/v1/knowledgebase
  -> controller.go
  -> routes.go
```

路由文件只负责注册当前模块路由：

```go
// RegisterRoutes 注册知识库模块路由
func (ctrl *Controller) RegisterRoutes(router *gin.RouterGroup) {
	group := router.Group("/knowledge-bases")
	group.POST("", ctrl.Create)
	group.GET("", ctrl.List)
}
```

### 7. 接入应用装配

在 `internal/app/app.go` 中创建 Repository 和 Service：

```go
knowledgeBaseRepo := repository.NewKnowledgeBaseRepository(a.db)
a.knowledgeBaseService = service.NewKnowledgeBaseService(knowledgeBaseRepo)
```

在 `internal/api/router.go` 中添加 Controller 字段、构造参数和路由注册。

## 当前模块维护要点

### 问答模块

- 路由：`POST /api/v1/qa/ask`
- Controller 位于 `internal/api/v1/qa`
- Service 位于 `internal/service/chat_service.go`
- Agent 编排位于 `internal/agent/knowledge.go`
- 当前 RAG 使用内存检索示例，后续替换向量数据库时应通过 Service/Repository/能力层边界接入

### 知识库管理

- 路由：
  - `POST /api/v1/knowledge-bases`
  - `GET /api/v1/knowledge-bases`
  - `GET /api/v1/knowledge-bases/:id`
  - `PUT /api/v1/knowledge-bases/:id`
  - `DELETE /api/v1/knowledge-bases/:id`
  - `GET /api/v1/knowledge-bases/:id/stats`
- 当前用户先从 `X-User-ID` 获取，后续接入认证中间件时替换该入口
- 本地知识库使用 `source_type = local`
- 删除使用软删除：`status = 2`，记录 `deleted_at` 和 `delete_expired_at`

### 存储配额

- 路由：`GET /api/v1/storage/quota`
- 默认单用户总配额为 10GB
- 单文件大小限制为 100MB
- 查询配额时优先读取 `storage_quotas`，没有记录时返回默认值，不在 GET 中隐式写库

### 文档处理

后续文档模块应覆盖：

- 上传到指定知识库
- 文件类型和大小校验
- 文档状态流转
- 解析、分块、向量化、索引建立
- 删除保留 30 天

文档状态建议保持：

```text
1 已上传
2 处理中
3 已就绪
4 处理失败
5 已删除
```

### 文档检索

文档检索是成员 A 提供给成员 B 的关键边界。建议接口保持在检索模块：

```text
POST /api/v1/retrieval/search
POST /api/v1/knowledge-bases/:kb_id/retrieval/search
POST /api/v1/documents/:id/retrieval/search
```

检索接口应返回来源文档、页码、章节、chunk 内容和相关度信息。

### 在线编辑

在线编辑应基于 `document_versions` 保存历史版本。保存新版本后触发重新分块和重新向量化，不应直接覆盖历史内容。

### 多源同步

多源同步后置实现。同步知识库应保持只读，来源平台信息写入知识库和文档来源字段。同步失败要记录失败原因，不影响已有数据。

## 数据库表

当前知识资产链路表结构位于 `scripts/init_knowledge_schema.sql`：

- `users`：用户基础表
- `knowledge_bases`：知识库主表
- `documents`：文档主表
- `document_versions`：文档版本表
- `document_chunks`：文档分块和向量表
- `document_processing_jobs`：文档处理任务表
- `storage_quotas`：用户存储配额表

表结构变更后要同步检查 Entity、Repository 查询和测试。

## 响应和错误

API 统一使用 `pkg/response`：

```go
response.Success(ctx, data)
response.BizError(ctx, err)
response.BadRequest(ctx, "参数错误")
```

业务错误使用 `pkg/errors`。新增错误码时同时补充默认中文错误消息。

## 日志规范

- 日志初始化由 `internal/app` 负责
- 业务日志使用 `pkg/logger`
- 日志文本使用简洁中文
- 不打印密钥、Token、密码、完整连接串等敏感信息

## 配置规范

- 配置结构位于 `pkg/config/config.go`
- 默认配置位于 `configs/config.yaml`
- 新增配置项时同步更新配置结构、默认值和 README 配置说明
- 真实密钥、Token、连接串不得写入文档或日志

## 文档同步

新增、删除或调整接口时，至少同步检查：

- `README.md`
- `docs/architecture.md`
- `docs/PRD.md`
- `docs/模块划分.md`
- 相关 SQL 或接口说明文档

文档必须以真实路由、真实 Service、真实表结构为准，不写尚未实现的能力为“已完成”。

## 测试规范

Go 代码修改后默认运行：

```bash
go test ./...
```

新增或调整路由时，应补充路由测试，至少覆盖：

- 正常路径
- 参数格式错误
- 关键业务错误
- 不应存在或已废弃路径

涉及数据库访问时：

- Repository 测试验证 GORM 查询和关键 SQL 行为
- Service 测试使用假 Repository 验证业务编排
- Controller 测试关注 HTTP 请求、响应码和路由行为

当前阶段可以为验证临时编写测试文件，但完成开发并确认测试通过后，不保留本轮新增的临时测试文件和仅测试使用的依赖。

不要为了覆盖率写无意义测试，不要对实现细节做脆弱断言。

## 提交前检查

```bash
go fmt ./...
go test ./...
go vet ./...
```

如果只改文档，至少确认 Markdown 可读、路径存在、接口路径与路由文件一致。

## 常见禁止项

- 禁止在 Service 层直接写数据库查询
- 禁止 Controller 直接调用 Repository 或 Agent
- 禁止 DTO 和 Entity 混用
- 禁止新增一次性工具函数或空壳抽象
- 禁止为不可能发生的场景添加兜底逻辑
- 禁止输出密钥、Token、密码、完整连接串
