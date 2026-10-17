package agent

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/pkg/tokenutil"
)

func TestBudgetModelCopiesToolMessages(t *testing.T) {
	raw := strings.Repeat(guardProbeRaw, 800) + guardProbeTail
	messages := []*schema.Message{schema.SystemMessage("系统要求"), schema.ToolMessage(raw, "probe")}
	for _, streaming := range []bool{false, true} {
		checked := false
		inner := &deepTestModel{respond: func(_ context.Context, input []*schema.Message) (*schema.Message, error) {
			checked = true
			if input[0].Content != "系统要求" || input[1].ToolCallID != "probe" || strings.Contains(input[1].Content, guardProbeTail) || tokenutil.CountTokens(input[1].Content, "gpt-4o") > toolResultCap {
				t.Fatal("预算压缩未保留消息契约")
			}
			return schema.AssistantMessage("结果", nil), nil
		}}
		wrapped, err := (&budgetModel{inner: inner, guard: newBudgetGuard(Request{Profile: ProfileDeep, MaxContextLength: 64000, ModelName: "gpt-4o"})}).WithTools([]*schema.ToolInfo{{Name: "probe"}})
		if err != nil {
			t.Fatal(err)
		}
		if streaming {
			stream, err := wrapped.Stream(context.Background(), messages)
			if err != nil {
				t.Fatal(err)
			}
			stream.Close()
		} else if _, err := wrapped.Generate(context.Background(), messages); err != nil {
			t.Fatal(err)
		}
		if !checked || messages[1].Content != raw {
			t.Fatal("模型未检查预算或修改了共享消息")
		}
	}
}

func TestPlanExecuteBudgetCompressesProfessionalToolResults(t *testing.T) {
	var calls, research atomic.Int32
	e := deepTestEngine()
	e.RegisterInternal("guard_probe", 1, false, DomainKnowledge, func(context.Context, string, []string) einoTool.BaseTool { return guardProbeTool{calls: &calls} })
	m := peModel(`{"strategy":"sequential","tasks":[{"id":"k","agent":"knowledge_research","title":"研究"},{"id":"f","agent":"synthesis_review","title":"汇总","depends_on":["k"]}]}`, &research)
	base := m.respond
	checked := false
	m.respond = func(ctx context.Context, input []*schema.Message) (*schema.Message, error) {
		if strings.Contains(input[0].Content, "研究知识库") {
			for _, message := range input {
				if message.Role == schema.Tool {
					checked = true
					if strings.Contains(message.Content, guardProbeTail) || tokenutil.CountTokens(message.Content, "gpt-4o") > toolResultCap {
						t.Error("专业 Agent 看到未压缩工具结果")
					}
					return schema.AssistantMessage("研究结果", nil), nil
				}
			}
			return peCall("guard_probe", map[string]any{}), nil
		}
		return base(ctx, input)
	}
	events := collectPEEvents(t, deepTestContext(t), e, Request{Query: "研究", UserID: "u", SessionID: "budget", MaxContextLength: 64000, ModelName: "gpt-4o"}, m)
	done := false
	for _, event := range events {
		if event.Type == EventError {
			t.Fatal(event.Error)
		}
		done = done || event.Type == EventDone
	}
	if !checked || !done || calls.Load() != 1 {
		t.Fatalf("预算未接入完整流程: checked=%t done=%t calls=%d", checked, done, calls.Load())
	}
}
