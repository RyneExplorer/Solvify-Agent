package observability

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"
)

// 这一组测试覆盖「自研轨孤儿 trace」缺陷的回归。
//
// 缺陷现象：一次 chat 请求在前端 trace 列表里出现**两条**记录 ——
// 一条是正常的 chat.request（user/session 齐全），另一条 user_id 或 session_id 是 unknown，
// 点进去只有 ctx.summarize / ctx.extract_memories 一个孤零零的节点，和真正的会话完全割裂。
//
// 成因是两个缺陷叠加：
//
//	① DetachedTraceContext 只搬了 OTel 的 SpanContext，没搬自研轨的
//	   traceIDKey / currentSpanKey / rootAttrsKey。后台 span 因此自己掷出一个随机 traceID、
//	   按「无父根 span」登记，EndSpan 时走 finalizeTrace 另写一行；同时因为 rootAttrsKey 丢了，
//	   那一行只能从 span attrs 上回捞归属，捞不到谁就是 unknown。
//	② 就算身份搬对了，publishTrace 里那次 LoadAndDelete 已经把 traceState 删掉了，
//	   请求结束后才 End 的后台 span 无处可归，仍然会被丢掉或另写一行。
//
// 所以断言分两层：身份必须搬全（Part A），落库必须收敛成一行（Part B）。

// tracesFor 返回 DBSink 收到的、自研 traceID == id 的全部落库记录（按到达顺序）。
// 首发 + 迟到重发会对同一行 upsert 多次，因此同 ID 出现多条是预期行为。
func (s *capturingDBSink) tracesFor(id string) []*Trace {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*Trace
	for _, tr := range s.traces {
		if tr != nil && tr.ID == id {
			out = append(out, tr)
		}
	}
	return out
}

// distinctTraceIDs 返回 DBSink 收到的所有互不相同的自研 traceID 及其出现次数。
// 这是「有没有裂行」最直接的判据：一次 chat 应当只对应一个 ID。
func (s *capturingDBSink) distinctTraceIDs() map[string]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]int{}
	for _, tr := range s.traces {
		if tr != nil {
			out[tr.ID]++
		}
	}
	return out
}

// spanNamesIn 深度遍历 span 树，返回出现的所有 span 名。
func spanNamesIn(root *Span) []string {
	if root == nil {
		return nil
	}
	out := []string{root.Name}
	for _, c := range root.Children {
		out = append(out, spanNamesIn(c)...)
	}
	return out
}

func hasSpanNamed(root *Span, name string) bool {
	for _, n := range spanNamesIn(root) {
		if n == name {
			return true
		}
	}
	return false
}

// installNoopTracer 把全局 tracer 换成 noop，模拟 OTelExporter=noop 的开发环境
// （SpanContext 恒为 invalid）。测试结束还原。
func installNoopTracer(t *testing.T) {
	t.Helper()
	prev := globalTracer
	globalTracer = trace.NewNoopTracerProvider().Tracer("orphan-trace-noop")
	t.Cleanup(func() { globalTracer = prev })
}

// chatChain 按真实链路顺序搭好一条 chat trace。
//
//	HTTP 根 span（gin 中间件建的，registry 里登记的就是它）
//	  → WithTraceRoot（chat 服务绑定 user/session/message）
//	  → chat.deep（业务根 span，挂在 http.request 下）
//
// 注意中间根不是登记根：StartSpan 只在「无父」时登记 traceState，
// 所以真实链路里 st.Trace.Root 是 http.request，chat.deep 是它的子节点。
// 测试必须照这个形状搭，否则会测出一条现实中不存在的树。
//
// 返回 ctx（已含 currentSpanKey=chatSpan）、httpSpan、chatSpan。
func chatChain(t *testing.T, rec Recorder) (context.Context, *Span, *Span) {
	t.Helper()
	ctx, httpSpan := rec.StartSpan(context.Background(), "http.request", ComponentHTTPServer, nil)
	ctx = rec.WithTraceRoot(ctx, TraceRootAttrs{
		UserID:     "u-1",
		SessionID:  "s-1",
		MessageID:  "m-1",
		SearchMode: "deep",
		ModelID:    "deepseek-v4-flash",
	})
	ctx, chatSpan := rec.StartSpan(ctx, "chat.deep", ComponentAgentEngine, Attrs{"session_id": "s-1"})
	return ctx, httpSpan, chatSpan
}

// TestDetachedTraceContextKeepsSelfHostedIdentity 断言 DetachedTraceContext 把两条轨道的身份
// 都搬过去，并且真的切断了取消信号。
//
// 回归价值：把该函数改回「只 return trace.ContextWithSpanContext(Background(), sc)」，
// 下面 traceID / currentSpan / rootAttrs 三条断言立刻变红 —— 这正是孤儿 trace 的第一层成因。
func TestDetachedTraceContextKeepsSelfHostedIdentity(t *testing.T) {
	installSDKTracer(t)

	rec := newDualTrackRecorder(t, &capturingDBSink{})
	parent, cancel := context.WithCancel(context.Background())
	ctx, span := rec.StartSpan(parent, "chat.deep", ComponentAgentEngine, nil)
	ctx = rec.WithTraceRoot(ctx, TraceRootAttrs{UserID: "u-1", SessionID: "s-1"})

	detached := DetachedTraceContext(ctx)

	// 自研轨三件套：traceID / 当前 span / 根属性，缺一个后台 span 就会另起一行
	if got := TraceIDFromContext(detached); got != span.TraceID {
		t.Errorf("自研 traceID 没搬过去: got=%q want=%q", got, span.TraceID)
	}
	if got := CurrentSpanFromContext(detached); got != span {
		t.Errorf("当前自研 span 没搬过去，后台 span 会变成无父根 span: got=%p want=%p", got, span)
	}
	if detached.Value(rootAttrsKey) == nil {
		t.Error("根属性(rootAttrsKey)没搬过去，后台 span 会拿不到 user/session 归属")
	}

	// OTel 轨也要在（这里装了真实 SDK tracer）
	if sc := trace.SpanContextFromContext(detached); !sc.IsValid() {
		t.Error("OTel SpanContext 没搬过去，三方平台上会断链")
	}

	// 取消信号必须被切断：响应返回后请求 ctx 被取消，后台任务不能被连带打断
	cancel()
	if err := detached.Err(); err != nil {
		t.Errorf("DetachedTraceContext 不该继承取消信号，Err()=%v", err)
	}
}

// TestDetachedTraceContextKeepsIdentityWithoutOTelExport 覆盖开发环境（OTelExporter=noop）：
// OTel SpanContext 无效时，自研轨身份也必须保留。
//
// 回归价值：若在实现里写成「sc 无效就整体退化成 Background」，本测试变红 ——
// 而 noop exporter 正是开发环境的默认配置，也就是说旧实现在开发环境同样裂行。
func TestDetachedTraceContextKeepsIdentityWithoutOTelExport(t *testing.T) {
	installNoopTracer(t)

	rec := newDualTrackRecorder(t, &capturingDBSink{})
	ctx, span := rec.StartSpan(context.Background(), "chat.deep", ComponentAgentEngine, nil)
	ctx = rec.WithTraceRoot(ctx, TraceRootAttrs{UserID: "u-1", SessionID: "s-1"})

	detached := DetachedTraceContext(ctx)

	if sc := trace.SpanContextFromContext(detached); sc.IsValid() {
		t.Fatal("前置条件不成立：noop tracer 下 SpanContext 应当无效")
	}
	if got := TraceIDFromContext(detached); got != span.TraceID {
		t.Errorf("noop exporter 下自研 traceID 也必须保留: got=%q want=%q", got, span.TraceID)
	}
	if got := CurrentSpanFromContext(detached); got != span {
		t.Errorf("noop exporter 下当前自研 span 也必须保留: got=%p want=%p", got, span)
	}
	if detached.Value(rootAttrsKey) == nil {
		t.Error("noop exporter 下根属性也必须保留")
	}
}

// TestLateBackgroundSpanMergesIntoPublishedTrace 是本次修复的核心回归：
// 后台 span 在主 trace 已经落库之后才 End，必须被并回**同一行** chat_traces，
// 而不是新写一行 user/session=unknown 的孤儿 trace。
//
// 回归价值：把 publishTrace 里的保活改造还原成 LoadAndDelete、或删掉 EndSpan 里的
// 迟到 span 分支，本测试的 distinctTraceIDs 就会变成 2 个，直接变红。
func TestLateBackgroundSpanMergesIntoPublishedTrace(t *testing.T) {
	installSDKTracer(t)

	sink := &capturingDBSink{}
	rec := newDualTrackRecorder(t, sink)

	ctx, _, chatSpan := chatChain(t, rec)

	// 真实调用顺序：refreshContextAsync 在响应返回前先取一次 detached ctx，
	// 后台 goroutine 才在响应返回后真正跑（chat_service_mode.go:317）。
	baseCtx := DetachedTraceContext(ctx)

	rec.EndSpan(ctx, chatSpan, SpanStatusOK, nil, nil)
	selfTraceID := rec.FlushTrace(ctx, "u-1", "s-1", "m-1")
	if selfTraceID == "" {
		t.Fatal("FlushTrace 返回了空的自研 traceID")
	}
	// 首发必须已经落库，且只有一行
	if n := len(sink.distinctTraceIDs()); n != 1 {
		t.Fatalf("首发后应当只有 1 个 traceID，实际 %d 个: %v", n, sink.distinctTraceIDs())
	}
	first := sink.tracesFor(selfTraceID)
	if len(first) == 0 {
		t.Fatal("首发没有落库")
	}
	if hasSpanNamed(first[len(first)-1].Root, "ctx.summarize") {
		t.Fatal("前置条件不成立：此时还不该有 ctx.summarize")
	}

	// 响应已经结束、trace 已经落库，后台任务这时才开跑
	bgCtx, bgSpan := rec.StartSpan(baseCtx, "ctx.summarize", ComponentServiceContext, Attrs{"session_id": "s-1"})
	if bgCtx == nil || bgSpan == nil {
		t.Fatal("后台 span 创建失败")
	}
	if bgSpan.TraceID != selfTraceID {
		// 自研轨身份没传下去。这里只记 Error 不中断：让下面的断言把「裂成两行」这个
		// 最终后果完整打出来，比停在中间状态更有诊断价值。
		t.Errorf("后台 span 没有继承自研 traceID（会另写一行孤儿 trace）: got=%q want=%q",
			bgSpan.TraceID, selfTraceID)
	}
	rec.EndSpan(bgCtx, bgSpan, SpanStatusOK, nil, nil)

	// 等防抖窗口过去，重发应当已经发生（不是 sleep 代替断言：下面会校验结果）
	waitFor(t, 5*time.Second, func() bool {
		if len(sink.distinctTraceIDs()) > 1 {
			return true // 已经裂行，不必再等，直接进入断言把证据打出来
		}
		got := sink.tracesFor(selfTraceID)
		if len(got) < 2 {
			return false
		}
		return hasSpanNamed(got[len(got)-1].Root, "ctx.summarize")
	}, "等待迟到 span 重发落库超时")

	// ① 没有裂行：自始至终只有一个 traceID
	ids := sink.distinctTraceIDs()
	if len(ids) != 1 {
		t.Errorf("裂行了：期望 1 个 traceID，实际 %d 个 %v —— 后台 span 新写了一行 chat_traces", len(ids), ids)
	}
	if _, ok := ids[selfTraceID]; !ok {
		t.Errorf("落库的 traceID 与 FlushTrace 返回的不一致: %v", ids)
	}

	// ② 同一行的最终版本里，后台 span 挂在 chat.deep 下面，归属完整
	all := sink.tracesFor(selfTraceID)
	last := all[len(all)-1]
	if last.Root == nil {
		t.Fatal("最终落库 trace 的 Root 为 nil")
	}
	if !hasSpanNamed(last.Root, "chat.deep") {
		t.Errorf("最终树里丢了 chat.deep，实际 span: %v", spanNamesIn(last.Root))
	}
	if !hasSpanNamed(last.Root, "ctx.summarize") {
		t.Errorf("最终树里没有 ctx.summarize，实际 span: %v", spanNamesIn(last.Root))
	}
	if last.UserID != "u-1" {
		t.Errorf("最终落库的 user_id 应为 u-1，实际 %q（unknown 就是孤儿 trace 的特征）", last.UserID)
	}
	if last.SessionID != "s-1" {
		t.Errorf("最终落库的 session_id 应为 s-1，实际 %q（unknown 就是孤儿 trace 的特征）", last.SessionID)
	}
}

// TestBackgroundSpanEndingBeforeFlushStaysSingleRow 是上面那条的对照组：
// 后台 span 在 FlushTrace **之前**就结束，首发应当直接带上它，
// 且不允许因为「树变了」被无限重发（收敛性）。
func TestBackgroundSpanEndingBeforeFlushStaysSingleRow(t *testing.T) {
	installSDKTracer(t)

	sink := &capturingDBSink{}
	rec := newDualTrackRecorder(t, sink)

	ctx, _, chatSpan := chatChain(t, rec)
	baseCtx := DetachedTraceContext(ctx)

	// 后台 span 在发布之前就跑完
	bgCtx, bgSpan := rec.StartSpan(baseCtx, "ctx.extract_memories", ComponentServiceContext, Attrs{"user_id": "u-1"})
	rec.EndSpan(bgCtx, bgSpan, SpanStatusOK, nil, nil)

	rec.EndSpan(ctx, chatSpan, SpanStatusOK, nil, nil)
	selfTraceID := rec.FlushTrace(ctx, "u-1", "s-1", "m-1")

	// 给潜在的多余重发留出时间：即使多发几次也必须是同一行
	time.Sleep(2 * traceRepublishDebounce)
	ids := sink.distinctTraceIDs()
	if len(ids) != 1 {
		t.Errorf("裂行了：期望 1 个 traceID，实际 %d 个 %v", len(ids), ids)
	}
	all := sink.tracesFor(selfTraceID)
	if len(all) == 0 {
		t.Fatal("没有落库")
	}
	last := all[len(all)-1]
	if !hasSpanNamed(last.Root, "ctx.extract_memories") {
		t.Errorf("首发树里应当已含 ctx.extract_memories，实际 span: %v", spanNamesIn(last.Root))
	}
	if last.UserID != "u-1" || last.SessionID != "s-1" {
		t.Errorf("归属不该丢: user=%q session=%q", last.UserID, last.SessionID)
	}
}

// TestRepublishKeepsSamplingDecision 断言迟到重发复用首发的采样决定，不会重掷骰子。
//
// 采样率置 0 且不开启「错误必采」时首发不落库，重发也必须保持不落库 ——
// 否则同一条 trace 会在两次写入之间「采样结果翻转」，行凭空冒出来。
func TestRepublishKeepsSamplingDecision(t *testing.T) {
	installSDKTracer(t)

	cfg := testObsConfig()
	cfg.SamplingRate = 0
	cfg.ErrorAlwaysSample = false
	sink := &capturingDBSink{}
	rec := NewRecorderWithDBSink(cfg, sink)
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = rec.Shutdown(c)
	})

	ctx, _, chatSpan := chatChain(t, rec)
	baseCtx := DetachedTraceContext(ctx)
	rec.EndSpan(ctx, chatSpan, SpanStatusOK, nil, nil)
	rec.FlushTrace(ctx, "u-1", "s-1", "m-1")

	bgCtx, bgSpan := rec.StartSpan(baseCtx, "ctx.summarize", ComponentServiceContext, nil)
	rec.EndSpan(bgCtx, bgSpan, SpanStatusOK, nil, nil)

	time.Sleep(2 * traceRepublishDebounce)
	if ids := sink.distinctTraceIDs(); len(ids) != 0 {
		t.Errorf("采样率为 0 时不该落库，重发也不该翻转决定，实际: %v", ids)
	}
}

// TestReleaseTraceStateStopsGCAfterRenewal 断言保活窗口被续期后，先排的那次 GC 不会误删状态。
// 这是「窗口内还有后台任务在跑」时最容易踩的坑：抢跑回收会让后续迟到 span 重新裂行。
func TestReleaseTraceStateStopsGCAfterRenewal(t *testing.T) {
	rec := NewRecorderWithDBSink(testObsConfig(), &capturingDBSink{})
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = rec.Shutdown(c)
	})
	dr := rec.(*defaultRecorder)

	st := dr.ensureTraceState("t-renew")
	st.mu.Lock()
	st.graceUntil = time.Now().Add(time.Hour) // 已被续期
	st.mu.Unlock()
	dr.traceStates.Store("t-renew", st)

	// 模拟一次「已经过时的 GC」触发：必须看到续期后主动放弃
	dr.releaseTraceState("t-renew")
	if dr.loadTraceState("t-renew") == nil {
		t.Error("保活窗口被续期后，过时的 GC 不该删除状态")
	}

	// 窗口真的到期后必须能正常回收
	st.mu.Lock()
	st.graceUntil = time.Now().Add(-time.Second)
	st.mu.Unlock()
	dr.releaseTraceState("t-renew")
	if dr.loadTraceState("t-renew") != nil {
		t.Error("保活窗口到期后状态应被回收")
	}
}

// TestEndingRegisteredRootAfterPublishDoesNotOverwriteTrace 覆盖保活窗口带来的新风险：
// 真实链路里 gin 中间件的 http.request 根 span 在 SSE 收尾、FlushTrace **之后**才 End，
// 而它恰好是 traceState 里登记的根（st.Trace.Root）。
//
// 旧实现靠 publishTrace 里的 LoadAndDelete 顺手挡住了这条路（状态已被删，EndSpan 找不到），
// 改成保活后状态还在，如果不加判断就会再走一次 finalizeTrace —— 用「直接根」形态
// （根 = http.request，归属只从 span attrs 取，而中间件跑在鉴权之前，attrs 里没有 user_id）
// 把刚写好的行覆盖成 user/session=unknown。这正是要防的「孤儿 trace」的另一种形态。
func TestEndingRegisteredRootAfterPublishDoesNotOverwriteTrace(t *testing.T) {
	installSDKTracer(t)

	sink := &capturingDBSink{}
	rec := newDualTrackRecorder(t, sink)

	ctx, httpSpan, chatSpan := chatChain(t, rec)
	rec.EndSpan(ctx, chatSpan, SpanStatusOK, nil, nil)
	selfTraceID := rec.FlushTrace(ctx, "u-1", "s-1", "m-1")

	before := sink.tracesFor(selfTraceID)
	if len(before) == 0 {
		t.Fatal("首发没有落库")
	}

	// 请求收尾：gin 中间件结束它自己建的 http.request 根 span
	rec.EndSpan(context.Background(), httpSpan, SpanStatusOK, nil, nil)
	time.Sleep(2 * traceRepublishDebounce)

	ids := sink.distinctTraceIDs()
	if len(ids) != 1 {
		t.Errorf("裂行了：期望 1 个 traceID，实际 %d 个 %v", len(ids), ids)
	}
	all := sink.tracesFor(selfTraceID)
	last := all[len(all)-1]
	if last.UserID != "u-1" || last.SessionID != "s-1" {
		t.Errorf("http 根 span 结束时覆盖了归属信息: user=%q session=%q（应为 u-1 / s-1）",
			last.UserID, last.SessionID)
	}
	if last.Root == nil {
		t.Fatal("最终落库 trace 的 Root 为 nil")
	}
	if last.Root.Name != "chat.request" {
		t.Errorf("发布根被换掉了：期望 chat.request，实际 %q", last.Root.Name)
	}
	if !hasSpanNamed(last.Root, "chat.deep") {
		t.Errorf("最终树里丢了 chat.deep，实际 span: %v", spanNamesIn(last.Root))
	}
}

// waitFor 轮询等待条件成立，超时则以 msg 失败。
// 用它替代裸 time.Sleep：断言的是「结果最终出现」，而不是「等了足够久」。
func waitFor(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal(msg)
}
