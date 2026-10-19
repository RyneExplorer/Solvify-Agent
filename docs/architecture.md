# 架构说明

Solvify-Agent 采用**分层架构 + 装饰器模式 + 策略模式**，以 `internal/app.App` 为装配中心，集中持有配置、日志、数据库连接、路由和所有服务依赖。

## 整体分层

```text
cmd/server                —— 入口，创建并启动 App
  └── internal/app        —— 装配中心：初始化配置→日志→数据库→依赖→路由→Server
       ├── pkg/config     —— YAML 配置加载 + .env 文件 + 环境变量覆盖
       ├── pkg/logger     —— zap + lumberjack 日志（双输出：控制台 + 文件）
       ├── pkg/database   —— PostgreSQL (GORM + pgvector) + Redis 连接管理
       ├── internal/api   —— Gin Router + 13 个 Controller 模块
       │   └── internal/middleware  —— Auth、CORS、Logger、Recovery
       ├── internal/service      —— 16 个业务 Service（接口+实现）
       ├── internal/repository   —— 16 个数据 Repository（接口+实现）+ 3 个 Redis 缓存装饰器
       ├── internal/model
       │   ├── entity            —— 15 个 GORM 实体
       │   └── dto/request       —— 13 个请求 DTO
       │   └── dto/response      —— 11 个响应 DTO
       ├── internal/agent        —— eino ReAct Agent 引擎（执行/回调/提示词/类型）
       ├── internal/rag          —— RAG 检索管线（装饰器链）
       ├── internal/tool         —— 工具系统（MCP连接解析、连接池、运行适配与内置能力）
       ├── internal/llm          —— LLM 客户端工厂（OpenAI 兼容，连接池复用）
       ├── internal/integration  —— 钉钉集成（OAuth / Wiki / 文档导出）
       ├── pkg/jwt               —— JWT 令牌签发与解析
       ├── pkg/email             —— SMTP 邮件发送
       ├── pkg/captcha           —— 图片验证码
       ├── pkg/cache             —— Redis 缓存通用封装
       ├── pkg/errors            —— 50+ 业务错误码 + BizError
       ├── pkg/response          —— 统一 API 响应 {"code":0,"message":"成功","data":{}}
       └── pkg/upload            —— 文件上传处理
```

## 启动流程

1. `cmd/server/main.go` 创建 `app.NewApp()`
2. `App.Initialize()` 依次执行：
   - `initConfig()` → 加载 `configs/config.yaml`，应用 `.env`，应用环境变量，校验必填项
   - `initLogger()` → 初始化 zap（JSON 格式，控制台+文件双输出，lumberjack 轮转）
   - `initDatabase()` → 连接 PostgreSQL（启用 pgvector）和 Redis
   - `initDependencies()` → 创建 Repository → 创建 Tool 基础设施 → 创建 Agent 组件（Embedding → HybridRetriever → 可选 RerankRetriever/ExpandRetriever → KnowledgeSearchTool → ReAct Engine）→ 创建 Service
   - `initRouter()` → 创建 Gin Engine，注册中间件（CORS → Recovery → Logger），注册 13 个路由模块
   - `initServer()` → 创建 `http.Server`
3. `App.Run()` → 启动 HTTP Server + 监听 SIGINT/SIGTERM 优雅关闭

## 模块边界

| 模块 | 职责 | 禁止 |
|------|------|------|
| `cmd/server` | 创建并运行 App | 不持有业务逻辑 |
| `pkg/config` | 配置加载、环境变量覆盖、全局访问 | 不依赖其他业务模块 |
| `internal/app` | 全局装配和生命周期管理 | 不写业务逻辑 |
| `internal/api` | Gin 路由注册、请求绑定、响应编码 | 不直接访问 DB、不编排 Agent |
| `internal/middleware` | JWT 鉴权、CORS、请求日志、panic 恢复 | — |
| `internal/service` | 业务用例编排、DTO/Entity 转换、校验 | 不写 GORM 查询、不拼 SQL |
| `internal/repository` | GORM 数据访问、缓存装饰 | 不写 HTTP、DTO 转换、权限判断 |
| `internal/model/entity` | 数据库表映射 | 不绑定 HTTP 语义 |
| `internal/model/dto` | HTTP API 边界定义 | 不直接复用 Entity |
| `internal/agent` | eino ReAct 编排：思考→行动→观察循环 | Controller 不直接调用 |
| `internal/rag` | 检索管线：Hybrid→Rerank→Expand 装饰器链 | — |
| `internal/tool` | 工具注册、校验、执行 | — |
| `internal/llm` | OpenAI 兼容客户端工厂（连接池、缓存） | — |
| `internal/integration` | 钉钉 OAuth / Wiki / 文档 API 封装 | — |

## RAG 检索管线（装饰器模式）

```text
HybridRetriever (核心)
   ├── 向量检索：pgvector cosine 距离 + IVFFlat 索引
   ├── 关键词检索：中文分词 (gse) + PostgreSQL GIN 索引 (text[])
   └── 融合算法：Min-Max 归一化 → RRF (k=60, 权重可配)
       → 跨源验证 → TopK 截断
       → 优雅降级：向量失败时降级为纯关键词检索

RerankRetriever (装饰器，可选)
   调用外部 Rerank API（Qwen3-Reranker 等）
   → 替换分数 → 重排序 → 阈值过滤 → TopN
   → API 失败时优雅降级返回原始结果

ExpandRetriever (装饰器，可选)
   查询相邻分块用于上下文补充
   → Jaccard 相似度去重 → Token 总量控制
```

## Chat 双模式架构（策略模式）

Agent 的执行入口、深度计划、工具和上下文代码位置见 [Agent 代码导航](../internal/agent/README.md)；开发约束以 [AGENTS.md](../AGENTS.md) 为准。

```
POST /api/v1/chat/sessions/:id/messages (SSE)
  ├── 快速模式 (search_mode=quick)
  │     ├── 并行：Query Rewrite (LLM) + RAG 检索
  │     ├── 如有改写结果则补充检索
  │     └── LLM 流式生成答案
  │
  └── 深度模式 (search_mode=smart-reasoning)
        └── agent.Engine.Execute() (eino ReAct)
              ├── Think: 分析问题
              ├── Act: 委派知识研究、MCP研究或文档操作子Agent
              ├── Observe: 评估结果
              └── 循环直至生成最终答案
                   → SSE 事件流: thinking / plan / tool_call / tool_result / answer / citation / done
```

## MCP与内部工具

用户在设置中只管理MCP连接，不再管理普通HTTP工具或逐工具开关。系统预置是第三方MCP连接模板，服务无需由本项目实现；后台维护固定地址、Headers模板和用户字段schema，用户填写凭据后保存个人绑定。用户也可添加公网Streamable HTTP MCP端点。

```text
用户MCP连接（mcp_connections）
  ├── 个人HTTP连接：URL、Headers、独立时限
  └── 绑定系统预置：tool_providers引用 + 用户配置字段
        └── 固定地址 + Headers中{{api_key}}等变量
                ↓ 可信用户身份、模板可用性与配置验证
MCP Provider → 按用户/连接/有效配置隔离的客户端池
  └── initialize → tools/list → 工具Schema → BaseTool适配器
        └── 调用前重新校验连接状态与有效配置 → tools/call
```

快速模式不连接MCP。深度PlanExecute的Planner只规划、Executor只委派，MCP由 `web_research` 专业子Agent使用；该角色只可调用实际挂载的能力，非搜索MCP不会自动变成联网搜索能力。知识检索、文档操作分别进入对应内部能力域，汇总角色不直接获得MCP。普通深度单Agent的新请求不挂MCP，历史checkpoint保留原恢复边界。

内部知识检索、文档管理和澄清工具继续由程序注册；HTTP接口与Agent工具共享业务规则。普通HTTP Provider已删除，旧用户工具路由不保留兼容。`tool_providers`/`tool_types`仍作为MCP预置基础设施，旧 `user_tool_configs` 只保留迁移与清理用途，仓库仅提供级联删除，不保留旧用户工具CRUD与缓存层，运行时不再从中加载工具。

个人连接在拨号时解析并固定公网IP，禁用代理、跨源请求和重定向；管理员预置可连接企业内网。认证头覆盖初始化、发现与调用；每个响应体限制8MiB，每个服务最多128个工具。详情只向本人回读凭据用于编辑，列表不含凭据。配置、迁移与已知限制见[MCP配置](mcp-configuration.md)。

## 请求链路

```text
HTTP Client
  → Gin Router
  → middleware.CORS
  → middleware.Recovery
  → middleware.Logger
  → middleware.Auth (JWT Bearer Token)
  → Controller (internal/api/v1/<module>)
  → Service (internal/service)
  → Repository (internal/repository) + Agent/RAG/Tool/LLM
  → PostgreSQL / Redis / 外部 API
  → response.Success({code:0, data}) 或 response.BizError({code, message})
```

## 配置结构

```
configs/config.yaml          —— 主配置文件（含所有节点和实际值）
  ├── app:                   应用基础信息 (name, version, env, mode)
  ├── server:                服务监听 + 优雅关闭
  ├── jwt:                   JWT 密钥 + 过期时间
  ├── log:                   日志级别 + 文件轮转
  ├── cors:                  CORS 策略
  ├── agent:                 Agent 行为 (enable_demo, max_iterations, score_threshold)
  ├── llm:                   默认 LLM (provider, model, api_key, base_url, 温度, max_tokens, timeout)
  ├── embedding:             向量化模型 (provider, model, api_key, base_url, dimension)
  ├── rag:                   检索配置 (top_k, score_threshold, vector_weight, keyword_weight, rrf_k)
  │   ├── reranker:          重排序 (enabled, endpoint, model, api_key, top_n, timeout, score_threshold)
  │   └── expander:          分块扩展 (enabled, window_size, max_chunk_tokens, dedup_threshold)
  ├── tools:                 内置时限 + MCP预置 (default_timeout, mcp_servers)
  ├── dingtalk:              钉钉集成 (app_key, app_secret, oauth_redirect_uri)
  ├── database:              PostgreSQL + Redis 连接配置
  └── email:                 SMTP 邮件配置

配置优先级: 代码默认值 < config.yaml < .env 文件 < 系统环境变量
```

## 响应规范

所有 API 返回统一结构：

```json
// 成功
{"code": 0, "message": "成功", "data": {}}

// 错误
{"code": 4003, "message": "工具调用失败"}
```

分页响应（列表接口）：

```json
{
  "code": 0,
  "message": "成功",
  "data": {
    "items": [...],
    "total": 100,
    "page": 1,
    "page_size": 20
  }
}
```

## 设计取舍

- **路由统一使用 Gin**，不再使用原生 `ServeMux`
- **日志使用 zap + lumberjack**，JSON 格式，双输出，自动轮转
- **LLM 使用 eino-openai**，支持 OpenAI / DeepSeek / 智谱 / 通义等所有兼容厂商
- **Embedding 使用 eino-ext embedding/openai**，支持 Redis 缓存（SHA-256，24h TTL）
- **聊天消息向量化异步持久执行**：消息INSERT同时登记任务状态；App管理有界Worker，数据库租约与令牌保证并发领取和重启恢复，向量结果与终态原子写入。后台直接使用可取消的Embedding客户端，查询侧保留缓存。启动不扫描或登记历史消息，旧数据由维护命令显式入队；有限重试后失败保留正文，排障和命令见[开发指南](DEVELOPMENT.md)。
- **RAG 使用 pgvector + 中文分词**，支持 IVFFlat 向量索引和 GIN 关键词索引
- **Agent 使用 eino ReAct**，支持工具调用、回调事件、SSE 流式输出
- **Repository 使用接口+实现分离**，Model/Tool/UserModelConfig 等高频读仓库使用 Redis 缓存装饰器
- **Service 不直接访问数据库**，通过 Repository 接口隔离
- **真实密钥/Token/密码不写入文档或日志**
- **LLM 客户端使用 sync.Map 缓存**（按 baseURL+apiKey+modelID+config 哈希），连接池复用
