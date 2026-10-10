package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"solvify-agent/internal/tool"
)

type peRestoreToolFactory struct {
	tool.ToolFactory
	write          einoTool.BaseTool
	wrongSelection atomic.Bool
}

func (f *peRestoreToolFactory) CreateAgentTools(_ context.Context, userID string, configIDs ...string) []einoTool.BaseTool {
	if userID != "u" || len(configIDs) != 1 || configIDs[0] != "original-config" {
		f.wrongSelection.Store(true)
		return nil
	}
	return []einoTool.BaseTool{f.write}
}

func peCall(name string, args any) *schema.Message {
	data, _ := json.Marshal(args)
	return schema.AssistantMessage("", []schema.ToolCall{{ID: "call-" + name, Type: "function", Function: schema.FunctionCall{Name: name, Arguments: string(data)}}})
}

func collectPEEvents(t *testing.T, ctx context.Context, e *Engine, req Request, m *deepTestModel) []Event {
	t.Helper()
	events, err := e.Execute(ctx, req, m)
	if err != nil {
		t.Fatal(err)
	}
	var out []Event
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-time.After(10 * time.Second):
			t.Fatal("Plan-and-Execute 未结束")
			return out
		}
	}
}

func TestPlanExecuteApprovalRestoreNewEngine(t *testing.T) {
	for _, approved := range []bool{true, false} {
		t.Run(fmt.Sprint(approved), func(t *testing.T) {
			var calls, research atomic.Int32
			ctx := withPrebuiltTools(deepTestContext(t), prebuiltToolsBundle{Buckets: map[ToolDomain][]einoTool.BaseTool{DomainExternal: {deepApprovalTool{calls: &calls}}}})
			plan := `{"strategy":"parallel","tasks":[{"id":"a","agent":"knowledge_research","title":"内部研究"},{"id":"w","agent":"web_research","title":"外部研究"},{"id":"s","agent":"synthesis_review","title":"汇总","depends_on":["a","w"]}]}`
			m := peModel(plan, &research)
			base := m.respond
			m.respond = func(ctx context.Context, msgs []*schema.Message) (*schema.Message, error) {
				if strings.Contains(msgs[0].Content, "使用已挂载的外部工具") {
					for _, msg := range msgs {
						if msg.Role == schema.Tool {
							return schema.AssistantMessage("外部结果", nil), nil
						}
					}
					return peCall("write_file", map[string]any{}), nil
				}
				return base(ctx, msgs)
			}
			e := deepTestEngine()
			req := Request{Query: "联合研究", UserID: "u", SessionID: "s", UserToolConfigIDs: []string{"original-config"}}
			events := collectPEEvents(t, ctx, e, req, m)
			var interrupt Event
			for _, ev := range events {
				if ev.Type == EventError {
					t.Fatalf("首次执行: %+v", ev)
				}
				if ev.Type == EventInterrupt {
					interrupt = ev
				}
			}
			if interrupt.CheckpointID == "" || calls.Load() != 0 || research.Load() != 1 {
				t.Fatalf("缺少正确中断: %+v calls=%d research=%d", events, calls.Load(), research.Load())
			}
			req.CheckpointID = interrupt.CheckpointID
			req.ResumeData = map[string]any{interrupt.InterruptID: &ApprovalResult{Approved: approved}}
			other := deepTestEngine()
			factory := &peRestoreToolFactory{write: deepApprovalTool{calls: &calls}}
			other.toolFactory = factory
			raw, found, err := e.buildCheckpointStore("s").Get(ctx, interrupt.CheckpointID)
			if err != nil || !found {
				t.Fatal("根 checkpoint 未保存")
			}
			if err = other.buildCheckpointStore("s").Set(ctx, interrupt.CheckpointID, raw); err != nil {
				t.Fatal(err)
			}
			// 刷新后输入框未选择 MCP；恢复必须采用 checkpoint 中的原配置。
			req.MCPDisabled = true
			req.UserToolConfigIDs = nil
			events = collectPEEvents(t, deepTestContext(t), other, req, m)
			done := false
			for _, ev := range events {
				if ev.Type == EventError {
					t.Fatalf("恢复: %+v", ev)
				}
				if ev.Type == EventDone {
					done = true
				}
			}
			want := int32(0)
			if approved {
				want = 1
			}
			if !done || calls.Load() != want || research.Load() != 1 || factory.wrongSelection.Load() {
				t.Fatalf("done=%t calls=%d research=%d events=%+v", done, calls.Load(), research.Load(), events)
			}
		})
	}
}

func peModel(plan string, research *atomic.Int32) *deepTestModel {
	return &deepTestModel{respond: func(ctx context.Context, msgs []*schema.Message) (*schema.Message, error) {
		system := msgs[0].Content
		if strings.Contains(system, "你是 Replanner") {
			content := msgs[len(msgs)-1].Content
			index := strings.Index(content, "执行状态：")
			var state struct {
				Plan    ExecutionPlan
				Results map[string]TaskResult
			}
			if index < 0 || json.Unmarshal([]byte(content[index+len("执行状态："):]), &state) != nil {
				return nil, fmt.Errorf("缺少执行状态")
			}
			if len(state.Plan.ReadyTasks()) > 0 {
				state.Plan.Reason = "继续剩余任务"
				return peCall("plan", state.Plan), nil
			}
			for i := range state.Plan.Tasks {
				task := &state.Plan.Tasks[i]
				if task.Status == TaskPending {
					var deps []string
					for _, dep := range task.DependsOn {
						if state.Results[dep].Status == TaskSuccess {
							deps = append(deps, dep)
						}
					}
					task.DependsOn = deps
					state.Plan.Reason = "根据失败结果调整汇总依赖"
					return peCall("plan", state.Plan), nil
				}
			}
			return peCall("respond", map[string]string{"response": "最终结论"}), nil
		}
		if strings.Contains(system, "Solvify Planner") {
			var raw any
			_ = json.Unmarshal([]byte(plan), &raw)
			return peCall("plan", raw), nil
		}
		if strings.Contains(system, "Solvify Executor") {
			for _, msg := range msgs {
				if msg.Role == schema.Tool {
					return schema.AssistantMessage("当前批次执行完成", nil), nil
				}
			}
			var batch []TaskSpec
			for _, msg := range msgs {
				if strings.HasPrefix(msg.Content, "当前批次：") {
					_ = json.Unmarshal([]byte(strings.TrimPrefix(msg.Content, "当前批次：")), &batch)
				}
			}
			var calls []schema.ToolCall
			for _, task := range batch {
				data, _ := json.Marshal(map[string]string{"task_id": task.ID})
				calls = append(calls, schema.ToolCall{ID: "task-" + task.ID, Type: "function", Function: schema.FunctionCall{Name: task.Agent, Arguments: string(data)}})
			}
			return schema.AssistantMessage("", calls), nil
		}
		if strings.Contains(system, "研究知识库") {
			research.Add(1)
			return schema.AssistantMessage("研究结论", nil), nil
		}
		if strings.Contains(system, "仅使用结构化研究结果") {
			return schema.AssistantMessage("汇总结论", nil), nil
		}
		return nil, fmt.Errorf("未知 Agent: %s", system)
	}}
}

func TestPlanExecuteNativeLifecycle(t *testing.T) {
	for _, plan := range []string{
		`{"strategy":"direct","tasks":[{"id":"s","agent":"synthesis_review","title":"回答"}]}`,
		`{"strategy":"parallel","tasks":[{"id":"a","agent":"knowledge_research","title":"研究 A"},{"id":"b","agent":"knowledge_research","title":"研究 B"},{"id":"s","agent":"synthesis_review","title":"汇总","depends_on":["a","b"]}]}`,
	} {
		t.Run(plan, func(t *testing.T) {
			var calls atomic.Int32
			e := deepTestEngine()
			events := collectPEEvents(t, deepTestContext(t), e, Request{Query: "研究问题", UserID: "u", SessionID: "s"}, peModel(plan, &calls))
			answers, done := 0, 0
			version := 0
			for _, ev := range events {
				if ev.Type == EventError {
					t.Fatalf("执行错误: %+v", ev)
				}
				if ev.Type == EventAnswer {
					answers++
					if ev.Content != "最终结论" {
						t.Fatal(ev.Content)
					}
				}
				if ev.Type == EventDone {
					done++
				}
				if ev.Plan != nil {
					version = max(version, ev.Plan.Version)
				}
			}
			if answers != 1 || done != 1 {
				t.Fatalf("answers=%d done=%d", answers, done)
			}
			if strings.Contains(plan, "parallel") && (calls.Load() != 2 || version < 2) {
				t.Fatalf("research=%d version=%d", calls.Load(), version)
			}
		})
	}
}
