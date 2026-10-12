package observability

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	langfuse "github.com/cloudwego/eino-ext/callbacks/langfuse/v2"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"solvify-agent/pkg/config"
	"solvify-agent/pkg/traceid"
)

// 这一组测试守「eino 官方 Langfuse callback（v2）」这条三方链路的字段下发语义。
//
// 断言全部打在**真正被导出的 span** 上，而不是我们自己的中间变量：
// 「配了但没生效」这类缺陷只有在对端视角才看得见。导出用官方
// `Config.SpanExporter`（其注释原文："intended for custom transports and tests"）
// 挂一个内存捕获器 —— 走真实 OTLP 的话收到的是 protobuf 二进制，断言前还得先解 protobuf。
//
// ⚠️ 对端必须与生产同参：本文件的 handler 一律由 `newLangfuseConfig`（生产同一个映射函数）
// 造出来，只替换导出器与批量参数。历史上测试里手抄过一份 Config，少了 Tags，于是
// 「静态标签被下发两遍」这个真缺陷在测试里完全看不见。

// lfSpanCapture 是给官方 Config.SpanExporter 用的探针：把导出的 span 原样留下来。
type lfSpanCapture struct {
	mu    sync.Mutex
	spans []sdktrace.ReadOnlySpan
}

func (c *lfSpanCapture) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.spans = append(c.spans, spans...)
	return nil
}

func (c *lfSpanCapture) Shutdown(context.Context) error { return nil }

func (c *lfSpanCapture) all() []sdktrace.ReadOnlySpan {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]sdktrace.ReadOnlySpan(nil), c.spans...)
}

// traceRoot 返回 trace 级 root observation —— 即「没有父 span 的那个」。
//
// 为什么不用「名字最长的那个」之类的启发式：StartTrace 第一步就是
// `trace.ContextWithSpanContext(ctx, trace.SpanContext{})`，刻意把父清空，
// 所以「父无效」是 root 的确定性特征。
func (c *lfSpanCapture) traceRoot(t *testing.T) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, s := range c.all() {
		if !s.Parent().IsValid() {
			return s
		}
	}
	t.Fatalf("没有捕获到 root span（共 %d 个 span）", len(c.all()))
	return nil
}

// newLFTestHandler 造一个「导出到内存捕获器」的官方 v2 handler。
//
// mutate 用来按需覆盖 Config（例如注入 MaskFunc）。默认值只影响「等多久」，
// 不影响正确性 —— 断言前一律显式 Flush。
func newLFTestHandler(t *testing.T, cfg config.ObservabilityConfig, cap *lfSpanCapture, mutate ...func(*langfuse.Config)) *langfuse.CallbackHandler {
	t.Helper()
	lcfg := newLangfuseConfig(cfg) // 生产同一份映射；下面只改「往哪导、攒多久」
	lcfg.SpanExporter = cap
	lcfg.MaxExportBatchSize = 1
	lcfg.BatchTimeout = 5 * time.Millisecond
	lcfg.Timeout = 2 * time.Second
	for _, m := range mutate {
		m(lcfg)
	}
	h, err := langfuse.NewHandler(context.Background(), lcfg)
	if err != nil {
		t.Fatalf("langfuse.NewHandler: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = h.Shutdown(ctx)
	})
	return h
}

// newLFTestTracer 造一个「已经拿着官方 handler」的 Tracer。
//
// 直接构造结构体、不走 NewTracer：NewTracer 会在「未配凭据」时返回 nil，
// 而这里要测的是「拿到了 Tracer 之后的行为」。Also 它会注册 eino 全局 handler，
// 在测试里没必要污染全局。
func newLFTestTracer(cfg config.ObservabilityConfig, h *langfuse.CallbackHandler) *Tracer {
	return &Tracer{handler: h, cfg: cfg}
}

// newLFTestStack 一次性造齐「捕获器 + 官方 handler + Tracer」。
func newLFTestStack(t *testing.T, cfg config.ObservabilityConfig, mutate ...func(*langfuse.Config)) (*lfSpanCapture, *langfuse.CallbackHandler, *Tracer) {
	t.Helper()
	cap := &lfSpanCapture{}
	h := newLFTestHandler(t, cfg, cap, mutate...)
	return cap, h, newLFTestTracer(cfg, h)
}

// flushLF 是「等官方把队列里的 span 真的导出完」的同步点。
func flushLF(t *testing.T, h *langfuse.CallbackHandler) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := h.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
}

// lfTestConfig 把一份可观测性配置调成「Langfuse 已启用」，且各字段与生产配置同形。
//
// host 用 `.invalid`（RFC 2606 保留域）：本组测试全部走 SpanExporter，
// 这个地址永远不会被拨号，写一个真实域名反而容易让人误以为在发真实请求。
func lfTestConfig() config.ObservabilityConfig {
	return config.ObservabilityConfig{
		OTelServiceName:               "solvify-agent-test",
		PIIMaskSecret:                 true,
		LangfuseHost:                  "https://langfuse.invalid",
		LangfusePublicKey:             "pk-lf-test",
		LangfuseSecretKey:             "sk-lf-test",
		LangfuseMaxExportBatchSize:    512,
		LangfuseBatchTimeoutMs:        5000,
		LangfuseTimeoutMs:             10000,
		LangfuseSampleRate:            1.0,
		LangfuseMaxQueueSize:          2048,
		LangfuseMaxSpanAttributeBytes: 4_000_000,
	}
}

// runLFRequest 跑一遍生产里的真实路径，返回平台侧看到的 root observation 与 trace id：
//
//	Tracer.Start → StartTrace：建 root span，把 traceRun 放进 ctx
//	OnStart/OnEnd → 挂一棵 eino 子 span 在 root 下（这里用 retriever 代表任意组件）
//	Tracer.End   → EndTrace：补 trace 级 output，并请求结束（无活跃子 span ⇒ 立即结束）
//	Flush        → 等导出器收完
func runLFRequest(
	t *testing.T,
	tr *Tracer,
	h *langfuse.CallbackHandler,
	cap *lfSpanCapture,
	attrs TraceRootAttrs,
	answer string,
) (sdktrace.ReadOnlySpan, string) {
	t.Helper()

	ctx := tr.Start(context.Background(), attrs)
	id := traceid.FromContext(ctx)
	if id == "" {
		t.Fatal("Tracer.Start 没有把 trace id 写进 ctx")
	}

	info := &callbacks.RunInfo{Name: "retriever.test", Component: components.ComponentOfRetriever}
	ctx = h.OnStart(ctx, info, map[string]any{"query": attrs.Input})
	h.OnEnd(ctx, info, map[string]any{"docs": 1})

	tr.End(ctx, answer)
	flushLF(t, h)

	return cap.traceRoot(t), id
}

// lfAttr 取字符串属性；不存在时 ok=false（用来区分「没下发」与「下发成空串」）。
func lfAttr(s sdktrace.ReadOnlySpan, key string) (string, bool) {
	for _, kv := range s.Attributes() {
		if string(kv.Key) == key {
			return kv.Value.AsString(), true
		}
	}
	return "", false
}

func lfAttrMust(t *testing.T, s sdktrace.ReadOnlySpan, key string) string {
	t.Helper()
	v, ok := lfAttr(s, key)
	if !ok {
		t.Fatalf("span 上缺少属性 %s（实际属性: %v）", key, lfAttrKeys(s))
	}
	return v
}

func lfAttrKeys(s sdktrace.ReadOnlySpan) []string {
	out := make([]string, 0, len(s.Attributes()))
	for _, kv := range s.Attributes() {
		out = append(out, string(kv.Key))
	}
	return out
}

func lfAttrSlice(s sdktrace.ReadOnlySpan, key string) []string {
	for _, kv := range s.Attributes() {
		if string(kv.Key) == key {
			return kv.Value.AsStringSlice()
		}
	}
	return nil
}

// TestLangfuseTraceOptionsReachPlatform 是本组的主测试：把一条问答链路走一遍
// 「Tracer.Start 下发选项 → 官方 handler 建 trace → 导出到平台」，断言平台侧
// 真实看到的那条 root observation 字段齐全。
//
// 回归价值：这些字段里没有任何一个能靠官方默认值兜住 —— release 不传就是空、
// tags 静态那批只由 Config.Tags 给一次（opts 只补本请求的检索模式，两边都给就重复）、
// trace id 不在「callback 自建 provider」的前提下就钉不住。任何一处漏下发，本测试都会红。
func TestLangfuseTraceOptionsReachPlatform(t *testing.T) {
	cfg := lfTestConfig()
	cfg.LangfuseRelease = "v9.9.9"
	cfg.LangfuseTags = []string{"production"}
	if !cfg.LangfuseEnabled() {
		t.Fatal("凭据已填齐，LangfuseEnabled() 应为 true")
	}

	cap, h, tr := newLFTestStack(t, cfg)

	root, id := runLFRequest(t, tr, h, cap, TraceRootAttrs{
		UserID:     "u-1",
		SessionID:  "s-1",
		MessageID:  "m-1",
		RequestID:  "r-1",
		SearchMode: "deep",
		ModelID:    "deepseek-v4-flash",
		Input:      "今年差旅报销标准是多少？",
	}, "差旅报销标准是每天 200 元。")

	// 前提：trace id 必须是 32 位小写 hex，否则「拿它当平台 trace id」不成立。
	if len(id) != 32 || strings.ToLower(id) != id {
		t.Fatalf("trace id 不符合 Langfuse 的「32 位小写 hex」要求: %q", id)
	}

	// ① 平台侧 trace id 必须等于我们自己的 id。这正是 WithID 钉住的那件事，
	//    也是「刻意不给 Config.TracerProvider」的原因（见其 Config 注释）。
	if got := root.SpanContext().TraceID().String(); got != id {
		t.Errorf("平台侧 trace id 必须等于自研 traceID（否则日志/反馈跳不过去）: got=%v want=%v", got, id)
	}
	// ② root observation 的类型由 StartTrace 固定写成 span。
	if got := lfAttrMust(t, root, "langfuse.observation.type"); got != "span" {
		t.Errorf("root observation 的 type 应为 span: got=%q", got)
	}
	// ③ 以下字段都必须由 langfuseOptions 显式下发，不能指望官方默认值。
	if got, _ := lfAttr(root, "langfuse.trace.name"); got != "solvify-agent-test" {
		t.Errorf("trace name 没下发: got=%q", got)
	}
	if got, _ := lfAttr(root, "langfuse.release"); got != "v9.9.9" {
		t.Errorf("release 没下发 ⇒ langfuse_release 是死配置: got=%q", got)
	}
	if got, _ := lfAttr(root, "langfuse.user.id"); got != "u-1" {
		t.Errorf("user.id 没下发（平台会把它当未归组属性）: got=%q", got)
	}
	if got, _ := lfAttr(root, "langfuse.session.id"); got != "s-1" {
		t.Errorf("session.id 没下发: got=%q", got)
	}
	tags := lfAttrSlice(root, "langfuse.trace.tags")
	// 用精确序列而不是「包含」：包含判断对「重复」零分辨力（多一个同样的绿一样过）。
	// 静态标签只由 Config.Tags 给一次，opts 只补本请求的检索模式，两边都给就会 [a a b]。
	if strings.Join(tags, ",") != "production,deep" {
		t.Errorf("tags 应是「静态标签 + 本请求检索模式」各一次，实际: %v（len=%d）", tags, len(tags))
	}
	// ④ metadata 只放 ID 类字段，key 前缀是 langfuse.trace.metadata.<key>。
	for k, want := range map[string]string{
		"langfuse.trace.metadata.message_id": "m-1",
		"langfuse.trace.metadata.request_id": "r-1",
		"langfuse.trace.metadata.model_id":   "deepseek-v4-flash",
	} {
		if got, _ := lfAttr(root, k); got != want {
			t.Errorf("%s: got=%q want=%q", k, got, want)
		}
	}
	// ⑤ trace 级输入 = 用户提问原文（由 StartTrace 写 langfuse.observation.input）。
	if got, _ := lfAttr(root, "langfuse.observation.input"); got != "今年差旅报销标准是多少？" {
		t.Errorf("trace 级 input 没下发 ⇒ langfuse.WithInput 漏了: got=%q", got)
	}

	// ⑥ 顺带钉住「子 span 真的挂在 root 下」。挂错父（例如 StartTrace 没生效、
	//    子 span 自己成了 root）时，这里会立刻红 —— 那正是 trace id 对不上的成因。
	spans := cap.all()
	if len(spans) != 2 {
		t.Fatalf("应导出 2 个 span（root + retriever 子 span），实际 %d 个", len(spans))
	}
	var child sdktrace.ReadOnlySpan
	for _, s := range spans {
		if s.Parent().IsValid() {
			child = s
		}
	}
	if child == nil {
		t.Fatal("没有找到挂在 root 下的子 span")
	}
	if child.Parent().SpanID() != root.SpanContext().SpanID() {
		t.Errorf("子 span 的父不是 root span: parent=%v root=%v",
			child.Parent().SpanID(), root.SpanContext().SpanID())
	}
}

// TestTracerEndReachesPlatform 守 trace 级 output 的补写链路。
//
// 官方 v2 没有 WithOutput 之类的 trace 选项 —— output 只能在答复产生【之后】用
// EndTrace 补。EndTrace 会把 output 写进 root span 的 langfuse.observation.output，
// 再结束 root span；所以这条断言同时也证明了「root span 是被 Tracer.End 结束并导出的」。
func TestTracerEndReachesPlatform(t *testing.T) {
	cap, h, tr := newLFTestStack(t, lfTestConfig())

	root, _ := runLFRequest(t, tr, h, cap,
		TraceRootAttrs{UserID: "u-1", SessionID: "s-1"},
		"差旅报销标准是每天 200 元。")

	if got, _ := lfAttr(root, "langfuse.observation.output"); got != "差旅报销标准是每天 200 元。" {
		t.Errorf("trace 级 output 没送到平台 ⇒ EndTrace 那条链路断了: got=%q", got)
	}
}

// TestNilTracerIsSafe 守「未接三方时业务零影响」。
//
// 本地开发不配凭据 ⇒ NewTracer 返回 nil，而调用点（chatService）不做分支，
// 所以四个方法都必须对 nil 接收者安全，且不能 panic。
func TestNilTracerIsSafe(t *testing.T) {
	var tr *Tracer
	ctx := context.Background()

	got := tr.Start(ctx, TraceRootAttrs{UserID: "u-1"})
	if got != ctx {
		t.Error("nil Tracer 的 Start 必须原样返回 ctx")
	}
	tr.End(ctx, "有内容但没人接") // 不得 panic
	if err := tr.Close(ctx); err != nil {
		t.Errorf("nil Tracer 的 Close 应返回 nil: %v", err)
	}
}

// TestNewTracerDisabledReturnsNil 守「判据唯一」。
//
// 「是否启用」只在 NewTracer 一处判定：判据不成立就返回 nil Tracer，
// 调用点靠 nil 接收者空转。若有人绕过这里、在别处再判一次
// （「handler 非 nil」之类的第二个来源），就会出现「handler 没注册、
// 但每条请求都在开 trace」的错位。
func TestNewTracerDisabledReturnsNil(t *testing.T) {
	cfg := lfTestConfig()
	cfg.LangfuseHost = "" // 抽掉一项凭据
	if cfg.LangfuseEnabled() {
		t.Fatal("测试前提：缺凭据不应视为已启用")
	}

	tr, err := NewTracer(context.Background(), cfg)
	if err != nil {
		t.Fatalf("未配置凭据不该返回 error（那是本地开发的正常状态）: %v", err)
	}
	if tr != nil {
		t.Error("未配置凭据必须返回 nil Tracer")
	}
}

// TestTracerEndSkipsEmptyOutput 守「答复为空时不得结束根 span」。
//
// 空答复是错误 / 中断路径的常态，那里不该产出「outout 为空」的 trace 收尾。
// 断言用的是「导出器里是空的」—— 未结束的 span 永远不会进 exporter
// （SpanProcessor 只在 span End 时被调用），所以它正好等价于「EndTrace 没被调用」，
// 比断言内部计数更硬。
func TestTracerEndSkipsEmptyOutput(t *testing.T) {
	cap, h, tr := newLFTestStack(t, lfTestConfig())

	ctx := tr.Start(context.Background(), TraceRootAttrs{UserID: "u-1", SessionID: "s-1"})
	tr.End(ctx, "")

	// ⚠️ 断言前必须 Flush，否则这条守卫会失效：导出是批量的异步动作，
	// 不 Flush 时「根 span 已被错误地结束」也还没来得及进导出器，注入缺陷后会假绿。
	// Flush 只导出**已结束**的 span，所以它同时把两个方向都变成确定性的：
	//   没结束 EndTrace → 导出器为空（正确）；结束了 → 一定能看到（缺陷）。
	flushLF(t, h)

	if n := len(cap.all()); n != 0 {
		t.Errorf("答复为空时不该结束根 span，但导出器已收到 %d 个 span", n)
	}
}

// TestMaskFuncMasksTraceInputAndMetadata 钉住「MaskFunc 覆盖到哪些字段」这条事实。
//
// 这不是形式主义：项目里「metadata 能不能放内容类字段」的整套判断都建立在它上面。
// v1 只覆盖 input / output；v2 连 trace metadata 也逐值过了一遍 prepare
// （trace.go:231），trace 级 input 同样过（trace.go:114）。
// 上游哪天改掉覆盖面，这条会红，提醒我们重新评估 metadata 的取舍。
func TestMaskFuncMasksTraceInputAndMetadata(t *testing.T) {
	cap, h, tr := newLFTestStack(t, lfTestConfig())

	root, _ := runLFRequest(t, tr, h, cap, TraceRootAttrs{
		UserID:    "u-1",
		MessageID: "m-alice@example.com", // 走 metadata 通道
		Input:     "请联系 alice@example.com 确认差旅标准",
	}, "已确认。")

	in := lfAttrMust(t, root, "langfuse.observation.input")
	if strings.Contains(in, "alice") {
		t.Errorf("trace 级 input 未脱敏: %q", in)
	}
	meta := lfAttrMust(t, root, "langfuse.trace.metadata.message_id")
	if strings.Contains(meta, "alice") {
		t.Errorf("trace metadata 未脱敏: %q", meta)
	}
	// 反例对照：user.id 不经过 prepare，不该被 masker 动过 ——
	// 证明上面两条「已脱敏」不是「什么都没发」造成的假绿。
	if got, _ := lfAttr(root, "langfuse.user.id"); got != "u-1" {
		t.Errorf("user.id 不该被 MaskFunc 影响: got=%q", got)
	}
}

// TestMaskPIIDoesNotTruncate 守 MaskPII 的职责边界：只脱敏，不截断。
//
// 官方 Config.MaskFunc 的职责只有「脱敏」，长度由官方自己的
// MaxAttributeValueLength / MaxSpanAttributeBytes 管。
// 若 MaskPII 顺手做了截断（历史版本曾误用带 200 字截断的 SanitizeString），
// 平台上的 prompt / 回复会被悄悄砍掉 —— 那三方平台就白接了，而且不会有任何报错。
func TestMaskPIIDoesNotTruncate(t *testing.T) {
	long := strings.Repeat("张", 1500) + " alice@example.com " + strings.Repeat("李", 1500)

	masked := MaskPII(long, true)
	if strings.Contains(masked, "alice") {
		t.Error("MaskPII 没有对邮箱本地部分脱敏")
	}
	// 邮箱打码会让长度微增（`al***`），所以判据是「不短于原文」而不是「等于原文」。
	if n, before := len([]rune(masked)), len([]rune(long)); n < before {
		t.Errorf("MaskPII 把内容截短了：%d → %d rune（MaskFunc 不应负责截断）", before, n)
	}

	// 对照组：手机号 / 密钥形态也必须打码（这几条正则漏一个就是数据泄露）。
	if out := MaskPII("联系 13812345678", true); strings.Contains(out, "13812345678") {
		t.Errorf("手机号未脱敏: %q", out)
	}
	if out := MaskPII("api_key=abcdefghijklmn", true); strings.Contains(out, "abcdefghijklmn") {
		t.Errorf("api_key 值未脱敏: %q", out)
	}
	// maskSecret=false 时只打邮箱/手机号，不动密钥形态 —— 证明那个开关真的在起作用。
	if out := MaskPII("api_key=abcdefghijklmn", false); !strings.Contains(out, "abcdefghijklmn") {
		t.Errorf("maskSecret=false 时不该动密钥形态: %q", out)
	}
}

// TestMaskPIIHeaderSecretGap 记下 MaskPII 在「带协议词的 Authorization 头」上的漏网。
//
// ⚠️ 这是一条【现状快照】，不是「期望行为」：secretHeaderRe 的取值部分是
// `[^\s,;"']+`（遇空白即止），而 `Authorization: Bearer <token>` 的空格在协议词之前，
// 于是正则只吃到 "Bearer"，maskHeaderSecret 把 "Bearer" 打成 `***`，
// **真正的 token 原样留在后面**。本用例显式钉住这个现状，是为了让「测试全绿」
// 不等于「这条路已经安全」。修法（把取值部分改成允许一个协议词、或直接认 `Bearer <token>`）
// 涉及正则语义变更，属独立决定；修完这里必须一起翻成「token 必须消失」。
func TestMaskPIIHeaderSecretGap(t *testing.T) {
	out := MaskPII("Authorization: Bearer abcdefghijklmn", true)
	if !strings.Contains(out, "abcdefghijklmn") {
		t.Errorf("该漏网已被修复（token 已消失: %q）—— 请把本用例改成「断言 token 必须消失」", out)
		return
	}
	if !strings.Contains(out, "***") {
		t.Errorf("至少协议词应被打码，实际: %q", out)
	}
}

// TestLangfuseEnabledRequiresAllThreeCredentials 钉住判据的口径：
// 三项凭据缺任意一项都不算启用。这条判据只被 NewTracer 使用 —— 改口径即改接线。
func TestLangfuseEnabledRequiresAllThreeCredentials(t *testing.T) {
	full := config.ObservabilityConfig{
		LangfuseHost:      "https://example.com",
		LangfusePublicKey: "pk",
		LangfuseSecretKey: "sk",
	}
	if !full.LangfuseEnabled() {
		t.Fatal("三项齐全时应为已启用")
	}
	for name, c := range map[string]config.ObservabilityConfig{
		"缺 host":   {LangfusePublicKey: "pk", LangfuseSecretKey: "sk"},
		"缺 public": {LangfuseHost: "https://example.com", LangfuseSecretKey: "sk"},
		"缺 secret": {LangfuseHost: "https://example.com", LangfusePublicKey: "pk"},
		"三项皆空":     {},
		"只有 host":  {LangfuseHost: "https://example.com"},
	} {
		if c.LangfuseEnabled() {
			t.Errorf("%s 时不应视为已启用", name)
		}
	}
}
