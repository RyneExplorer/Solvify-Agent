# Agent 代码导航

开发约束以项目根目录 [AGENTS.md](../../AGENTS.md) 为准；本文只说明本包的查找入口。

## 从执行流程开始

`Engine.Execute` 是统一入口，位于 `execute.go`，负责准备请求并选择执行路径：

- 快速模式：`quick_agent.go` 中的 `runQuickAgent`。
- 深度计划编排：`deep_plan_execute.go` 中的 `runPlanExecute`。新请求是否进入此路径取决于 `DeepPlanExecuteEnabled`；`deeppe-v1-` checkpoint 继续原流程。
- 普通深度单 Agent：`deep_agent.go` 中的 `runDeepAgent`，也供旧深度任务恢复调用。
- 旧 `deep-` checkpoint：`deep_legacy_resume.go`，只处理已有任务恢复。

## 按功能查找

### 引擎、工具和交互

- `agent_interface.go`：业务执行接口与 App 装配接口。
- `engine.go`：Engine 依赖、内置工具注册、工具域和危险工具判定。
- `engine_tools.go`：工具构建、域过滤、预构建复用、工具定义额度估算及通用 ToolsNode 配置。
- `tool_middleware.go`：危险工具审批、澄清中断及恢复数据。
- `iteration_budget.go`：快速/深度模式模型轮次及交互能力所需下界。

### 深度计划与专业任务

- `deep_plan_execute.go`：工作流运行、会话状态发布、计划/任务事件及追踪上下文。
- `deep_builder.go`：组装 Eino Planner、Executor、Replanner，并构建重规划输入。
- `deep_plan.go`：计划、任务和结果结构，依赖校验、就绪任务、计划更新、完成校验及 checkpoint 类型注册。
- `deep_stage.go`：规划/执行/重规划阶段包装与阶段状态更新。
- `deep_model.go`：控制模型与执行模型适配，执行前校验模型输出。
- `deep_task.go`：专业子 Agent 执行与恢复、结果记录、前置任务输入。

### 消息、上下文和恢复

- `types.go`：Agent 请求、模式、事件及身份常量；HTTP DTO 由 Service 转换。
- `prompt.go`：快速、普通深度及专业任务提示词，系统提示词组合和输入消息构建。
- `context_summary.go`：运行上下文快照、摘要中间件及可摘要的完整消息组选择。
- `context_artifact.go`：工具结果产物保存、按范围读取及前置任务证据展开。
- `budget_guard.go`：模型输入额度检查、降级裁剪及模型包装器。
- `checkpoint_store.go`：内存/数据库 checkpoint 存储和通用 checkpoint ID。
- `context_checkpoint.go`：checkpoint 上下文封装、请求恢复与兼容识别。

`read_context_artifact` 的 `offset`、`limit` 均按字符计数，工具定义及产物引用公开当前 `max_read_runes`。省略或零 `limit` 使用配置上限，超大正整数自动缩小到该上限；完整 JSON 输出仍受 `read_tokens` 限制，因此 `complete=false` 时必须使用返回的 `next_offset` 继续，不能按请求页长累加偏移。

非法JSON、字段类型、缺少ID、负数与越界偏移作为工具结果返回 `success=false`、`error_type=invalid_arguments`、`field`、中文 `message` 和 `retryable=true`，模型可在原执行预算内修正后重新调用。深度任务单独保存尚未纠正的参数错误，正确读取后清除，模型忽略错误则任务失败；该标记仅识别程序内置读取工具。归属、存储、取消、版本/哈希与无法返回有效片段的预算错误仍通过Go error终止，不转为可纠正参数错误。

### 输出和错误

- `runner_adapter.go`：消费 Eino Runner 事件，处理流式消息、中断、来源和最终输出。
- `tool_event_format.go`：工具调用和返回结果的展示文案、参数提取。
- `source.go`：证据来源结构及多任务来源合并。
- `error_kinds.go`：框架错误分类、工具协议错误判定和终态错误事件。

## 测试与后续拆分

实现对应的测试使用相同功能前缀，例如 `deep_plan_test.go`、`context_summary_test.go`。跨文件行为测试按场景命名，例如审批拒绝、重试和上下文恢复；测试仍放在同包，便于与实现一起查找。

新增代码先归入对应职责文件。出现独立的变化原因时再拆文件，不按行数或每个辅助函数拆分。只有依赖方向清晰、确有独立使用需求的能力才进一步拆成子包。

验证命令见 [开发文档](../../docs/DEVELOPMENT.md)。本次整理保留 checkpoint 前缀、序列化注册名、工具名和提示词内容。
