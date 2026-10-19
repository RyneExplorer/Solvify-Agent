package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/pkg/config"
	apperrors "solvify-agent/pkg/errors"
)

type taskBudgetTool struct{ calls *atomic.Int32 }

func (taskBudgetTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "lookup_record", Desc: "读取资料"}, nil
}

func (t taskBudgetTool) InvokableRun(context.Context, string, ...einoTool.Option) (string, error) {
	t.calls.Add(1)
	return `{"success":true,"data":"资料"}`, nil
}

func TestPETaskBudgetMultiStepAndExhaustion(t *testing.T) {
	for _, exhaust := range []bool{false, true} {
		t.Run(map[bool]string{false: "多步查询并收尾", true: "最后一次不执行工具"}[exhaust], func(t *testing.T) {
			var tools, research, modelCalls atomic.Int32
			e := deepTestEngine()
			e.cfg.DeepMaxSubagentIterations = config.Default().Agent.DeepMaxSubagentIterations
			e.RegisterInternal("lookup_record", 1, false, DomainKnowledge, func(context.Context, string, []string) einoTool.BaseTool {
				return taskBudgetTool{calls: &tools}
			})
			m := peModel(`{"strategy":"sequential","tasks":[{"id":"a","agent":"knowledge_research","title":"列出资料并逐项核对"},{"id":"s","agent":"synthesis_review","title":"汇总","depends_on":["a"]}]}`, &research)
			base := m.respond
			m.respond = func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
				if strings.Contains(messages[0].Content, "研究知识库") {
					modelCalls.Add(1)
					if exhaust || tools.Load() < 4 {
						return peCall("lookup_record", map[string]any{}), nil
					}
					return schema.AssistantMessage("四次查询后取得研究结果", nil), nil
				}
				return base(ctx, messages)
			}
			events := collectPEEvents(t, context.Background(), e, Request{Query: "核对资料", UserID: "u", SessionID: "s"}, m)
			var done, failed bool
			for _, ev := range events {
				if ev.Type == EventDone {
					done = true
				}
				if ev.Type == EventError {
					failed = true
					if !exhaust || ev.Kind != apperrors.KindMaxIterations {
						t.Fatalf("非预期错误: %+v", ev)
					}
				}
			}
			if exhaust {
				if done || !failed || modelCalls.Load() != 12 || tools.Load() != 11 {
					t.Fatalf("耗尽终态或副作用边界错误: done=%v failed=%v models=%d tools=%d", done, failed, modelCalls.Load(), tools.Load())
				}
			} else if !done || failed || tools.Load() != 4 || modelCalls.Load() != 5 {
				t.Fatalf("无法完成多步任务: done=%v failed=%v models=%d tools=%d", done, failed, modelCalls.Load(), tools.Load())
			}
		})
	}
}

func TestPETaskModelRetriesShareBudgetAndKeepInput(t *testing.T) {
	var calls int
	b := &taskModelBudget{Limit: 3}
	original := []*schema.Message{schema.SystemMessage("任务指令"), schema.UserMessage("问题")}
	m := &peTaskModel{budget: b, inner: &deepTestModel{respond: func(_ context.Context, messages []*schema.Message) (*schema.Message, error) {
		calls++
		if !strings.Contains(messages[0].Content, "本任务模型调用额度") {
			t.Fatal("缺少剩余额度提示")
		}
		return nil, modelDNSTimeout()
	}}}
	bound, err := m.WithTools([]*schema.ToolInfo{{Name: "lookup_record"}})
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		_, _ = bound.Generate(context.Background(), original, model.WithMaxTokens(128))
	}
	if _, err = m.Generate(context.Background(), original); !errors.Is(err, adk.ErrExceedMaxIterations) || calls != 3 {
		t.Fatalf("重试重置预算: calls=%d err=%v", calls, err)
	}
	if original[0].Content != "任务指令" || len(original) != 2 {
		t.Fatal("修改了原始上下文")
	}
}

// 真实原生 checkpoint 跨独立进程恢复，验证新计数与审批执行状态一并保存。
func TestPETaskBudgetCheckpointProcess(t *testing.T) {
	dir := os.Getenv("SOLVIFY_PE_BUDGET_PROBE_DIR")
	phase := os.Getenv("SOLVIFY_PE_BUDGET_PROBE_PHASE")
	if dir == "" {
		dir = t.TempDir()
		for _, stage := range []string{"save", "restore"} {
			cmd := exec.Command(os.Args[0], "-test.run=^TestPETaskBudgetCheckpointProcess$", "-test.timeout=30s")
			cmd.Env = append(os.Environ(), "SOLVIFY_PE_BUDGET_PROBE_DIR="+dir, "SOLVIFY_PE_BUDGET_PROBE_PHASE="+stage)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", stage, err, out)
			}
		}
		return
	}
	var writes, reads, calls, research atomic.Int32
	e := deepTestEngine()
	e.cfg.DeepMaxSubagentIterations = 4
	if phase == "restore" {
		e.cfg.DeepMaxSubagentIterations = 12
	}
	ctx := withPrebuiltTools(context.Background(), prebuiltToolsBundle{Buckets: map[ToolDomain][]einoTool.BaseTool{
		DomainExternal: {deepApprovalTool{calls: &writes}, taskBudgetTool{calls: &reads}},
	}})
	req := Request{UserID: "u", SessionID: "s", RunID: "budget-probe", Query: "操作后核对资料", MaxContextLength: 64000, ModelName: "gpt-4o", OutputLimit: 4096}
	r := &peRuntime{engine: e, request: req}
	m := peModel(`{"strategy":"sequential","tasks":[{"id":"w","agent":"web_research","title":"执行后核对"},{"id":"s","agent":"synthesis_review","title":"汇总","depends_on":["w"]}]}`, &research)
	base := m.respond
	m.respond = func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
		if strings.Contains(messages[0].Content, "使用已挂载的外部工具") {
			calls.Add(1)
			for _, msg := range messages {
				if msg.Role == schema.Tool {
					return peCall("lookup_record", map[string]any{}), nil
				}
			}
			return peCall("write_file", map[string]any{}), nil
		}
		return base(ctx, messages)
	}
	r.model = m
	root, err := r.build(ctx)
	if err != nil {
		t.Fatal(err)
	}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: root, CheckPointStore: probeFileStore{dir: dir}})
	var iter *adk.AsyncIterator[*adk.AgentEvent]
	if phase == "save" {
		raw, _ := json.Marshal(&peState{Request: req, Results: map[string]TaskResult{}, Unsafe: map[string]bool{}, Failures: map[string]string{}})
		iter = runner.Run(ctx, []*schema.Message{schema.UserMessage(req.Query)}, adk.WithCheckPointID("budget"), adk.WithSessionValues(map[string]any{peSessionKey: string(raw)}))
	} else {
		target, readErr := os.ReadFile(filepath.Join(dir, "interrupt-id"))
		if readErr != nil {
			t.Fatal(readErr)
		}
		iter, err = runner.ResumeWithParams(ctx, "budget", &adk.ResumeParams{Targets: map[string]any{string(target): &ApprovalResult{Approved: true}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	var interrupted, exhausted bool
	for {
		ev, ok := iter.Next()
		if !ok {
			break
		}
		if ev.Err != nil {
			if !errors.Is(ev.Err, adk.ErrExceedMaxIterations) {
				t.Fatal(ev.Err)
			}
			exhausted = true
		}
		if ev.Action != nil && ev.Action.Interrupted != nil {
			for _, point := range ev.Action.Interrupted.InterruptContexts {
				if point.IsRootCause {
					if err := os.WriteFile(filepath.Join(dir, "interrupt-id"), []byte(point.ID), 0600); err != nil {
						t.Fatal(err)
					}
					interrupted = true
					break
				}
			}
		}
	}
	if phase == "save" {
		if !interrupted || exhausted || writes.Load() != 0 || calls.Load() != 1 {
			t.Fatalf("未正确暂停: writes=%d calls=%d", writes.Load(), calls.Load())
		}
	} else if interrupted || !exhausted || writes.Load() != 1 || reads.Load() != 2 || calls.Load() != 3 {
		t.Fatalf("恢复预算被重置或重复操作: interrupted=%v exhausted=%v writes=%d reads=%d calls=%d", interrupted, exhausted, writes.Load(), reads.Load(), calls.Load())
	}
}
