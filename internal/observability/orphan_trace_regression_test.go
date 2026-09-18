package observability

import (
	"context"
	"fmt"
	"sort"
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

// findSpanNamed 深度查找第一个同名 span，找不到返回 nil。
func findSpanNamed(root *Span, name string) *Span {
	if root == nil {
		return nil
	}
	if root.Name == name {
		return root
	}
	for _, c := range root.Children {
		if got := findSpanNamed(c, name); got != nil {
			return got
		}
	}
	return nil
}

func hasSpanNamed(root *Span, name string) bool {
	return findSpanNamed(root, name) != nil
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
//
// 这里必须用「无业务归属」的 trace：按 SampleRequest.Required 的语义，挂了 session / message
// 的 chat trace 一律落库，那条路径上根本走不到采样率这一层（见
// TestChatTraceKeptRegardlessOfSamplingRate）。无归属的形态就是纯 HTTP 噪声 ——
// 登记根 http.request 自己结束，没有 rootAttrs 归属。
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

	ctx, httpSpan := rec.StartSpan(context.Background(), "http.request", ComponentHTTPServer, nil)
	baseCtx := DetachedTraceContext(ctx)
	rec.EndSpan(ctx, httpSpan, SpanStatusOK, nil, nil)

	time.Sleep(2 * traceRepublishDebounce)
	if ids := sink.distinctTraceIDs(); len(ids) != 0 {
		t.Fatalf("采样率为 0 的 HTTP 噪声不该落库，实际: %v", ids)
	}

	bgCtx, bgSpan := rec.StartSpan(baseCtx, "ctx.summarize", ComponentServiceContext, nil)
	rec.EndSpan(bgCtx, bgSpan, SpanStatusOK, nil, nil)

	time.Sleep(2 * traceRepublishDebounce)
	if ids := sink.distinctTraceIDs(); len(ids) != 0 {
		t.Errorf("重发不该翻转采样决定，实际: %v", ids)
	}
}

// TestChatTraceKeptRegardlessOfSamplingRate 钉住「有业务归属的 trace 必留」这条规则。
//
// 为什么必须成立：chat 场景下 traceID 会先写进助手消息的 metadata.trace_id 返回给前端，
// 采样丢弃等于对外承诺了一个在 chat_traces 里查不到详情的悬空 ID（前端点「追踪详情」查空）。
// 所以采样率被拉到 0 时，chat trace 依然要落库。
//
// 回归价值：把 flushTraceState 传给采样器的 Required 去掉，本测试立刻变红。
func TestChatTraceKeptRegardlessOfSamplingRate(t *testing.T) {
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
	selfTraceID := rec.FlushTrace(ctx, "u-1", "s-1", "m-1")

	if len(sink.tracesFor(selfTraceID)) == 0 {
		t.Fatal("有业务归属的 chat trace 必须落库，实际一条都没有（前端拿到的 trace_id 会悬空）")
	}

	// 迟到的后台 span 再触发一次重发：仍然只有这一行，且归属与迟到子树都在
	bgCtx, bgSpan := rec.StartSpan(baseCtx, "ctx.summarize", ComponentServiceContext, nil)
	rec.EndSpan(bgCtx, bgSpan, SpanStatusOK, nil, nil)
	time.Sleep(2 * traceRepublishDebounce)

	if ids := sink.distinctTraceIDs(); len(ids) != 1 {
		t.Errorf("迟到的后台 span 不该裂出第二行，实际: %v", ids)
	}
	all := sink.tracesFor(selfTraceID)
	last := all[len(all)-1]
	if !last.Sampled {
		t.Error("落库的 chat trace 应当标记 Sampled=true（「是否落库」与「采样决定」必须一致）")
	}
	if last.UserID != "u-1" || last.SessionID != "s-1" {
		t.Errorf("归属不该丢: user=%q session=%q", last.UserID, last.SessionID)
	}
	if !hasSpanNamed(last.Root, "ctx.summarize") {
		t.Errorf("重发应把迟到 span 并回同一行，实际 span: %v", spanNamesIn(last.Root))
	}
}

// TestRepublishKeepsOTelExported 钉住「双轨对齐结果首发即冻结」这条规则。
//
// 成因：重发发生在请求结束之后，ctx 是 Background，而 cloneSpan 刻意不搬运 otelSpan，
// 于是 oTelExportedFor 现算必然得到 false；落库又是 upsert 整行覆盖，
// 结果同一 trace 在运行中读到 otel_exported=true、事后读到 false ——
// 而 collector 日志证明这些 span 真的导出了。
//
// 回归价值：去掉 traceState.otelExported 的复用，最后两条断言立刻变红。
func TestRepublishKeepsOTelExported(t *testing.T) {
	installSDKTracer(t)
	// 没有真实 exporter 时 oTelExportedFor 直接短路成 false，这条路径就测不到了
	installOTelExportActive(t, true)

	sink := &capturingDBSink{}
	rec := NewRecorderWithDBSink(testObsConfig(), sink)
	t.Cleanup(func() {
		c, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = rec.Shutdown(c)
	})

	ctx, _, chatSpan := chatChain(t, rec)
	baseCtx := DetachedTraceContext(ctx)
	rec.EndSpan(ctx, chatSpan, SpanStatusOK, nil, nil)
	selfTraceID := rec.FlushTrace(ctx, "u-1", "s-1", "m-1")

	first := sink.tracesFor(selfTraceID)
	if len(first) == 0 {
		t.Fatal("首发没落库，无法验证重发")
	}
	if !first[0].OTelExported || first[0].OTelTraceID == "" {
		t.Fatalf("首发就该是「已导出且带回 OTel traceID」: exported=%v traceID=%q",
			first[0].OTelExported, first[0].OTelTraceID)
	}

	bgCtx, bgSpan := rec.StartSpan(baseCtx, "ctx.summarize", ComponentServiceContext, nil)
	rec.EndSpan(bgCtx, bgSpan, SpanStatusOK, nil, nil)
	time.Sleep(2 * traceRepublishDebounce)

	all := sink.tracesFor(selfTraceID)
	last := all[len(all)-1]
	if !last.OTelExported {
		t.Error("重发把 otel_exported 翻转成了 false（upsert 会覆盖首发写下的真值）")
	}
	if last.OTelTraceID != first[0].OTelTraceID {
		t.Errorf("重发丢了 otel_trace_id: 首发=%q 重发=%q", first[0].OTelTraceID, last.OTelTraceID)
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

// TestPublishedTreeIsSelfConsistent 钉住「树是父子关系与时间区间的唯一来源」。
//
// 现场（当时 6/6 条 trace 都有）：
//   - chat.request.children 里有 http.request，但 http.request.parent_id 为空 ——
//     按 parent_id 重建会散成两棵树；
//   - 时间上 http.request 先开始（gin 中间件建的），chat.request 后开始（WithTraceRoot 才拼出来），
//     于是出现「根比子短」；
//   - 更深一层：ChatModelGenerate 82ms / 子 eino.chat_model 4296ms（P1-3），
//     以及后置任务比 chat.quick.graph 晚 4.91s 结束（P1-4）。
//
// 回归价值：去掉 flushTraceState 里的 stampParentIDs / envelopeTreeIntervals，对应断言各自变红。
func TestPublishedTreeIsSelfConsistent(t *testing.T) {
	installSDKTracer(t)

	sink := &capturingDBSink{}
	rec := newDualTrackRecorder(t, sink)

	ctx, httpSpan, chatSpan := chatChain(t, rec)
	rec.EndSpan(ctx, chatSpan, SpanStatusOK, nil, nil)
	selfTraceID := rec.FlushTrace(ctx, "u-1", "s-1", "m-1")

	all := sink.tracesFor(selfTraceID)
	if len(all) == 0 {
		t.Fatal("没落库")
	}
	root := all[0].Root
	if root == nil {
		t.Fatal("落库 trace 的 Root 为 nil")
	}
	if root.ParentID != "" {
		t.Errorf("发布根不该有 parent_id，实际 %q", root.ParentID)
	}

	// 逐层断言两条：出现在谁的 children 里 → parent_id 指向谁；父的区间包住每一个子
	var walk func(n *Span)
	walk = func(n *Span) {
		for _, c := range n.Children {
			if c == nil {
				continue
			}
			if c.ParentID != n.SpanID {
				t.Errorf("span %q 挂在 %q 下，parent_id 却是 %q（按 parent_id 重建会散架）",
					c.Name, n.Name, c.ParentID)
			}
			if c.StartAt.Before(n.StartAt) || c.EndAt.After(n.EndAt) {
				t.Errorf("span %q 的区间 [%s, %s] 超出父 %q 的 [%s, %s]",
					c.Name,
					c.StartAt.Format("15:04:05.000"), c.EndAt.Format("15:04:05.000"),
					n.Name,
					n.StartAt.Format("15:04:05.000"), n.EndAt.Format("15:04:05.000"))
			}
			walk(c)
		}
	}
	walk(root)

	// http.request 是真实 HTTP 根，必须真的在树里（否则断言在空树上恒真）
	if !hasSpanNamed(root, "http.request") {
		t.Fatalf("树里没有 http.request，实际 span: %v", spanNamesIn(root))
	}
	if httpSpan.SpanID == "" {
		t.Error("HTTP span 没有 SpanID，无法验证父子关系")
	}
}

// TestPublishedDurationCoversStreamingChild 钉住 P1-3 的形状：
// 图节点先 End、流式子 span 后 End，父的落库耗时必须覆盖子。
//
// 现场：ChatModelGenerate 是 InvokableLambda，eino 在「返回流对象」时就 OnEnd，
// 真正的生成发生在流被消费阶段 —— 父 82ms / 子 eino.chat_model 4296ms（40~70 倍倒挂），
// 前端耗时占比图因此得出「检索花了 6 秒、生成只花 0.1 秒」这个完全相反的结论。
//
// 回归价值：去掉 envelopeTreeIntervals 的调用，本测试立刻变红。
func TestPublishedDurationCoversStreamingChild(t *testing.T) {
	installSDKTracer(t)

	sink := &capturingDBSink{}
	rec := newDualTrackRecorder(t, sink)

	ctx, _, chatSpan := chatChain(t, rec)
	nodeCtx, nodeSpan := rec.StartSpan(ctx, "ChatModelGenerate", ComponentAgentEngine, nil)
	rec.EndSpan(nodeCtx, nodeSpan, SpanStatusOK, nil, nil) // 父：返回流对象就结束了

	modelCtx, modelSpan := rec.StartSpan(nodeCtx, "eino.chat_model", ComponentLLMClient, nil)
	time.Sleep(30 * time.Millisecond) // 子：真正的生成还在继续
	rec.EndSpan(modelCtx, modelSpan, SpanStatusOK, nil, nil)

	rec.EndSpan(ctx, chatSpan, SpanStatusOK, nil, nil)
	selfTraceID := rec.FlushTrace(ctx, "u-1", "s-1", "m-1")

	all := sink.tracesFor(selfTraceID)
	if len(all) == 0 {
		t.Fatal("没落库")
	}
	node := findSpanNamed(all[0].Root, "ChatModelGenerate")
	model := findSpanNamed(all[0].Root, "eino.chat_model")
	if node == nil || model == nil {
		t.Fatalf("树里缺节点: %v", spanNamesIn(all[0].Root))
	}
	if node.DurationMs < model.DurationMs {
		t.Errorf("父 %q 耗时 %dms 小于子 %q 的 %dms（前端占比图会得出相反结论）",
			node.Name, node.DurationMs, model.Name, model.DurationMs)
	}
	if node.EndAt.Before(model.EndAt) {
		t.Errorf("父 %q 比子 %q 早结束: %s < %s",
			node.Name, model.Name,
			node.EndAt.Format("15:04:05.000"), model.EndAt.Format("15:04:05.000"))
	}
}

// TestEndSpanIsIdempotentInSpanTree 钉住「一个 span 在 span_tree 里只能是一个节点」。
//
// 现场（真库取证，深度模式那一轮）：落库的 span_tree 里同一个 span_id 会在**同一个父节点**下
// 出现 3 次 —— AfterToolCalls / CancelCheck / ToolNode / eino.lambda / eino.embedding 都是 ×3。
// 判据是「同一父节点下重复」，所以不是「同名不同 span」，而是同一个 *Span 指针被 append 了多次。
//
// 成因：EndSpan 里 `span.parent.Children = append(..., span)` 没有幂等守卫，而 EndSpan
// 「只会被调用一次」这个前提在本项目里并不成立 ——
//   - 业务侧是「显式 End + defer 兜底 End」双保险（chat_service_mode.go / chat_service_graph_quick.go /
//     context_service.go …）；
//   - eino 侧同一个 state.span 可能被 OnEnd / OnError / OnEndWithStreamOutput 多条路径命中。
//
// 复用同一棵树的后果不只是「前端详情页重复显示节点」：节点总数与 OTel 侧对不上，
// 任何「按 span 去重后统计」的分析都会把同一个节点算重。
//
// 回归价值：去掉 recorder.go 里的 containsSpan 守卫，本测试立刻变红。
func TestEndSpanIsIdempotentInSpanTree(t *testing.T) {
	installSDKTracer(t)

	sink := &capturingDBSink{}
	rec := newDualTrackRecorder(t, sink)

	ctx, _, chatSpan := chatChain(t, rec)

	// 模拟真实链路：同一个 span 被显式 End 一次，再被 defer 兜底 End 两次。
	childCtx, child := rec.StartSpan(ctx, "ToolNode", ComponentAgentEngine, nil)
	rec.EndSpan(childCtx, child, SpanStatusOK, nil, nil)
	rec.EndSpan(childCtx, child, SpanStatusOK, nil, nil)
	rec.EndSpan(childCtx, child, SpanStatusOK, nil, nil)

	rec.EndSpan(ctx, chatSpan, SpanStatusOK, nil, nil)
	selfTraceID := rec.FlushTrace(ctx, "u-1", "s-1", "m-1")

	all := sink.tracesFor(selfTraceID)
	if len(all) == 0 {
		t.Fatal("没落库")
	}
	root := all[0].Root

	// 判据一（与分析器 B 组一致）：整棵树里 span_id 必须唯一。
	ids := map[string]int{}
	var walk func(n *Span)
	walk = func(n *Span) {
		if n == nil {
			return
		}
		ids[n.SpanID]++
		for _, c := range n.Children {
			walk(c)
		}
	}
	walk(root)

	var dup []string
	for id, c := range ids {
		if c > 1 {
			dup = append(dup, fmt.Sprintf("%s×%d", id, c))
		}
	}
	if len(dup) > 0 {
		sort.Strings(dup)
		t.Errorf("span_tree 里同一个 span_id 出现多次（重复 EndSpan 被挂成多个节点）: %v\n整棵树的节点名: %v",
			dup, spanNamesIn(root))
	}

	// 判据二（更贴近现场）：同一父节点下不该出现同一个 child 指针。
	// 这条直接盯 append 本身，即使将来树的序列化方式变了也不会失效。
	if chatSpan != nil {
		seen := map[*Span]bool{}
		for _, c := range chatSpan.Children {
			if seen[c] {
				t.Errorf("chat.deep 的 children 里同一个 span 挂了多次：name=%q span_id=%s，children=%v",
					c.Name, c.SpanID, spanNamesIn(chatSpan))
			}
			seen[c] = true
		}
		if len(chatSpan.Children) != 1 {
			t.Errorf("chat.deep 的 children 期望 1 个（ToolNode），实际 %d 个: %v",
				len(chatSpan.Children), spanNamesIn(chatSpan))
		}
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
