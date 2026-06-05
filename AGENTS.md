# AGENTS.md

# 项目名称：Solvify-Agent

## 目标：
- 用户/企业知识助理

## 架构分层

- 项目采用 Go + Gin + Eino，整体按 MVC + Service + Agent 能力层组织
- `cmd/server` 只负责启动应用，不写业务逻辑
- `internal/app` 负责应用装配，包括配置、日志、依赖初始化、Gin Engine、中间件、HTTP Server 生命周期
- `internal/api` 只负责 HTTP 路由聚合，不直接写业务处理逻辑
- `internal/api/v1/<module>` 按功能模块拆分 Controller 和 routes
- `internal/service` 负责业务用例编排，以及 DTO 与 Agent/底层能力结构之间的转换
- `internal/agent` 负责 Agent 编排逻辑，包括 RAG、Tool、LLM 调用流程
- `internal/rag`、`internal/tool`、`internal/llm` 负责具体能力实现
- `internal/model/entity` 存放数据库实体结构
- `internal/model/dto/request` 存放请求 DTO
- `internal/model/dto/response` 存放响应 DTO
- `pkg` 存放可复用基础设施，例如配置、日志、错误码、统一响应

## 路由规范

- `internal/api/router.go` 只做模块路由聚合
- `Router.Setup(engine *gin.Engine)` 是总路由入口
- App 层创建 `gin.Engine` 并统一挂载全局中间件
- 路由层不创建 `gin.Engine`
- 路由层不初始化日志
- 路由层不直接调用 Service
- 默认健康检查路由保留为 `GET /health`
- 业务接口按模块分组，例如问答模块使用 `/api/v1/qa/...`
- 模块 routes 文件只负责注册当前模块路由
- 不保留废弃路由兼容，除非用户明确要求

## Controller 规范

- Controller 负责接收请求、绑定 DTO、调用 Service、返回统一响应
- Controller 不直接依赖 `internal/agent` 的请求或响应结构
- Controller 不编排 RAG、Tool、LLM 等 Agent 内部流程
- Controller 不写复杂业务逻辑
- 每个模块默认拆分为：
  - `模块名_controller.go`, 比如`qa_controller`
  - `routes.go`

## DTO 规范

- HTTP 请求结构统一放在 `internal/model/dto/request`
- HTTP 响应结构统一放在 `internal/model/dto/response`
- Agent 层结构不直接作为 HTTP API DTO 暴露
- Service 层负责 DTO 与 Agent 入参/出参之间的转换
- DTO 命名要体现业务含义，例如 `qaReq`、`qaRes`
- 不为了单字段或临时结构过度拆分 DTO，确实属于接口边界时才创建

## Entity 规范

- 数据库表映射结构统一放在 `internal/model/entity`
- Entity 只表达持久化数据结构，不写业务流程
- Entity 不直接绑定 HTTP 请求语义
- DTO 和 Entity 不混用

## Service 规范

- Service 是业务用例入口，Controller 只能通过 Service 调用业务能力
- Service 可以调用 Agent、Repository、外部能力适配器
- Service 负责必要的数据转换、业务校验和错误透传
- Service 命名要体现业务含义，以下划线为分隔例如 `chat_service`
- 不创建只使用一次的工具函数或抽象层
- 不为不可能发生的场景添加错误处理或兜底逻辑

## 日志规范

- 日志初始化由 `internal/app` 负责
- 全局日志能力由 `pkg/logger` 提供
- Gin 请求日志通过中间件统一处理
- 不在每次日志打印时额外加锁
- 日志输出统一使用中文，保持简洁明确
- 不打印密钥、Token、密码、完整连接串等敏感信息
- 错误日志应包含必要上下文，避免英文长句式报错

## 错误与响应规范

- API 返回统一使用 `pkg/response`
- 业务错误优先使用 `pkg/errors` 中的错误码和业务错误结构
- Controller 中参数错误使用 `response.BadRequest`
- Service 或 Agent 返回的业务错误使用 `response.BizError`
- 错误提示默认使用简洁中文

## 测试规范

- 修改业务逻辑、路由、DTO 或公共响应结构后必须运行 `go test ./...`
- 新增或调整路由时，应补充对应路由测试
- 路由测试至少覆盖：
  - 正常路径
  - 参数格式错误
  - 关键业务错误
  - 被废弃或不应存在的路径
- 测试应关注行为，不为实现细节写脆弱断言
- 不为了追求覆盖率添加无意义测试

## 新增接口规范

- 新增接口时按模块创建或复用目录：`internal/api/v1/<module>`
- 必要时同步新增 request/response DTO
- Controller 只绑定 DTO 并调用 Service
- Service 负责转换 DTO 并调用 Agent 或其他能力层
- 路由路径必须按模块分组，不直接堆在 `/api/v1`

## 工程取舍

- 避免过度设计，只做任务明确要求或明显必要的改动
- 确认无用代码后直接删除，不留注释说明
- 优先保持功能模块化，但不为了形式拆出空壳抽象
- 结构调整要服务于边界清晰，而不是机械套模板

---

# Agent 工作流

## 复杂度评估

- 每次开始任务前，先判断任务复杂度
- 简单任务可直接采用轻量计划，说明目标、修改范围和验证方式
- 非简单任务必须先进入完整 Plan 流程，再开始实现
- 非简单任务包括：
  - 涉及 3 个以上步骤
  - 跨模块修改
  - 涉及架构决策
  - 涉及公共接口变化
  - 涉及数据库、部署、认证、安全、并发或核心抽象
  - 验证成本较高或影响范围不明确
- 如果执行过程中发现事情偏离预期，立即停止并重新规划，不要硬着头皮继续做

## Plan First

- 复杂任务先写清楚目标、范围、方案、影响面和验证方式
- 需要完整计划时，将任务拆到 `tasks/todo.md`
- `tasks/todo.md` 应包含可勾选任务、当前进度、验证项和复盘区域
- 开始实现前先确认计划是否足够明确
- Plan 不只用于开发，也用于验证和排查阶段

## 子代理策略

- 只有在能显著提升并行效率、降低主上下文负担或改善分析质量时才使用 Subagent
- 研究、探索、并行分析、复杂方案对比可以交给 Subagent
- 简单任务、阻塞主路径任务、边界不清任务不要机械使用 Subagent
- 每个 Subagent 只负责一个明确任务
- Subagent 输出必须回到主上下文进行判断，不能盲目采纳

## 自我改进循环

- 只要用户做出纠正，就把经验模式记录到 `tasks/lessons.md`
- 每条 lesson 要说明：
  - 出错场景
  - 错误原因
  - 以后应遵守的规则
- 每次会话开始时，优先查看 `tasks/lessons.md` 是否与当前任务相关
- lessons 用来减少重复错误，不写空泛口号

## 完成前验证

- 没有证据证明功能有效之前，不要声称任务完成
- 完成前必须运行与改动相关的测试或检查
- Go 代码修改后默认运行 `go test ./...`
- 路由、接口、配置、部署相关修改，应补充对应行为验证
- 完成前检查 diff，确认没有引入无关改动
- 完成前自查：
  - 是否符合单一职责
  - 是否引入不必要依赖
  - 是否扩大模块耦合
  - 是否增加不必要抽象
  - 是否明显提高实现复杂度
  - 是否使验证更困难

## Bug 修复策略

- 收到明确 Bug 报告时，先复现或定位根因，再修复
- 主动查看错误信息、日志、失败测试和相关代码
- 不要求用户进行不必要的上下文切换
- CI 或测试失败时，应主动分析并修复
- 不做临时修补，优先解决根本原因

## 执行过程要求

- 执行过程中持续说明高层级变更，不输出无关细节
- 不回滚用户已有改动
- 不做未获批准的破坏性命令
- 遇到高风险歧义先停下确认
- 只修改完成任务所必需的范围



