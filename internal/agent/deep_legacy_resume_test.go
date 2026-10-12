package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/pkg/config"
)

type deepTestModel struct {
	respond func(context.Context, []*schema.Message) (*schema.Message, error)
}

func (m *deepTestModel) Generate(ctx context.Context, msgs []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	return m.respond(ctx, msgs)
}
func (m *deepTestModel) Stream(ctx context.Context, msgs []*schema.Message, _ ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.respond(ctx, msgs)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}
func (m *deepTestModel) WithTools(_ []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	return &deepTestModel{respond: m.respond}, nil
}

const parallelTestPlan = `{"strategy":"parallel","tasks":[{"id":"a","agent":"knowledge_research","title":"研究 A","description":"topic-A"},{"id":"b","agent":"knowledge_research","title":"研究 B","description":"topic-B"},{"id":"final","agent":"synthesis_review","title":"汇总","depends_on":["a","b"]}]}`

func deepTestEngine() *Engine {
	return &Engine{cfg: config.AgentConfig{DeepPlanExecuteEnabled: true, DeepParallelEnabled: true, DeepMaxPlanTasks: 4, DeepMaxSubagentIterations: 3}}
}

func deepTestContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return withPrebuiltTools(ctx, prebuiltToolsBundle{Buckets: map[ToolDomain][]einoTool.BaseTool{DomainKnowledge: nil}})
}

type deepApprovalTool struct{ calls *atomic.Int32 }

func (t deepApprovalTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "write_file", Desc: "写文件"}, nil
}
func (t deepApprovalTool) InvokableRun(context.Context, string, ...einoTool.Option) (string, error) {
	t.calls.Add(1)
	return "已执行", nil
}

func TestDeepOrchestratorApprovalPreservesIndependentResults(t *testing.T) {
	for _, approved := range []bool{true, false} {
		t.Run(fmt.Sprint(approved), func(t *testing.T) {
			ctx := deepTestContext(t)
			var toolCalls, knowledgeCalls atomic.Int32
			ctx = withPrebuiltTools(ctx, prebuiltToolsBundle{Buckets: map[ToolDomain][]einoTool.BaseTool{
				DomainKnowledge: nil, DomainExternal: {deepApprovalTool{calls: &toolCalls}},
			}})
			m := &deepTestModel{respond: func(_ context.Context, msgs []*schema.Message) (*schema.Message, error) {
				if strings.Contains(msgs[0].Content, "研究知识库") {
					knowledgeCalls.Add(1)
					return schema.AssistantMessage("独立知识结果", nil), nil
				}
				if strings.Contains(msgs[0].Content, "使用已挂载的外部工具") {
					for _, msg := range msgs {
						if msg.Role == schema.Tool {
							return schema.AssistantMessage("外部研究结果", nil), nil
						}
					}
					return schema.AssistantMessage("", []schema.ToolCall{{ID: "call-write", Type: "function", Function: schema.FunctionCall{Name: "write_file", Arguments: "{}"}}}), nil
				}
				if !strings.Contains(msgs[len(msgs)-1].Content, "独立知识结果") {
					return nil, fmt.Errorf("恢复后丢失独立任务结果")
				}
				return schema.AssistantMessage("恢复后的最终答复", nil), nil
			}}
			e := deepTestEngine()
			req := Request{Query: "联合研究", UserID: "u", SessionID: "s"}
			plan := ExecutionPlan{ID: "legacy-plan", Version: 1, Strategy: "parallel", Status: TaskWaiting, Tasks: []TaskSpec{
				{ID: "knowledge", Agent: "knowledge_research", Title: "知识研究", Status: TaskSuccess},
				{ID: "web", Agent: "web_research", Title: "外部研究", Status: TaskWaiting},
				{ID: "s", Agent: "synthesis_review", Title: "汇总", Status: TaskPending, DependsOn: []string{"knowledge", "web"}},
			}}
			// 制造旧版子 Runner 中断及已完成分支，模拟升级前已持久化的 checkpoint。
			results := map[string]TaskResult{"knowledge": {TaskID: "knowledge", Agent: "knowledge_research", Status: TaskSuccess, Summary: "独立知识结果"}}
			out := make(chan Event, 100)
			waiting := e.executeDeepTask(ctx, req, m, plan, plan.Tasks[1], results, out)
			if waiting.Interrupt == nil {
				t.Fatalf("旧版子任务未中断: %+v", waiting)
			}
			results["web"] = waiting
			raw, err := json.Marshal(deepRunState{Plan: plan, Results: results, Request: req})
			if err != nil {
				t.Fatal(err)
			}
			if err = e.buildCheckpointStore("s").Set(ctx, "deep-legacy", raw); err != nil {
				t.Fatal(err)
			}
			interrupt := *waiting.Interrupt
			interrupt.CheckpointID = "deep-legacy"
			planID := plan.ID
			req.CheckpointID = interrupt.CheckpointID
			req.ResumeData = map[string]any{interrupt.InterruptID: &ApprovalResult{Approved: approved}}
			events := collectPEEvents(t, ctx, e, req, m)
			done := false
			for _, ev := range events {
				if ev.Type == EventError {
					t.Fatalf("resume error: %+v; events=%+v", ev, events)
				}
				if ev.Type == EventDone {
					done = ev.Content == "恢复后的最终答复"
				}
				if ev.Type == EventPlan && ev.PlanID != planID {
					t.Fatal("恢复后计划 ID 变化")
				}
			}
			if !done || knowledgeCalls.Load() != 0 {
				t.Fatalf("done=%t knowledge=%d", done, knowledgeCalls.Load())
			}
			wantToolCalls := int32(0)
			if approved {
				wantToolCalls = 1
			}
			if toolCalls.Load() != wantToolCalls {
				t.Fatalf("toolCalls=%d want=%d", toolCalls.Load(), wantToolCalls)
			}
		})
	}
}

func TestFlattenSourcesMergesParallelEvidenceWithoutMutatingInputs(t *testing.T) {
	source := SourceInfo{DocumentID: "doc", KnowledgeBaseID: "kb", Title: "资料", Score: 0.5, Chunks: []ChunkSource{{ID: "c1", Content: "内容"}}}
	second := source
	second.Score = 0.9
	second.Chunks = []ChunkSource{{ID: "c1"}, {ID: "c2"}}
	got := flattenSources([]AgentEvidence{{Sources: []SourceInfo{source}}, {Sources: []SourceInfo{second}}})
	if len(got) != 1 || len(got[0].Chunks) != 2 || got[0].Score != 0.9 {
		t.Fatalf("来源聚合错误: %+v", got)
	}
	if len(source.Chunks) != 1 || source.Score != 0.5 {
		t.Fatal("聚合修改了原研究证据")
	}
}
