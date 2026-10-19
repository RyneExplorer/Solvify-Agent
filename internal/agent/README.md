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
- `model_completion.go`：校验模型结束原因与消息形态；`stop` 对应文本，`tool_calls` 对应实际工具请求。截断、过滤、缺失或未知结束类型不能标为成功，异常工具请求在执行前拒绝。
- `deep_answer_stream.go`：Replanner 的真实上游流消费与 `respond.response` 增量解码；任务完成条件通过后发送 `answer` 片段，完整流仍需最终校验。断流、格式错误或取消保留已发送的部分正文，不重试或发送成功终态。内部 Runner 保持非流式，不额外调用模型或将完整回答拆分模拟流式。
- `deep_task.go`：专业子 Agent 执行与恢复、结果记录、前置任务输入。

### 消息、上下文和恢复

- `types.go`：Agent 请求、模式、事件及身份常量；HTTP DTO 由 Service 转换。
- `prompt.go`：快速、普通深度及专业任务提示词，系统提示词组合和输入消息构建。
- `context_summary.go`：运行上下文快照、摘要中间件及可摘要的完整消息组选择。
- `context_artifact.go`：工具结果产物保存、按范围读取及前置任务证据展开。
- `context_tool_budget.go`：模型窗口与实际消息余量驱动的工具结果投影、共享额度和每轮读取额度。
- `context_artifact_select.go`：关键词之外的章节、JSON Pointer和知识分块定位，以及完整JSON成员投影。
- `budget_guard.go`：模型输入额度检查、降级裁剪及模型包装器。
- `model_recovery.go`：上下文拒绝后的单次调用级恢复，调整输出预留并消耗专业任务原有调用额度。
- `checkpoint_store.go`：内存/数据库 checkpoint 存储和通用 checkpoint ID。
- `context_checkpoint.go`：checkpoint 上下文封装、请求恢复与兼容识别。

工具结果超过 `artifact_threshold` 时将完整原文保存到 PostgreSQL `context_artifacts.content`，返回带 hash/version 的引用。模型调用前按窗口、输出预留、输入上限以及消息和工具定义的实际 token 总量统一分配资料空间：能容纳的结果直接展开全文；超出软水位时在所有已保存结果之间分配额度。JSON 数组和知识库 sources 按完整记录保留排名前缀，省略时标记 `complete=false`，不把切坏的 JSON 当成有效结果。没有可靠原文引用的结果不再统一裁成1500 tokens；仍超硬预算则明确拒绝。

`read_context_artifact` 支持 `keyword`、Markdown `section`、JSON Pointer `json_path`、知识分块 `chunk_id`，一次最多选择一个。`offset`、`limit` 按所选视图的 Unicode 字符计数；关键词返回原文偏移，章节/JSON/分块续读需保留选择器。工具返回 `selection`、`total_runes`、`next_offset`，`complete` 只表示所选视图读到末尾；必须使用实际返回的 `next_offset` 续读，不能按请求页长推算。结构投影的长度不代表原文偏移，需按定位器补读。

正常运行的读取 token 额度由模型窗口、当前消息、工具定义及 `tool_context_ratio`（默认0.5）计算；每轮刷新，多个并行读取后的消息在下轮统一分配，避免先完成的工具抢占额度。读取仍受原文大小和100万字符的硬边界限制。`read_tokens`、`read_runes` 保留为未解析窗口的兼容策略。研究任务记录实际读取的选择器和范围，汇总按依赖的原权限读取这些范围并共享预算。任务结果和摘要快照中的引用只保存定位信息；模型消息仍按原有Runner机制随checkpoint保存。

存储配额和网络响应上限独立于模型窗口。默认单产物2 MiB、单运行20 MiB、单用户100 MiB，TTL 48小时；HTTP/MCP还受自身响应体限制。保存失败或超配额会明确报错，不能返回一个不可读取的引用或默默丢掉原文。读取、展开与汇总均校验用户/会话/运行/Agent/任务范围及原文 hash/version。

非法JSON、字段类型、缺少ID、负数与越界偏移作为工具结果返回 `success=false`、`error_type=invalid_arguments`、`field`、中文 `message` 和 `retryable=true`，模型可在原执行预算内修正后重新调用。深度任务单独保存尚未纠正的参数错误，正确读取后清除，模型忽略错误则任务失败；该标记仅识别程序内置读取工具。归属、存储、取消、版本/哈希与无法返回有效片段的预算错误仍通过Go error终止，不转为可纠正参数错误。

### 输出和错误

- `runner_adapter.go`：消费 Eino Runner 事件，处理流式消息、中断、来源和最终输出。
- `tool_event_format.go`：工具调用和返回结果的展示文案、参数提取。
- `source.go`：证据来源结构及多任务来源合并。
- `citation.go`：从成功工具返回的已知结构登记来源，向模型提供 `citation_sources`；模型使用 `<cite id="src_…" />` 选择来源，展示标题与目标由程序决定。
- `citation_stream.go`：逐段校验引用，将登记标识转换为现有 `kb/web` 展示标签；流式正文与 `done` 全文使用相同规则。
- `error_kinds.go`：框架错误分类、工具协议错误判定和终态错误事件。

引用登记表按运行隔离，并随上下文 checkpoint 保存及校验哈希；恢复沿用原运行来源，新运行不能仅凭历史正文中的标识引用。知识来源取自 `knowledge_search.sources` 和 `grep_chunks.data` 的文档/分块记录；网页来源取自 MCP 结果的明确 `title/url` 记录，支持 `results`、`contents`、`structuredContent`、`content` 和 MCP 文本中的 JSON，协议展开最多8层。自然语言中的网址和任意业务 `data` 字段不会被自动登记。每次运行最多512个来源，知识引用只在分块标识唯一时接受兼容写法。

新生成回答中，未登记的引用显示“来源未核验”；行内 Markdown 链接若能匹配登记的网页，使用真实标题和URL，否则只保留标签并标明“链接未核验”。这不是通用 Markdown/HTML 安全过滤器；行内代码与围栏代码保留为代码，不渲染为引用。已保存的历史正文继续使用原展示协议，不回写或追认旧来源。登记表示工具实际返回过该来源，不表示网页全文已读，也不证明论断受来源正文支持；非结构化 MCP 结果需按工具协议适配后才能生成可核验的网页引用。

## 测试与后续拆分

实现对应的测试使用相同功能前缀，例如 `deep_plan_test.go`、`context_summary_test.go`。跨文件行为测试按场景命名，例如审批拒绝、重试和上下文恢复；测试仍放在同包，便于与实现一起查找。

新增代码先归入对应职责文件。出现独立的变化原因时再拆文件，不按行数或每个辅助函数拆分。只有依赖方向清晰、确有独立使用需求的能力才进一步拆成子包。

验证命令见 [开发文档](../../docs/DEVELOPMENT.md)。保留 checkpoint 前缀、序列化注册名与官方 plan/respond 工具。

PlanExecute 的研究角色提供事实、来源和限制，`synthesis_review` 提供证据审查、回答要点和大纲，通常不超过600中文字；最终正文由 Replanner 在 `respond.response` 中生成，避免先写完整文章再重写。普通回答默认结论前置、3—5个关键要点、通常300—600中文字；用户明确要求详细内容、代码或指定篇幅时按需求及实际输出预算展开。该篇幅策略通过提示词引导，不保证固定字数或实际 token 节省比例。

快速、普通深度、Planner、Executor、专业任务和最终流均校验结束原因。`length`、`content_filter` 及异常结束分别映射为 `output_limit`、`output_filtered`、`model_output_invalid`，不进入计划纠正或自动重试；异常工具请求在实际执行前拒绝。专业任务保留失败结果中的部分材料，回答流保留已发送正文，不发送成功终态。运行摘要也拒绝不完整结果并保留原上下文。

每次调用先减少可选历史和工具材料，再在总窗口与独立输入上限内分配输出预留。必要输入较大时可降低本次输出额度，最低512 tokens（原额度更小时沿用原额度），仍容不下则明确失败。实际序列化请求继续核对消息、工具和供应商输出字段。端点明确拒绝上下文且尚未产生输出时，最多再降低一次输出额度并重试；专业任务额外调用消耗同一份持久预算，不重跑已完成工具。流已返回后不做此类恢复，不修改持久模型配置或虚增已知窗口。
