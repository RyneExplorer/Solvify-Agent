package agent

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/pkg/config"
)

// 这一组走**生产路径**：Engine.Execute → runQuickAgent → adk.NewChatModelAgent → adk.Runner。
//
// 为什么不直接构造 budgetGuard 来测：单测验的是「这个结构体被调用时会做什么」，
// 验不到「它有没有被接上」。接上那一步在 quick_agent.go 的 Handlers 字段里，
// 只有走一遍真实装配才能覆盖（否则把 Handlers 整行删掉，单测照样全绿）。
//
// 覆盖面（对应计划 §7 的 V1/V2）：
//   V1 首次执行：窗口充足时保留完整工具结果；
//   V2 中断恢复：恢复后的那一轮模型调用同样过检查 —— 恢复不重新装配，
//     但预算检查是挂在 Agent 上的，不能因此跳过。

const guardProbeRaw = "检索结果内容"

// guardProbeTail 只钉在正文**末尾**：截断是砍尾巴，所以「哨兵还在」就等价于
// 「没有截断」。反过来用「是否还含正文片段」判是恒真的 —— 截断后的前缀仍然是
// 那 800 遍原文的开头，Contains 照样为真（这条第一版就撞上了）。
const guardProbeTail = "<<TAIL-SENTINEL-ONLY-SURVIVES-WITHOUT-TRUNCATION>>"

// guardProbeTool 返回带末尾哨兵的长结果，用于验证原文保留。
type guardProbeTool struct{ calls *atomic.Int32 }

func (t guardProbeTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "guard_probe", Desc: "返回超长结果的探针工具"}, nil
}

func (t guardProbeTool) InvokableRun(context.Context, string, ...einoTool.Option) (string, error) {
	t.calls.Add(1)
	return strings.Repeat(guardProbeRaw, 800) + guardProbeTail, nil
}

// guardClarifyTool 只负责「这个名字在工具集里存在」：真正的 ask_clarify
// 由澄清中间件在调用前中断，工具体不会被执行。
type guardClarifyTool struct{}

func (guardClarifyTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: clarifyToolName, Desc: "澄清追问"}, nil
}

func (guardClarifyTool) InvokableRun(context.Context, string, ...einoTool.Option) (string, error) {
	return "", errors.New("ask_clarify 应被澄清中间件中断，工具体不应执行")
}

// guardTestEngine 注册两个工具：知识域探针 + 澄清域工具。
// 注册顺序决定域内顺序，必须与下面 prebuilt 桶的顺序一致（toolsInDomains 按下标取用）。
func guardTestEngine() *Engine {
	e := &Engine{cfg: config.AgentConfig{QuickAgentMaxIterations: 5}}
	build := func(t einoTool.BaseTool) ToolBuildFn {
		return func(context.Context, string, []string) einoTool.BaseTool { return t }
	}
	e.RegisterInternal("guard_probe", 1, false, DomainKnowledge, build(guardProbeTool{}))
	e.RegisterInternal(clarifyToolName, 2, false, DomainClarify, build(guardClarifyTool{}))
	return e
}

func guardTestContext(probe guardProbeTool) context.Context {
	return withPrebuiltTools(context.Background(), prebuiltToolsBundle{
		Buckets: map[ToolDomain][]einoTool.BaseTool{
			DomainKnowledge: {probe},
			DomainClarify:   {guardClarifyTool{}},
		},
	})
}

func collectGuardEvents(t *testing.T, events <-chan Event) []Event {
	t.Helper()
	var out []Event
	for {
		select {
		case ev, ok := <-events:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-time.After(10 * time.Second):
			t.Fatal("快速模式执行未结束")
			return out
		}
	}
}

// toolContentByCallID 按 tool_call_id 取回对应的工具结果。
//
// ⚠️ 不能取「最后一条 Role=Tool 的消息」：恢复之后状态里最后一条工具结果是
// **澄清中间件**写回去的短文本，拿它来断言「超长结果被截断」是恒真的。
func toolContentByCallID(msgs []*schema.Message, callID string) (string, bool) {
	for _, m := range msgs {
		if m != nil && m.Role == schema.Tool && m.ToolCallID == callID {
			return m.Content, true
		}
	}
	return "", false
}

func TestQuickAgentBudgetGuardOnFirstRunAndResume(t *testing.T) {
	var probe guardProbeTool
	probe.calls = &atomic.Int32{}

	var seen [][]*schema.Message
	var calls int32
	model := &deepTestModel{respond: func(_ context.Context, msgs []*schema.Message) (*schema.Message, error) {
		if strings.HasPrefix(msgs[0].Content, "总结已完成交互") {
			return schema.AssistantMessage("旧检索已完成，继续使用最新检索结果。", nil), nil
		}
		snap := make([]*schema.Message, len(msgs))
		copy(snap, msgs)
		seen = append(seen, snap)

		switch atomic.AddInt32(&calls, 1) {
		case 1: // 首次执行：先调用探针工具拿一条超长结果
			return schema.AssistantMessage("", []schema.ToolCall{{
				ID: "call-probe", Type: "function",
				Function: schema.FunctionCall{Name: "guard_probe", Arguments: "{}"},
			}}), nil
		case 2: // 拿到工具结果后的这一轮：检查已生效，接着触发澄清中断
			return schema.AssistantMessage("", []schema.ToolCall{{
				ID: "call-clarify", Type: "function",
				Function: schema.FunctionCall{Name: clarifyToolName, Arguments: `{"question":"要哪个口径？"}`},
			}}), nil
		case 3: // 用户回答后继续检索，第四轮才能生成最终回答。
			if answer, ok := toolContentByCallID(msgs, "call-clarify"); !ok || !strings.Contains(answer, "用第二个口径") {
				t.Fatal("恢复后模型未收到用户澄清回答")
			}
			return schema.AssistantMessage("", []schema.ToolCall{{
				ID: "call-probe-again", Type: "function",
				Function: schema.FunctionCall{Name: "guard_probe", Arguments: "{}"},
			}}), nil
		default: // 恢复之后：直接给最终回答
			return schema.AssistantMessage("最终回答", nil), nil
		}
	}}

	req := Request{
		Profile:   ProfileQuick,
		Query:     "帮我查一下这个超长结果",
		UserID:    "u1",
		SessionID: "s1",
		// 走离屏模型 ID，避免 llm 包里的运行时降级表干扰窗口取值。
		// 窗口足以容纳探针原文，首次执行与恢复都应保留完整证据。
		ModelID: "e2e-budget-guard-window", ModelName: "gpt-4o", MaxContextLength: 64000,
	}

	// ── 首次执行 ──
	ctx := guardTestContext(probe)
	first := guardTestEngine()
	first.cfg.QuickAgentMaxIterations = 2 // 旧配置同样必须容纳四轮链路。
	events := collectGuardEvents(t, mustExecute(t, ctx, first, req, model))

	var interrupt Event
	for _, ev := range events {
		if ev.Type == EventError {
			t.Fatalf("首次执行报错: %+v", ev)
		}
		if ev.Type == EventInterrupt {
			interrupt = ev
		}
	}
	if probe.calls.Load() != 1 {
		t.Fatalf("探针工具调用次数=%d 期望 1", probe.calls.Load())
	}
	if interrupt.CheckpointID == "" || !interrupt.IsClarify {
		t.Fatalf("未拿到澄清中断: %+v", interrupt)
	}
	if len(seen) != 2 {
		t.Fatalf("首次执行应有 2 次模型调用，实际 %d", len(seen))
	}

	// V1：第2次模型调用能看到窗口容得下的完整工具结果。
	toolContent, ok := toolContentByCallID(seen[1], "call-probe")
	if !ok {
		t.Fatal("第 2 次模型调用里没有探针工具的结果 ⇒ 探针路径没走到")
	}
	if toolContent != strings.Repeat(guardProbeRaw, 800)+guardProbeTail {
		t.Fatal("模型未看到完整工具证据")
	}

	// ── 中断恢复（换一个 Engine：模拟「恢复时是另一个进程/实例」）──
	// 内存 checkpoint store 是 per-Engine 的，跨实例恢复必须显式搬运字节 ——
	// 这一步本身就是「checkpoint 内容可移植」的检查，别跳过。
	req.CheckpointID = interrupt.CheckpointID
	req.ResumeData = map[string]any{interrupt.InterruptID: "用第二个口径"}
	other := guardTestEngine()
	other.cfg.QuickAgentMaxIterations = 2
	blob, found, err := first.buildCheckpointStore(req.SessionID).Get(ctx, interrupt.CheckpointID)
	if err != nil || !found {
		t.Fatalf("根 checkpoint 未保存: found=%v err=%v", found, err)
	}
	if err := other.buildCheckpointStore(req.SessionID).Set(ctx, interrupt.CheckpointID, blob); err != nil {
		t.Fatalf("搬运 checkpoint 失败: %v", err)
	}
	seen = nil

	resumed := collectGuardEvents(t, mustExecute(t, guardTestContext(probe), other, req, model))
	done := false
	for _, ev := range resumed {
		if ev.Type == EventError {
			t.Fatalf("恢复执行报错: %+v", ev)
		}
		if ev.Type == EventDone {
			done = true
		}
	}
	if !done {
		t.Fatalf("恢复执行未完成: %+v", resumed)
	}
	if calls != 4 || probe.calls.Load() != 2 {
		t.Fatalf("应完成四轮模型调用和两次检索：模型=%d，检索=%d", calls, probe.calls.Load())
	}

	// V2：换Engine恢复后完整证据仍保留，预算检查不额外丢失尾部内容。
	if len(seen) == 0 {
		t.Fatal("恢复后没有任何模型调用")
	}
	toolContent, ok = toolContentByCallID(seen[0], "call-probe")
	if !ok {
		t.Fatal("恢复后的模型调用里没有探针工具的结果")
	}
	if toolContent != strings.Repeat(guardProbeRaw, 800)+guardProbeTail {
		t.Fatal("恢复后完整证据丢失")
	}
}

func mustExecute(t *testing.T, ctx context.Context, e *Engine, req Request, m *deepTestModel) <-chan Event {
	t.Helper()
	if req.MaxContextLength == 0 {
		req.MaxContextLength = 64000
		req.ModelName = "gpt-4o"
	}
	events, err := e.Execute(ctx, req, m)
	if err != nil {
		t.Fatalf("Execute 失败: %v", err)
	}
	return events
}
