package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/internal/llm"
)

func TestBudgetModelRejectsBeforeCallingInner(t *testing.T) {
	calls := 0
	inner := &deepTestModel{respond: func(context.Context, []*schema.Message) (*schema.Message, error) {
		calls++
		return schema.AssistantMessage("正常", nil), nil
	}}
	wrapped := &budgetModel{inner: inner, guard: newBudgetGuard(Request{ModelName: "gpt-4o", MaxContextLength: 8192})}
	for _, streaming := range []bool{false, true} {
		huge := []*schema.Message{schema.UserMessage(strings.Repeat("当前问题", 5000))}
		var err error
		if streaming {
			_, err = wrapped.Stream(context.Background(), huge)
		} else {
			_, err = wrapped.Generate(context.Background(), huge)
		}
		if !errors.Is(err, llm.ErrContextBudget) || calls != 0 {
			t.Fatalf("超预算调用了底层: %v calls=%d", err, calls)
		}
	}
	_, err := wrapped.Generate(context.Background(), []*schema.Message{schema.UserMessage("问")}, model.WithMaxTokens(9000))
	if !errors.Is(err, llm.ErrContextBudget) || calls != 0 {
		t.Fatal("输出覆盖未核对")
	}
	_, err = wrapped.Generate(context.Background(), []*schema.Message{schema.UserMessage("问")})
	if err != nil || calls != 1 {
		t.Fatal("正常调用失败", err)
	}
}

func TestBudgetGuardDefersInvalidDefaultToActualPayload(t *testing.T) {
	guard := newBudgetGuard(Request{ModelName: "gpt-4o", MaxContextLength: 2048, OutputLimit: 4096})
	state := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.UserMessage("问题")}}
	if _, _, err := guard.BeforeModelRewriteState(context.Background(), state, nil); !errors.Is(err, llm.ErrContextBudget) {
		t.Fatal("无最终保护时必须拒绝", err)
	}
	guard.finalCheckElsewhere = true
	if _, got, err := guard.BeforeModelRewriteState(context.Background(), state, nil); err != nil || got != state {
		t.Fatal("最终请求尚未应用输出覆盖就提前拒绝", err)
	}
}
func TestBudgetModelDropsOnlyCompletedOldConversation(t *testing.T) {
	current := "当前问题"
	messages := []*schema.Message{schema.SystemMessage("固定约束"), schema.UserMessage(strings.Repeat("旧问题", 5000)), schema.AssistantMessage("旧回答", nil), schema.UserMessage(current)}
	inner := &deepTestModel{respond: func(_ context.Context, input []*schema.Message) (*schema.Message, error) {
		if len(input) != 2 || input[0].Content != "固定约束" || input[1].Content != current {
			t.Fatal("未正确降级", input)
		}
		return schema.AssistantMessage("正常", nil), nil
	}}
	wrapped := &budgetModel{inner: inner, guard: newBudgetGuard(Request{ModelName: "gpt-4o", MaxContextLength: 8192})}
	if _, err := wrapped.Generate(context.Background(), messages); err != nil {
		t.Fatal(err)
	}
	if len(messages) != 4 || len(messages[1].Content) == 0 {
		t.Fatal("修改了源状态")
	}
	calls := schema.AssistantMessage("", []schema.ToolCall{{ID: "call", Function: schema.FunctionCall{Name: "write", Arguments: "{}"}}})
	if _, ok := dropOldConversation([]*schema.Message{schema.UserMessage("旧"), calls, schema.ToolMessage("成功", "call"), schema.UserMessage(current)}); ok {
		t.Fatal("删除了工具执行事实")
	}
}

func TestDropOldConversationPreservesUnansweredUser(t *testing.T) {
	first := schema.UserMessage("未回答的问题")
	latest := schema.UserMessage("当前问题")
	input := []*schema.Message{first, schema.UserMessage("旧问题"), schema.AssistantMessage("旧回答", nil), latest}
	got, ok := dropOldConversation(input)
	if !ok || len(got) != 2 || got[0] != first || got[1] != latest || len(input) != 4 {
		t.Fatal("删除了未回答问题或修改了原状态")
	}
}
