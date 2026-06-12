# Deep Mode 重设计

## 概述

改造深度模式（smart-reasoning），从当前简单的 ReAct 循环升级为 Planner + ReAct + Memory + Early Stop 架构，同时将 SSE 事件从暴露真实 CoT 改为 Level 2 推理摘要展示。

## 当前问题

1. **无计划** — Agent 自行决定搜索策略，容易乱搜、重复搜
2. **无记忆** — 每轮 Think 不知道之前搜过什么，导致重复搜索
3. **暴露 CoT** — `EventThought` 直接展示 LLM 的 Chain of Thought，存在安全和体验问题
4. **无 Early Stop** — 只靠 `maxRounds=10` 兜底，浪费 Token
5. **上下文膨胀** — 完整工具结果写入历史，多轮后上下文过大

## 设计决策

| 决策 | 选择 | 原因 |
|------|------|------|
| 检索方式 | 保持现有向量检索 | Hybrid Search 后续单独升级 |
| Planner | 独立 LLM 调用 | 减少 Agent 乱搜，延迟可接受（~1-2s） |
| Memory | 仅内存，请求级生命周期 | 实现简单，无持久化开销 |
| Early Stop | final_answer 带 confidence 字段 | 无需额外 LLM 调用 |
| Observe 摘要 | 截断式（前 500 字符） | 不增加 LLM 调用开销 |
| SSE 展示 | Level 2：推理摘要 + 工具调用 | 用户体验好，不暴露 CoT |

## 架构

```
用户问题
    ↓
加载上下文（LLM Client + 历史对话）
    ↓
Planner（独立 LLM 调用）
    ↓ 生成 {goal, steps}
ReAct Agent（注入计划 + Memory）
    │
    ├─ Think（LLM 思考，参考计划和 Memory）
    ├─ Act（执行工具）
    │    ├─ knowledge_search
    │    └─ web_search (TODO)
    ├─ Observe（截断摘要写入历史，更新 Memory）
    ├─ Early Stop（confidence > 0.85 → 提前结束）
    └─ 循环（最多 5 轮）
    ↓
Final Answer
    ↓
SSE 输出（status/tool_call/tool_result/answer）
    ↓
异步保存（含 reasoning_steps）
```

## 模块设计

### 1. 数据结构

新增类型（`internal/agent/types.go`）：

```go
// Plan Planner 生成的执行计划
type Plan struct {
    Goal  string   `json:"goal"`
    Steps []string `json:"steps"`
}

// Memory 搜索记忆（请求级生命周期）
type Memory struct {
    Searches []string `json:"searches"` // 已搜索的查询
    Findings []string `json:"findings"` // 已发现的关键信息
}

// FinalAnswerResponse LLM 通过 final_answer 返回的答案
type FinalAnswerResponse struct {
    Answer     string  `json:"answer"`
    Confidence float64 `json:"confidence"`
}
```

修改事件常量：

```go
const (
    EventStatus     = "status"       // 推理摘要（替代 EventThought）
    EventToolCall   = "tool_call"    // 工具调用
    EventToolResult = "tool_result"  // 工具结果
    EventAnswer     = "answer"       // 最终答案
    EventSources    = "sources"      // 来源信息
    EventDone       = "done"
    EventError      = "error"
)
```

移除 `EventThought`，新增 `EventStatus`。`EventStatus` 展示推理摘要（如"正在查询A产品资料"），而非 LLM 的真实 CoT。

### 2. Planner

新增 `internal/agent/planner.go`。

**Planner Prompt：**

```
你是一个任务规划助手。根据用户问题，制定一个简单的执行计划。

规则：
1. 分析用户问题的核心目标
2. 将问题拆解为 2-5 个可执行步骤
3. 每个步骤应该是具体的搜索或分析动作
4. 只输出 JSON，不要输出任何解释

输出格式：
{"goal":"...","steps":["步骤1","步骤2",...]}
```

**实现：**

```go
func (e *Engine) plan(ctx context.Context, llmClient llm.Client, query string, history []historyMessage) (*Plan, error) {
    messages := []*schema.Message{
        schema.SystemMessage(plannerSystemPrompt),
        schema.UserMessage(buildPlannerUserMessage(query, history)),
    }

    resp, err := llmClient.Generate(ctx, llm.GenerateRequest{Messages: messages})
    if err != nil {
        return nil, err
    }

    var plan Plan
    if err := json.Unmarshal([]byte(resp.Message.Content), &plan); err != nil {
        return nil, err
    }
    return &plan, nil
}
```

**计划注入 ReAct：** 在 `buildReActSystemPrompt` 中追加：

```
## 执行计划
目标：比较A和B
步骤：
1. 查询A产品信息
2. 查询B产品信息
3. 比较差异
4. 生成结论

请按照计划有序执行，避免重复搜索。
```

### 3. Memory

在 `reActLoop` 内维护请求级 Memory：

```go
memory := &Memory{}

// 每次 Act 后更新
if tr.name == "knowledge_search" && tr.errMsg == "" {
    memory.Searches = append(memory.Searches, query)
    memory.Findings = append(memory.Findings, truncate(summary, 500))
}
```

**注入 Prompt：** 每轮 Think 前追加到 system prompt：

```
## 搜索记忆
已搜索：A产品功能、A产品价格
已发现：A产品支持私有化部署、K8S、SSO

请避免重复搜索，基于已有信息继续推理。
```

### 4. Early Stop

改造 `final_answer` 工具（`internal/tool/final_answer.go`）：

```go
func (t *FinalAnswerTool) Parameters() map[string]any {
    return map[string]any{
        "answer": map[string]any{
            "type":        "string",
            "description": "最终答案内容，使用 Markdown 格式",
            "required":    true,
        },
        "confidence": map[string]any{
            "type":        "number",
            "description": "对答案的置信度，0-1 之间。0.85 以上表示信息充分",
        },
    }
}
```

在 `reActLoop` 中判断：

```go
if tc.Name == "final_answer" {
    answer, confidence := parseFinalAnswerWithConfidence(tc.Arguments)
    // 记录置信度日志
    logger.Infof("Agent final_answer confidence=%.2f", confidence)
    streamAnswer(eventCh, answer)
    eventCh <- Event{Type: EventDone, Done: true}
    return
}
```

### 5. SSE 事件流

**事件类型：**

| 事件 | type | content | 示例 |
|------|------|---------|------|
| 状态 | `status` | 推理摘要 | "正在分析问题" |
| 工具调用 | `tool_call` | tool + query | `{"tool":"knowledge_search","query":"A产品功能"}` |
| 工具结果 | `tool_result` | 结果摘要 | "找到3条相关资料" |
| 答案流 | `answer` | 内容片段 | 分片流式输出 |
| 来源 | `sources` | 文档列表 | 复用现有结构 |
| 完成 | `done` | - | `{"done":true}` |

**Status 事件生成方式（自动，非 LLM 提取）：**

- Planner 完成 → `status: "正在分析问题"`
- 工具调用前 → `status: "正在查询{query}"`
- 工具结果后 → `status: "已获取{N}条相关资料"`
- final_answer 前 → `status: "正在生成最终答案"`

**前端展示效果：**

```
🧠 正在分析问题
🔍 查询A产品资料
📄 已获取3条相关资料
🔍 查询B产品资料
📄 已获取2条相关资料
📊 正在比较差异
✍️ 正在生成最终答案

[答案内容流式输出...]
```

### 6. ReasoningStep 持久化

修改 `ReasoningStep` 支持 status 类型：

```go
type ReasoningStep struct {
    Type       string          `json:"type"`                  // "status" / "tool_call" / "tool_result"
    Content    string          `json:"content,omitempty"`     // 状态描述 / 工具结果摘要
    ToolCalls  []ToolCallInfo  `json:"tool_calls,omitempty"`  // 工具调用
    ToolResult *ToolResultInfo `json:"tool_result,omitempty"` // 工具结果
}
```

## 改造文件清单

| 文件 | 改动类型 | 说明 |
|------|---------|------|
| `internal/agent/types.go` | 修改 | 新增 Plan/Memory/FinalAnswerResponse，修改事件常量 |
| `internal/agent/planner.go` | 新增 | Planner 逻辑 |
| `internal/agent/prompt.go` | 修改 | 注入计划 + Memory 到 system prompt |
| `internal/agent/react.go` | 修改 | 增加 Memory 追踪 + Early Stop + Status 事件 |
| `internal/tool/final_answer.go` | 修改 | 增加 confidence 参数 |
| `internal/model/dto/response/chat_res.go` | 修改 | ReasoningStep 支持 status 类型 |
| `internal/service/chat_service.go` | 修改 | processDeepMode 事件转发适配新事件类型 |

**不改动的文件：**

- `knowledge_search.go` — 保持不变
- `web_search.go` — 保持不变（仍为 TODO）
- `engine.go` — 保持不变
- `registry.go` — 保持不变

## 最大迭代次数

从 10 轮降低到 5 轮。有了 Planner 和 Memory，5 轮通常足够。

## 验收标准

1. 深度模式不再输出 `EventThought`，改为 `EventStatus`
2. Status 事件展示推理摘要（"正在查询X"），不暴露 CoT
3. Memory 正确追踪已搜索和已发现的内容
4. 重复搜索行为明显减少
5. final_answer 包含 confidence 字段
6. Planner 生成的计划注入 ReAct system prompt
7. 现有快速检索模式不受影响
