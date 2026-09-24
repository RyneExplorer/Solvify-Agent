package agent

import (
	"strings"
	"time"

	"solvify-agent/internal/observability"
	"solvify-agent/pkg/strutil"
)

// stepSummaryMaxRunes 是摘要类字段的长度上限。
// 与 eino 观测侧一致走 pkg/strutil 按 rune 截断（不按 byte，避免中文被切半）。
const stepSummaryMaxRunes = 500

// agentStepTracker 把一次 agent run 的事件流切成「轮次」，每轮落一行 agent_task_steps。
//
// 轮次判据（对齐 adk/react.go 的循环结构）：
//   - 一次 Role=Assistant 的 MessageOutput = 一轮的**开始**（本轮的模型决策）；
//   - 紧随其后的 Role=Tool 输出 = 该轮的工具执行；
//   - 再出现 Role=Assistant = 上一轮收尾、下一轮开始。
//
// ⚠️ 为什么只落表、不建 span：
// iteration span 只有成为 ChatModel span 的 parent 才有意义，而 ChatModel 的 parent
// 由 eino 内部 ctx 决定（ADK 把 ReAct 循环编译成 Chain+Graph，模型节点挂在 chain span
// 之下），从 runWithRunner 这一层插不进去。硬建只能得到「与 ChatModel 平级的轮次
// span」—— 轮次和它自己的模型/工具节点不在同一棵子树，层级图反而更乱。
// 轮次在 span 侧的可见性由 eino handler 给 ChatModel span 打的 agent.iteration 属性承担。
//
// 所有方法都对 nil 接收者安全，调用点因此不必到处判空。
type agentStepTracker struct {
	obs    observability.Recorder
	taskID string

	idx       int
	startedAt time.Time
	thinking  strings.Builder

	toolName   string
	toolInput  string
	toolResult string
	toolStatus string
	toolErr    string

	open bool
}

// newAgentStepTracker 构造追踪器。obs 为 nil 或 taskID 为空时返回 nil ——
// 这两种情况都没有可写的落点（没开可观测性 / 没有归属，硬写只会得到无主数据）。
//
// taskID 取自研轨 traceID：chat_service.go 建 agent_tasks 行时用的就是 `ID: traceID`，
// 所以 agent_task_steps.task_id 与 chat_traces.id 同源。
func newAgentStepTracker(obs observability.Recorder, taskID string) *agentStepTracker {
	if obs == nil || taskID == "" {
		return nil
	}
	return &agentStepTracker{obs: obs, taskID: taskID}
}

// beginRound 开启新一轮，并先把上一轮收尾。
// 首次调用时上一轮还不存在，finish 会直接返回。
func (t *agentStepTracker) beginRound() {
	if t == nil {
		return
	}
	t.finish()
	t.startedAt = time.Now()
	t.thinking.Reset()
	t.toolName, t.toolInput, t.toolResult, t.toolStatus, t.toolErr = "", "", "", "", ""
	t.open = true
}

// finish 落库当前轮并关闭它。可重复调用（已关闭时直接返回，保证 defer 与
// beginRound 内部调用不会重复写同一轮）。
func (t *agentStepTracker) finish() {
	if t == nil || !t.open {
		return
	}
	t.open = false

	now := time.Now()
	// 这里不自己做脱敏：RecordAgentStep 就是落库出口，它内部会走 sanitizeAgentStep。
	// （落库出口自己脱敏 —— 不在上游重复做，也不各维护一套规则。）
	t.obs.RecordAgentStep(&observability.AgentStep{
		TaskID:            t.taskID,
		StepIndex:         t.idx,
		StartedAt:         t.startedAt,
		EndedAt:           now,
		ThinkingSummary:   strutil.Truncate(strings.TrimSpace(t.thinking.String()), stepSummaryMaxRunes),
		ToolName:          t.toolName,
		ToolInputMasked:   strutil.Truncate(t.toolInput, stepSummaryMaxRunes),
		ToolResultSummary: strutil.Truncate(t.toolResult, stepSummaryMaxRunes),
		ToolStatus:        t.toolStatus,
		ToolError:         strutil.Truncate(t.toolErr, stepSummaryMaxRunes),
		LatencyMs:         now.Sub(t.startedAt).Milliseconds(),
	})
	t.idx++
}

// noteToolCall 记下本轮发起的工具调用。
// 一轮可能带多个 tool call，这里保留第一个非空名 + 第一份入参，
// 与 span 侧「一轮 = 一次模型决策 + 其后的工具执行」保持同一口径。
func (t *agentStepTracker) noteToolCall(name, args string) {
	if t == nil || !t.open {
		return
	}
	if t.toolName == "" && name != "" {
		t.toolName = name
	}
	if t.toolInput == "" && args != "" {
		t.toolInput = args
	}
	if t.toolStatus == "" {
		t.toolStatus = "running"
	}
}

// noteToolResult 记下本轮工具的执行结果。
func (t *agentStepTracker) noteToolResult(content string) {
	if t == nil || !t.open {
		return
	}
	t.toolResult = content
	t.toolStatus = "success"
}

// noteToolError 记下本轮工具的执行失败。
func (t *agentStepTracker) noteToolError(errMsg string) {
	if t == nil || !t.open {
		return
	}
	t.toolErr = errMsg
	t.toolStatus = "error"
}

// noteThinking 累积本轮的模型输出正文（流式下同一个 MessageVariant 会被调用多次）。
func (t *agentStepTracker) noteThinking(content string) {
	if t == nil || !t.open || content == "" {
		return
	}
	t.thinking.WriteString(content)
}
