package agent

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/pkg/config"
	bizerrors "solvify-agent/pkg/errors"
)

type peResultTestTool struct {
	name   string
	result string
	err    error
	calls  *atomic.Int32
}

func (p peResultTestTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: p.name, Desc: "只读查询测试"}, nil
}
func (p peResultTestTool) InvokableRun(context.Context, string, ...einoTool.Option) (string, error) {
	p.calls.Add(1)
	return p.result, p.err
}

func TestPlanExecuteToolBusinessResults(t *testing.T) {
	for _, tc := range []struct {
		name, first, second       string
		sameTool, failed, limited bool
		err                       error
	}{
		{name: "文档未命中后参数纠正", first: `{"success":false,"message":"未找到该文档"}`, second: `{"success":true,"data":"有效文档"}`, sameTool: true, limited: true},
		{name: "部分资料缺失", first: `{"success":false,"message":"未找到该文档"}`, second: `{"success":true,"data":"其他来源"}`, limited: true},
		{name: "全部查询失败不能被模型包装成功", first: `{"success":false,"message":"未找到该文档"}`, failed: true},
		{name: "Exa限流后存在替代证据", first: `{"_meta":{"ai.exa/rateLimited":true},"content":[{"type":"text","text":"rate limit"}]}`, second: `{"success":true,"data":"抓取正文"}`, limited: true},
		{name: "MCP业务错误", first: `{"isError":true,"content":[{"type":"text","text":"读取失败"}]}`, failed: true},
		{name: "MCP大错误结果不得被产物预览覆盖", first: `{"isError":true,"content":[{"type":"text","text":"` + strings.Repeat("读取失败", 100) + `"}]}`, failed: true},
		{name: "MCP文本内的业务失败", first: `{"content":[{"type":"text","text":"{\"success\":false,\"message\":\"未找到该文档\"}"}]}`, failed: true},
		{name: "正常结果中的业务数据不误判", first: `{"success":true,"data":{"success":false,"message":"示例数据"}}`, second: `{"success":true}`, sameTool: true},
		{name: "权限拒绝不得被替代查询覆盖", second: `{"success":true,"data":"其他来源"}`, err: bizerrors.New(bizerrors.CodeForbidden, "无权读取该文档"), failed: true},
		{name: "JSON权限拒绝不得被覆盖", first: `{"success":false,"code":403,"message":"无权读取该文档"}`, failed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var firstCalls, secondCalls, modelCalls atomic.Int32
			first := peResultTestTool{name: "read_record", result: tc.first, err: tc.err, calls: &firstCalls}
			second := peResultTestTool{name: "read_other", result: tc.second, calls: &secondCalls}
			e := deepTestEngine()
			e.WithArtifactRepo(&memoryArtifactRepo{})
			e.cfg.RuntimeContext = config.DefaultRuntimeContextPolicy()
			e.cfg.RuntimeContext.ArtifactThreshold = 16
			e.RegisterInternal(first.name, 1, false, DomainKnowledge, func(context.Context, string, []string) einoTool.BaseTool { return first })
			e.RegisterInternal(second.name, 2, false, DomainKnowledge, func(context.Context, string, []string) einoTool.BaseTool { return second })
			if tc.sameTool {
				e.RegisterInternal(first.name, 1, false, DomainKnowledge, func(context.Context, string, []string) einoTool.BaseTool {
					return &peAlternatingResultTool{peResultTestTool: first, next: tc.second}
				})
			}
			plan := `{"strategy":"sequential","tasks":[{"id":"a","agent":"knowledge_research","title":"研究"},{"id":"s","agent":"synthesis_review","title":"汇总","depends_on":["a"]}]}`
			m := peModel(plan, new(atomic.Int32))
			base := m.respond
			var captured TaskResult
			m.respond = func(ctx context.Context, msgs []*schema.Message) (*schema.Message, error) {
				if strings.Contains(msgs[0].Content, "研究知识库") {
					n := modelCalls.Add(1)
					if n == 1 {
						return peCall(first.name, map[string]string{"document_id": "missing"}), nil
					}
					if n == 2 && tc.second != "" {
						name := second.name
						if tc.sameTool {
							name = first.name
						}
						return peCall(name, map[string]string{"document_id": "valid"}), nil
					}
					return schema.AssistantMessage("研究完成", nil), nil
				}
				if strings.Contains(msgs[0].Content, "你是 Replanner") {
					content := msgs[len(msgs)-1].Content
					index := strings.Index(content, "执行状态：")
					var state struct{ Results map[string]TaskResult }
					if index >= 0 && json.Unmarshal([]byte(content[index+len("执行状态："):]), &state) == nil {
						captured = state.Results["a"]
					}
				}
				return base(ctx, msgs)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			events := collectPEBehaviorEvents(t, ctx, e, Request{Query: "核对资料", UserID: "u", SessionID: "tool-result"}, m)
			var toolEvents []Event
			for _, ev := range events {
				if ev.Type == EventToolResult && ev.TaskID == "a" {
					toolEvents = append(toolEvents, ev)
				}
			}
			firstFailed := tc.name != "正常结果中的业务数据不误判"
			if len(toolEvents) == 0 || firstFailed && (toolEvents[0].Status != TaskError || toolEvents[0].Detail == "") || !firstFailed && toolEvents[0].Status != TaskSuccess {
				t.Fatalf("真实失败被展示为成功: %+v", toolEvents)
			}
			if firstFailed {
				blocked := tc.err != nil || tc.name == "JSON权限拒绝不得被覆盖"
				if toolEvents[0].Retryable == blocked {
					t.Fatalf("只读纠错与安全终止没有区分: %+v", toolEvents[0])
				}
			}
			want := TaskSuccess
			if tc.failed {
				want = TaskError
			}
			if captured.Status != want {
				t.Fatalf("任务终态错误: want=%s result=%+v", want, captured)
			}
			if tc.limited && !strings.Contains(captured.Summary, "执行限制") {
				t.Fatalf("部分结果没有保留失败说明: %+v", captured)
			}
			expected := int32(1)
			if tc.second != "" {
				expected++
			}
			if firstCalls.Load()+secondCalls.Load() != expected && tc.err == nil {
				t.Fatalf("产生额外工具重试: %d/%d", firstCalls.Load(), secondCalls.Load())
			}
			if tc.err != nil && secondCalls.Load() != 0 {
				t.Fatal("权限拒绝后仍执行了替代工具")
			}
		})
	}
}

func TestPlanExecuteToolFailureRecoveryAcrossProcesses(t *testing.T) {
	dir, phase := os.Getenv("SOLVIFY_PE_RESULT_DIR"), os.Getenv("SOLVIFY_PE_RESULT_PHASE")
	if dir == "" {
		dir = t.TempDir()
		for _, stage := range []string{"save", "restore"} {
			cmd := exec.Command(os.Args[0], "-test.run=^TestPlanExecuteToolFailureRecoveryAcrossProcesses$", "-test.timeout=30s")
			cmd.Env = append(os.Environ(), "SOLVIFY_PE_RESULT_DIR="+dir, "SOLVIFY_PE_RESULT_PHASE="+stage)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", stage, err, out)
			}
		}
		return
	}
	for _, approved := range []bool{true, false} {
		label := "approved"
		if !approved {
			label = "denied"
		}
		storeDir := filepath.Join(dir, label)
		if err := os.MkdirAll(storeDir, 0700); err != nil {
			t.Fatal(err)
		}
		var reads, writes atomic.Int32
		read := peResultTestTool{name: "lookup_record", result: `{"success":false,"message":"资料缺失"}`, calls: &reads}
		ctx := withPrebuiltTools(context.Background(), prebuiltToolsBundle{Buckets: map[ToolDomain][]einoTool.BaseTool{DomainExternal: {read, deepApprovalTool{calls: &writes}}}})
		req := Request{UserID: "u", SessionID: "tool-result-process", Query: "查询后执行操作", MaxContextLength: 64000, ModelName: "gpt-4o", OutputLimit: 4096}
		e := deepTestEngine()
		r := &peRuntime{engine: e, request: req}
		m := peModel(`{"strategy":"sequential","tasks":[{"id":"a","agent":"web_research","title":"查询并操作"},{"id":"s","agent":"synthesis_review","title":"汇总","depends_on":["a"]}]}`, new(atomic.Int32))
		base := m.respond
		m.respond = func(ctx context.Context, msgs []*schema.Message) (*schema.Message, error) {
			if strings.Contains(msgs[0].Content, "使用已挂载的外部工具") {
				last := msgs[len(msgs)-1]
				if last.Role != schema.Tool {
					return peCall("lookup_record", map[string]string{}), nil
				}
				if last.ToolName == "lookup_record" || last.ToolCallID == "call-lookup_record" {
					return peCall("write_file", map[string]string{}), nil
				}
				return schema.AssistantMessage("基于可用结果完成操作", nil), nil
			}
			return base(ctx, msgs)
		}
		r.model = m
		root, err := r.build(ctx)
		if err != nil {
			t.Fatal(err)
		}
		runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: root, CheckPointStore: probeFileStore{dir: storeDir}})
		var iter *adk.AsyncIterator[*adk.AgentEvent]
		if phase == "save" {
			raw, _ := json.Marshal(&peState{Request: req, Results: map[string]TaskResult{}, Unsafe: map[string]bool{}, Failures: map[string]string{}})
			iter = runner.Run(ctx, []*schema.Message{schema.UserMessage(req.Query)}, adk.WithCheckPointID("result"), adk.WithSessionValues(map[string]any{peSessionKey: string(raw)}))
		} else {
			target, readErr := os.ReadFile(filepath.Join(storeDir, "interrupt-id"))
			if readErr != nil {
				t.Fatal(readErr)
			}
			iter, err = runner.ResumeWithParams(ctx, "result", &adk.ResumeParams{Targets: map[string]any{string(target): &ApprovalResult{Approved: approved}}})
			if err != nil {
				t.Fatal(err)
			}
		}
		interrupted, deniedEvent := false, false
		for {
			ev, ok := iter.Next()
			if !ok {
				break
			}
			if ev.Err != nil {
				t.Fatal(ev.Err)
			}
			if ev.Action != nil && ev.Action.Interrupted != nil {
				for _, point := range ev.Action.Interrupted.InterruptContexts {
					if point.IsRootCause {
						if err := os.WriteFile(filepath.Join(storeDir, "interrupt-id"), []byte(point.ID), 0600); err != nil {
							t.Fatal(err)
						}
						interrupted = true
					}
				}
			}
			if ev.Output != nil {
				if event, ok := ev.Output.CustomizedOutput.(Event); ok && event.Type == EventToolResult && event.Status == TaskError && strings.Contains(event.Detail, "拒绝") {
					deniedEvent = true
				}
			}
		}
		if phase == "save" {
			if !interrupted || reads.Load() != 1 || writes.Load() != 0 {
				t.Fatalf("暂停前执行错误: reads=%d writes=%d", reads.Load(), writes.Load())
			}
		} else {
			result := r.state.Results["a"]
			if reads.Load() != 0 || interrupted {
				t.Fatal("恢复重复查询或未结束")
			}
			if approved {
				if writes.Load() != 1 || result.Status != TaskSuccess || !strings.Contains(result.Summary, "资料缺失") {
					t.Fatalf("恢复丢失失败限制: %+v writes=%d", result, writes.Load())
				}
			} else if writes.Load() != 0 || result.Status != TaskError || !deniedEvent {
				t.Fatalf("审批拒绝被伪装成功: %+v event=%v", result, deniedEvent)
			}
		}
	}
}

func TestPlanExecuteSideEffectFailureBlocksFurtherTools(t *testing.T) {
	var writes, reads atomic.Int32
	write := peResultTestTool{name: "write_file", result: `{"success":false,"message":"操作结果不确定"}`, calls: &writes}
	read := peResultTestTool{name: "lookup_record", result: `{"success":true}`, calls: &reads}
	ctx := withPrebuiltTools(deepTestContext(t), prebuiltToolsBundle{Buckets: map[ToolDomain][]einoTool.BaseTool{DomainExternal: {write, read}}})
	e := deepTestEngine()
	m := peModel(`{"strategy":"sequential","tasks":[{"id":"a","agent":"web_research","title":"执行操作"},{"id":"s","agent":"synthesis_review","title":"汇总","depends_on":["a"]}]}`, new(atomic.Int32))
	base := m.respond
	m.respond = func(ctx context.Context, msgs []*schema.Message) (*schema.Message, error) {
		if strings.Contains(msgs[0].Content, "使用已挂载的外部工具") {
			last := msgs[len(msgs)-1]
			if last.Role != schema.Tool {
				return peCall("write_file", map[string]string{}), nil
			}
			if last.ToolName == "write_file" || last.ToolCallID == "call-write_file" {
				return peCall("lookup_record", map[string]string{}), nil
			}
			return schema.AssistantMessage("模型声称已经恢复", nil), nil
		}
		return base(ctx, msgs)
	}
	req := Request{Query: "执行操作", UserID: "u", SessionID: "side-effect-result"}
	events := collectPEEvents(t, ctx, e, req, m)
	var checkpoint, target string
	for _, ev := range events {
		if ev.Type == EventInterrupt {
			checkpoint, target = ev.CheckpointID, ev.InterruptID
		}
	}
	if checkpoint == "" || writes.Load() != 0 {
		t.Fatal("审批前执行了操作或未暂停")
	}
	req.CheckpointID, req.ResumeData = checkpoint, map[string]any{target: &ApprovalResult{Approved: true}}
	events = collectPEEvents(t, ctx, e, req, m)
	var failedTask, failedTool bool
	for _, ev := range events {
		if ev.Type == EventTaskResult && ev.TaskID == "a" {
			failedTask = ev.TaskStatus == TaskError
		}
		if ev.Type == EventToolResult && ev.TaskID == "a" {
			failedTool = ev.Status == TaskError
		}
	}
	if writes.Load() != 1 || reads.Load() != 0 || !failedTask || !failedTool {
		t.Fatalf("操作失败被覆盖或重复执行: writes=%d reads=%d task=%v tool=%v", writes.Load(), reads.Load(), failedTask, failedTool)
	}
}

type peAlternatingResultTool struct {
	peResultTestTool
	next string
}

func (p *peAlternatingResultTool) InvokableRun(context.Context, string, ...einoTool.Option) (string, error) {
	if p.calls.Add(1) > 1 {
		return p.next, nil
	}
	return p.result, p.err
}
