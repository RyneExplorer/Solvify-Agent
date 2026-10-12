package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/pkg/tokenutil"
)

// V1：超长工具结果在**写入 state 之前**被截断，且工具调用配对不被破坏。
//
// 配对是硬约束：工具结果要和发起它的 assistant.tool_calls 按 ToolCallID 一一对应，
// 少一条就会留下无人应答的 tool_call，模型直接报错。所以这条同时断言「条数不变」。
func TestBudgetGuardTruncatesToolResults(t *testing.T) {
	long := strings.Repeat("这是一段很长的检索结果。", 2000)
	msgs := []*schema.Message{
		schema.UserMessage("帮我查一下"),
		schema.AssistantMessage("", []schema.ToolCall{{
			ID:       "call-1",
			Type:     "function",
			Function: schema.FunctionCall{Name: "knowledge_search", Arguments: `{"query":"x"}`},
		}}),
		schema.ToolMessage(long, "call-1"),
	}

	g := &budgetGuard{
		modelID:     "test-model-budget-truncate",
		declaredCtx: 2000,
		modelName:   "gpt-4o",
		profile:     ProfileQuick,
	}
	state := &adk.ChatModelAgentState{Messages: msgs}

	_, out, err := g.BeforeModelRewriteState(context.Background(), state, &adk.ModelContext{})
	if err != nil {
		t.Fatalf("BeforeModelRewriteState 返回错误: %v", err)
	}
	if len(out.Messages) != len(msgs) {
		t.Fatalf("压缩破坏了消息条数: %d → %d", len(msgs), len(out.Messages))
	}

	toolMsg := out.Messages[2]
	if toolMsg.ToolCallID != "call-1" {
		t.Fatalf("工具结果与 tool_call 的配对字段丢失: ToolCallID=%q", toolMsg.ToolCallID)
	}
	if got := tokenutil.CountTokens(toolMsg.Content, "gpt-4o"); got > toolResultCap {
		t.Fatalf("工具结果未被截断到 %d token 以内，实际 %d", toolResultCap, got)
	}
	// 用户提问必须原样保留。
	if out.Messages[0].Content != "帮我查一下" {
		t.Fatalf("用户提问被改动: %q", out.Messages[0].Content)
	}
}

// V3'：必留内容（用户提问 / 助手回答）本身超预算时，**不得**被丢弃或截断。
// 它们的原文没有替代来源，取舍属于装配器，不属于这里。
// 到这一步只允许「响亮记录 + 放行」。
func TestBudgetGuardKeepsIrreplaceableMessages(t *testing.T) {
	huge := strings.Repeat("必留内容", 3000)
	msgs := []*schema.Message{schema.UserMessage(huge)}

	g := &budgetGuard{
		modelID:     "test-model-budget-irreplaceable",
		declaredCtx: 2000,
		modelName:   "gpt-4o",
		profile:     ProfileQuick,
	}
	state := &adk.ChatModelAgentState{Messages: msgs}

	_, out, err := g.BeforeModelRewriteState(context.Background(), state, &adk.ModelContext{})
	if err != nil {
		t.Fatalf("BeforeModelRewriteState 返回错误: %v", err)
	}
	if len(out.Messages) != 1 {
		t.Fatalf("必留消息被丢弃: 1 → %d", len(out.Messages))
	}
	if out.Messages[0].Content != huge {
		t.Fatalf("必留消息被截断/改写（长度 %d → %d）", len(huge), len(out.Messages[0].Content))
	}
}

// 拿不到窗口时必须放行（而不是按 0 预算砍光上下文）——「没接上要响亮，但不能伤数据」。
//
// 输入必须是**会被截断的那类消息**（超长工具结果）：给一条 "hi" 的话，
// 任何预算逻辑都不会动它，用例就退化成 panic 检测（恒真）。
// 这样写才钉得住「窗口未知 ⇒ 不猜、不截」。
func TestBudgetGuardSkipsWhenNoWindow(t *testing.T) {
	long := strings.Repeat("这是一段很长的检索结果。", 2000)
	msgs := []*schema.Message{
		schema.UserMessage("hi"),
		schema.ToolMessage(long, "call-x"),
	}
	g := &budgetGuard{
		modelID:     "test-model-budget-unknown-window",
		declaredCtx: 0, // 声明窗口为 0 且没有运行时修正值
		modelName:   "gpt-4o",
		profile:     ProfileQuick,
	}
	state := &adk.ChatModelAgentState{Messages: msgs}

	_, out, err := g.BeforeModelRewriteState(context.Background(), state, &adk.ModelContext{})
	if err != nil {
		t.Fatalf("BeforeModelRewriteState 返回错误: %v", err)
	}
	if len(out.Messages) != 2 || out.Messages[0].Content != "hi" {
		t.Fatalf("窗口未知时应原样放行，实际消息数 %d", len(out.Messages))
	}
	if out.Messages[1].Content != long {
		t.Fatalf("窗口未知时按猜测值截断了工具结果（长度 %d → %d）",
			len(long), len(out.Messages[1].Content))
	}
}
