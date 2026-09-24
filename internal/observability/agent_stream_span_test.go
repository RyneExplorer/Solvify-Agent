package observability

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// mustState 取 OnStart* 之后挂在 ctx 上的 span 状态。
func mustState(t *testing.T, ctx context.Context) *einoSpanState {
	t.Helper()
	s, ok := ctx.Value(einoSpanKey{}).(*einoSpanState)
	if !ok || s == nil || s.span == nil {
		t.Fatal("ctx 里没有 einoSpanState/span")
	}
	return s
}

// newOneShotStream 造一个只有一条 item 的流。eino 要求 handler 消费并 Close 自己那份副本。
func newOneShotStream[T any](item T) *schema.StreamReader[T] {
	sr, sw := schema.Pipe[T](1)
	go func() {
		sw.Send(item, nil)
		sw.Close()
	}()
	return sr
}

// TestStreamInputComponentGetsItsOwnSpan 锁定「流式输入组件必须建自己的 span」。
//
// 回归背景：OnStart 与 OnStartWithStreamInput 是 eino 里**互斥**的两条 timing
// （compose/utils.go 按「入参是不是 *schema.StreamReader」二选一）。修复前只有
// OnStart 建 span，走流式输入的组件不建 —— 危害不止「少一个 span」：它的
// OnEnd 会从 ctx 里取到**上层 Agent** 留下的 span 引用，把子节点自己的身份属性
// （eino_comp / gen_ai.operation.name）写到父 span 上。线上那条 SolvifyDeepAgent
// 因此同时带着 Chain 的 eino_comp 与 invoke_workflow，三方平台按 op.name 分类时
// 把 Agent 渲染成了 workflow。
func TestStreamInputComponentGetsItsOwnSpan(t *testing.T) {
	rec := newTestRecorder(t)
	h := NewEinoCallbackHandler(rec)
	ctx := rec.WithTraceRoot(context.Background(), TraceRootAttrs{UserID: "u1", SessionID: "s1"})

	// ① Agent 级 OnStart（普通 timing）
	ctx = h.OnStart(ctx, &callbacks.RunInfo{
		Name:      "SolvifyDeepAgent",
		Component: adk.ComponentOfAgent,
	}, nil)
	agentSpan := mustState(t, ctx).span

	// ② 内部 Chain 走流式输入 timing
	ctx2 := h.OnStartWithStreamInput(ctx, &callbacks.RunInfo{
		Name:      "SolvifyDeepAgent",
		Component: compose.ComponentOfChain,
	}, newOneShotStream[callbacks.CallbackInput](&model.CallbackInput{}))

	chainState, ok := ctx2.Value(einoSpanKey{}).(*einoSpanState)
	if !ok || chainState == nil || chainState.span == nil {
		t.Fatal("流式输入组件没有自己的 span —— 它的 OnEnd 会把子节点身份写到上层 span 上")
	}
	if chainState.span == agentSpan {
		t.Fatal("流式输入组件复用了上层 Agent 的 span —— 子节点身份会覆盖父节点（冒名）")
	}
	if chainState.span.ParentID != agentSpan.SpanID {
		t.Errorf("流式输入 span 的 parent 应为上层 Agent span：期望 %s，实际 %s",
			agentSpan.SpanID, chainState.span.ParentID)
	}
	if got, _ := chainState.span.Attrs["eino_comp"].(string); got != string(compose.ComponentOfChain) {
		t.Errorf("流式输入 span 的 eino_comp = %q，期望 %q", got, string(compose.ComponentOfChain))
	}
	if got, _ := chainState.span.Attrs["stream_input"].(bool); !got {
		t.Error("流式输入 span 缺少 stream_input 标记，无法与普通 OnStart 路径区分")
	}
}

// TestStreamChainEndDoesNotHijackAgentSpan 直接锁定「冒名」现象本身。
//
// 上一条钉的是根因（没有自己的 span），这一条钉的是症状：
// Chain 走完 OnEndWithStreamOutput 之后，Agent span 上不能出现 Chain 的身份属性。
func TestStreamChainEndDoesNotHijackAgentSpan(t *testing.T) {
	rec := newCaptureRecorder(t)
	h := NewEinoCallbackHandler(rec)
	ctx := rec.WithTraceRoot(context.Background(), TraceRootAttrs{UserID: "u1", SessionID: "s1"})

	ctx = h.OnStart(ctx, &callbacks.RunInfo{
		Name:      "SolvifyDeepAgent",
		Component: adk.ComponentOfAgent,
	}, nil)
	agentSpan := mustState(t, ctx).span

	ctx2 := h.OnStartWithStreamInput(ctx, &callbacks.RunInfo{
		Name:      "SolvifyDeepAgent",
		Component: compose.ComponentOfChain,
	}, newOneShotStream[callbacks.CallbackInput](&model.CallbackInput{}))
	chainSpan := mustState(t, ctx2).span

	h.OnEndWithStreamOutput(ctx2, &callbacks.RunInfo{
		Name:      "SolvifyDeepAgent",
		Component: compose.ComponentOfChain,
	}, newOneShotStream[callbacks.CallbackOutput](&model.CallbackOutput{}))

	// einoOnStreamEnd 是异步 EndSpan（要等流读完），用 channel 同步等它。
	ended := waitSpan(t, rec)
	if ended != chainSpan {
		t.Fatalf("结束的不是 Chain 自己的 span —— 说明它拿到的是另一条 span 的引用")
	}

	// Agent span 必须保持 Agent 的身份
	if got, _ := agentSpan.Attrs["eino_comp"].(string); got == string(compose.ComponentOfChain) {
		t.Errorf("Chain 把自己的 eino_comp(%q) 写到了 Agent span 上（冒名）", got)
	}
	if got, _ := agentSpan.Attrs[AttrGenAIOperationName].(string); got == genAIOpInvokeFlow {
		t.Errorf("Chain 把自己的 gen_ai.operation.name(%q) 写到了 Agent span 上（冒名）", got)
	}
	// Chain span 自己的身份必须是对的
	if got, _ := chainSpan.Attrs["eino_comp"].(string); got != string(compose.ComponentOfChain) {
		t.Errorf("Chain span 的 eino_comp = %q，期望 %q", got, string(compose.ComponentOfChain))
	}
}

// TestAgentStartAttrsDistinguishResume 锁定「首次执行 / 恢复执行」可区分。
//
// 判据取自 AgentCallbackInput.ResumeInfo（adk 只在 Resume 分支填它）。
// 项目里 runner.ResumeWithParams 是真实用到的（打断点审批 / 澄清追问），
// 不读这个字段的话，追踪上完全分不清一次 run 是首次还是恢复。
func TestAgentStartAttrsDistinguishResume(t *testing.T) {
	rec := newTestRecorder(t)
	h := NewEinoCallbackHandler(rec)
	base := rec.WithTraceRoot(context.Background(), TraceRootAttrs{UserID: "u1", SessionID: "s1"})

	fresh := h.OnStart(base, &callbacks.RunInfo{
		Name: "A", Component: adk.ComponentOfAgent,
	}, &adk.AgentCallbackInput{})
	if got := mustState(t, fresh).span.Attrs["agent.resumed"]; got != false {
		t.Errorf("全新执行的 agent.resumed = %v，期望 false", got)
	}

	resumed := h.OnStart(base, &callbacks.RunInfo{
		Name: "A", Component: adk.ComponentOfAgent,
	}, &adk.AgentCallbackInput{ResumeInfo: &adk.ResumeInfo{WasInterrupted: true, IsResumeTarget: true}})
	a := mustState(t, resumed).span.Attrs
	if a["agent.resumed"] != true {
		t.Errorf("恢复执行的 agent.resumed = %v，期望 true", a["agent.resumed"])
	}
	if a["agent.was_interrupted"] != true {
		t.Errorf("恢复执行的 agent.was_interrupted = %v，期望 true", a["agent.was_interrupted"])
	}
}

// TestChatModelSpanCarriesIteration 锁定「第几轮」在 span 上可见。
//
// 框架不提供 per-iteration 的轮次边界回调（adk/react.go 的循环体只递减迭代计数），
// 所以轮次只能自己数：Agent 级 OnStart 放一个计数器进 ctx，ChatModel 每次调用递增。
// 没有它就只能靠「同层若干个同名 ChatModel span 排排坐」去数轮数。
func TestChatModelSpanCarriesIteration(t *testing.T) {
	rec := newTestRecorder(t)
	h := NewEinoCallbackHandler(rec)
	base := rec.WithTraceRoot(context.Background(), TraceRootAttrs{UserID: "u1", SessionID: "s1"})

	// 不在 Agent 之下的 ChatModel 不该带轮次
	solo := h.OnStart(base, &callbacks.RunInfo{
		Name: "cm", Component: components.ComponentOfChatModel,
	}, &model.CallbackInput{})
	if _, ok := mustState(t, solo).span.Attrs["agent.iteration"]; ok {
		t.Error("不在 Agent 之下的 ChatModel 不该带 agent.iteration")
	}

	// Agent 之下连续三次模型调用 ⇒ 1 / 2 / 3
	actx := h.OnStart(base, &callbacks.RunInfo{
		Name: "A", Component: adk.ComponentOfAgent,
	}, nil)
	for want := 1; want <= 3; want++ {
		c := h.OnStart(actx, &callbacks.RunInfo{
			Name: "cm", Component: components.ComponentOfChatModel,
		}, &model.CallbackInput{})
		if got := mustState(t, c).span.Attrs["agent.iteration"]; got != want {
			t.Errorf("第 %d 次模型调用的 agent.iteration = %v（%T），期望 %d", want, got, got, want)
		}
	}
}
