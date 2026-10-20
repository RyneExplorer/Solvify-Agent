package agent

import (
	"context"
	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"strings"
	"sync/atomic"
	"testing"
)

func TestPlanExecuteRepairsMissingSynthesisDependencies(t *testing.T) {
	var research atomic.Int32
	m := peModel(parallelTestPlan, &research)
	base := m.respond
	invalid, corrections := false, 0
	m.respond = func(ctx context.Context, msgs []*schema.Message) (*schema.Message, error) {
		if strings.Contains(msgs[0].Content, "你是 Replanner") {
			if strings.Contains(msgs[len(msgs)-1].Content, "刚才生成的计划未通过校验") {
				corrections++
			}
			if !invalid {
				invalid = true
				return peCall("plan", map[string]any{"strategy": "parallel", "replan_reason": "继续汇总", "tasks": []map[string]string{{"id": "latest", "agent": "synthesis_review", "title": "汇总"}}}), nil
			}
		}
		return base(ctx, msgs)
	}
	events := collectPEEvents(t, deepTestContext(t), deepTestEngine(), Request{Query: "比较", UserID: "u", SessionID: "repair"}, m)
	done, failed := false, false
	for _, event := range events {
		done = done || event.Type == EventDone
		failed = failed || event.Type == EventError
	}
	if corrections != 0 || !done || failed || research.Load() != 2 {
		t.Fatalf("计划依赖未由程序补齐或重复执行研究：corrections=%d done=%t failed=%t research=%d", corrections, done, failed, research.Load())
	}
}

func TestPlanExecuteDocumentToolsDoNotEnableWebResearch(t *testing.T) {
	e := deepTestEngine()
	e.RegisterInternal("delete_document", 1, true, DomainDocMgmt, func(context.Context, string, []string) einoTool.BaseTool {
		return namedTestTool{name: "delete_document"}
	})
	var research atomic.Int32
	m := peModel(parallelTestPlan, &research)
	base := m.respond
	checked := false
	m.respond = func(ctx context.Context, msgs []*schema.Message) (*schema.Message, error) {
		if strings.Contains(msgs[0].Content, "Solvify Planner") {
			checked = true
			if !strings.Contains(msgs[0].Content, "web_research 可用=false") || !strings.Contains(msgs[0].Content, "系统文档操作可用=true") {
				t.Errorf("能力提示错误: %s", msgs[0].Content)
			}
		}
		return base(ctx, msgs)
	}
	events := collectPEEvents(t, context.Background(), e, Request{Query: "比较知识库", UserID: "u", SessionID: "no-web", MCPDisabled: true}, m)
	done := false
	for _, event := range events {
		if event.Type == EventError {
			t.Fatal(event.Error)
		}
		done = done || event.Type == EventDone
	}
	if !checked || !done {
		t.Fatal("能力隔离流程未完成")
	}
}
