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
)

// 这一组测试守「官方 eino → Langfuse callback（v2）」的字段下发语义。
//
// 与 v1 版本相比有三点根本差异，所以本文件是整体重写、不是改几行：
//
//  1. 传输方式变了。v1 是 `POST /api/public/ingestion`（JSON 事件批），
//     v2 换成标准 **OTLP/HTTP**（protobuf + gzip → `/api/public/otel/v1/traces`）。
//     于是「起个假 HTTP 端点、断言请求体里的 JSON」这条路不再可行 ——
//     收到的会是一坨 protobuf 二进制，断言前还得先解 protobuf。
//     改用官方 `Config.SpanExporter`（其注释原文："intended for custom
//     transports and tests"）挂一个内存捕获器，断言**导出器收到的 span 属性**。
//     测的仍然是「官方 callback 到底把哪些属性写到了 span 上」，只是换掉了最后一跳。
//
//  2. 生命周期变了。v1 用 SetTrace / UpdateTraceOutput，v2 换成 StartTrace / EndTrace：
//     trace 由 StartTrace **立刻**建出 root span，并把 traceRun 一起放进 ctx。
//     所以本文件里 `WithTraceRoot` 一被调用，平台侧那条 trace 就已经存在了；
//     `SetTraceOutput` 则是「补 output + 请求结束」这一个动作。
//
//  3. 属性名是 Langfuse 的 OTEL 约定：trace 级前缀 `langfuse.trace.*`，
//     用户 / 会话是 `langfuse.user.id` / `langfuse.session.id`，
//     observation 级是 `langfuse.observation.*`。下面所有断言直接引这些 key。
//
// 断言刻意打在**真正被导出的 span** 上，而不是断言我们自己的中间变量：
// 「配了但没生效」这类缺陷只有在对端视角才看得见。

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
func newLFTestHandler(t *testing.T, cap *lfSpanCapture, mutate ...func(*langfuse.Config)) *langfuse.CallbackHandler {
	t.Helper()
	cfg := &langfuse.Config{
		ServiceName:        "solvify-agent-test",
		SpanExporter:       cap,
		MaxExportBatchSize: 1,
		BatchTimeout:       5 * time.Millisecond,
		Timeout:            2 * time.Second,
	}
	for _, m := range mutate {
		m(cfg)
	}
	h, err := langfuse.NewHandler(context.Background(), cfg)
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

// newLFTestRecorder 造一个挂了官方 callback 的 recorder，并在测试结束时关闭。
func newLFTestRecorder(t *testing.T, cfg config.ObservabilityConfig, h *langfuse.CallbackHandler) Recorder {
	t.Helper()
	rec := NewRecorder(cfg, WithLangfuseCallback(h))
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = rec.Shutdown(ctx)
	})
	return rec
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

// lfTestConfig 把一份可观测性配置调成「Langfuse 已启用」。
//
// host 用 `.invalid`（RFC 2606 保留域）：本组测试全部走 SpanExporter，
// 这个地址永远不会被拨号，写一个真实域名反而容易让人误以为在发真实请求。
func lfTestConfig() config.ObservabilityConfig {
	cfg := testObsConfig()
	cfg.LangfuseHost = "https://langfuse.invalid"
	cfg.LangfusePublicKey = "pk-lf-test"
	cfg.LangfuseSecretKey = "sk-lf-test"
	return cfg
}

// runLFRequest 跑一遍生产里的真实路径，返回平台侧看到的 root observation 与自研 traceID：
//
//	WithTraceRoot   → StartTrace：建 root span，把 traceRun 放进 ctx
//	OnStart/OnEnd   → 挂一棵 eino 子 span 在 root 下（这里用 retriever 代表任意组件）
//	SetTraceOutput  → EndTrace：补 trace 级 output，并请求结束（无活跃子 span ⇒ 立即结束）
//	Flush           → 等导出器收完
func runLFRequest(
	t *testing.T,
	cfg config.ObservabilityConfig,
	cap *lfSpanCapture,
	h *langfuse.CallbackHandler,
	attrs TraceRootAttrs,
	answer string,
) (sdktrace.ReadOnlySpan, string) {
	t.Helper()
	rec := newLFTestRecorder(t, cfg, h)

	ctx := rec.WithTraceRoot(context.Background(), attrs)
	traceID := TraceIDFromContext(ctx)
	if traceID == "" {
		t.Fatal("WithTraceRoot 没有把自研 traceID 写进 ctx")
	}

	info := &callbacks.RunInfo{Name: "retriever.test", Component: components.ComponentOfRetriever}
	ctx = h.OnStart(ctx, info, map[string]any{"query": attrs.Input})
	h.OnEnd(ctx, info, map[string]any{"docs": 1})

	rec.SetTraceOutput(ctx, answer)
	flushLF(t, h)

	return cap.traceRoot(t), traceID
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

func lfHas(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// TestLangfuseTraceOptionsReachPlatform 是本组的主测试：把一条问答链路走一遍
// 「WithTraceRoot 下发选项 → 官方 handler 建 trace → 导出到平台」，断言平台侧
// 真实看到的那条 root observation 字段齐全。
//
// 回归价值：这些字段里没有任何一个能靠官方默认值兜住 —— release 不传就是空、
// tags 不传就只有 Config 里那批（少了本请求的检索模式）、trace id 不在
// 「callback 自建 provider」的前提下就钉不住。任何一处漏下发，本测试都会红。
func TestLangfuseTraceOptionsReachPlatform(t *testing.T) {
	cap := &lfSpanCapture{}
	h := newLFTestHandler(t, cap)

	cfg := lfTestConfig()
	cfg.OTelServiceName = "solvify-agent-test"
	cfg.LangfuseRelease = "v9.9.9"
	cfg.LangfuseTags = []string{"production"}
	if !cfg.LangfuseEnabled() {
		t.Fatal("凭据已填齐，LangfuseEnabled() 应为 true")
	}

	root, selfTraceID := runLFRequest(t, cfg, cap, h, TraceRootAttrs{
		UserID:     "u-1",
		SessionID:  "s-1",
		MessageID:  "m-1",
		RequestID:  "r-1",
		SearchMode: "deep",
		ModelID:    "deepseek-v4-flash",
		Input:      "今年差旅报销标准是多少？",
	}, "差旅报销标准是每天 200 元。")

	// 前提：自研 id 必须是 32 位小写 hex，否则「拿它当平台 trace id」不成立。
	if len(selfTraceID) != 32 || strings.ToLower(selfTraceID) != selfTraceID {
		t.Fatalf("自研 traceID 不符合 Langfuse 的「32 位小写 hex」要求: %q", selfTraceID)
	}

	// ① 平台侧 trace id 必须等于自研 traceID。这正是 WithID 钉住的那件事，
	//    也是「刻意不给 Config.TracerProvider」的原因（见其 Config 注释）。
	if got := root.SpanContext().TraceID().String(); got != selfTraceID {
		t.Errorf("平台侧 trace id 必须等于自研 traceID（否则自有 UI 跳不过去）: got=%v want=%v",
			got, selfTraceID)
	}
	// ② root observation 的类型由 StartTrace 固定写成 span（trace.go:99）。
	if got := lfAttrMust(t, root, "langfuse.observation.type"); got != "span" {
		t.Errorf("root observation 的 type 应为 span: got=%q", got)
	}
	// ③ 以下字段都必须由 recorder 显式下发，不能指望官方默认值。
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
	if !lfHas(tags, "production") || !lfHas(tags, "deep") {
		t.Errorf("tags 应是「静态标签 + 本请求检索模式」的合并结果，实际: %v", tags)
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
	//    子 span 自己成了 root）时，这里会立刻红 —— 那正是双轨 traceID 对不上的成因。
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

// TestSetTraceOutputReachesPlatform 守 trace 级 output 的补写链路。
//
// v2 没有 WithOutput 之类的 trace 选项 —— output 只能在答复产生【之后】用
// handler.EndTrace 补。EndTrace 会把 output 写进 root span 的
// langfuse.observation.output，再结束 root span；所以这条断言同时也证明了
// 「root span 是被 SetTraceOutput 结束并导出的」。
func TestSetTraceOutputReachesPlatform(t *testing.T) {
	cap := &lfSpanCapture{}
	h := newLFTestHandler(t, cap)

	root, _ := runLFRequest(t, lfTestConfig(), cap, h,
		TraceRootAttrs{UserID: "u-1", SessionID: "s-1"},
		"差旅报销标准是每天 200 元。")

	if got, _ := lfAttr(root, "langfuse.observation.output"); got != "差旅报销标准是每天 200 元。" {
		t.Errorf("trace 级 output 没送到平台 ⇒ EndTrace 那条链路断了: got=%q", got)
	}
}

// TestSetTraceOutputSkipsWithoutCallbackOrContent 守两条「不该发」的边界。
//
// 两者都是「无事可做」，但都容易被写成照样发一次：
//   - 没接三方（callback 为 nil）：本地开发的默认状态，还不能 panic
//   - 答复为空：错误 / 中断路径的常态
func TestSetTraceOutputSkipsWithoutCallbackOrContent(t *testing.T) {
	t.Run("未接三方（callback 为 nil）时不得 panic", func(t *testing.T) {
		rec := NewRecorder(lfTestConfig()) // 刻意不注入 callback
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = rec.Shutdown(ctx)
		})

		ctx := rec.WithTraceRoot(context.Background(), TraceRootAttrs{UserID: "u-1", SessionID: "s-1"})
		// 这条必须不 panic：判空就在 defaultRecorder.SetTraceOutput 里。
		rec.SetTraceOutput(ctx, "有内容但没人接")
	})

	t.Run("答复为空时不得结束根 span", func(t *testing.T) {
		cap := &lfSpanCapture{}
		h := newLFTestHandler(t, cap)
		rec := newLFTestRecorder(t, lfTestConfig(), h)

		ctx := rec.WithTraceRoot(context.Background(), TraceRootAttrs{UserID: "u-1", SessionID: "s-1"})
		rec.SetTraceOutput(ctx, "")

		// ⚠️ 断言前必须 Flush，否则这条守卫会「失效」：导出是批量的异步动作，
		// 不 Flush 时「根 span 已被错误地结束」也还没来得及进导出器，注入缺陷后会假绿
		// （实测：去掉 output 判空的那份注入下，不 Flush 时本用例全绿）。
		// Flush 只导出**已结束**的 span，所以它同时把两个方向都变成确定性的：
		//   没结束 EndTrace → 导出器为空（正确）；结束了 → 一定能看到（缺陷）。
		flushLF(t, h)

		// 没结束的 span 永远不会进 exporter —— SpanProcessor 只在 span End 时被调用，
		// 所以「导出器还是空的」正好等价于「EndTrace 没被调用」，比断言内部计数更硬。
		if n := len(cap.all()); n != 0 {
			t.Errorf("答复为空时不该结束根 span，但导出器已收到 %d 个 span", n)
		}
	})
}

// TestLangfuseTraceNotStartedWhenDisabled 守「判据单一」。
//
// WithTraceRoot 用 config.LangfuseEnabled() 决定要不要 StartTrace。若有人把它改成
// 「callback 非 nil」之类的第二个来源，就会出现「handler 没注册、但每条请求都在
// 开 trace」的错位。这里把它变成可观测的：未启用时只应存在那个孤儿子 span，
// 没有 root —— 因为 StartTrace 根本没被调用。
func TestLangfuseTraceNotStartedWhenDisabled(t *testing.T) {
	cap := &lfSpanCapture{}
	h := newLFTestHandler(t, cap)

	cfg := testObsConfig() // 不带任何 langfuse 凭据
	if cfg.LangfuseEnabled() {
		t.Fatal("测试前提：默认配置不应视为已启用 Langfuse")
	}

	rec := newLFTestRecorder(t, cfg, h)
	ctx := rec.WithTraceRoot(context.Background(), TraceRootAttrs{UserID: "u-1", SessionID: "s-1"})

	info := &callbacks.RunInfo{Name: "retriever.test", Component: components.ComponentOfRetriever}
	ctx = h.OnStart(ctx, info, map[string]any{"query": "你好"})
	h.OnEnd(ctx, info, map[string]any{"docs": 1})
	flushLF(t, h)

	spans := cap.all()
	if len(spans) != 1 {
		t.Fatalf("未启用时不该有 root span，应只导出 1 个孤儿子 span，实际 %d 个", len(spans))
	}
	if spans[0].Parent().IsValid() {
		t.Error("未启用时不该出现挂在 Langfuse root 下的子 span（说明 StartTrace 被调了）")
	}

	// 顺带确认：选项组装本身不依赖启用状态（判据只在 WithTraceRoot 一处）。
	r, ok := rec.(*defaultRecorder)
	if !ok {
		t.Fatal("NewRecorder 应返回 *defaultRecorder")
	}
	if opts := r.langfuseTraceOptions("", TraceRootAttrs{UserID: "u-1"}); len(opts) == 0 {
		t.Error("langfuseTraceOptions 本身应能产出选项；「是否下发」由 WithTraceRoot 的判据决定")
	}
}

// TestMaskFuncMasksTraceInputAndMetadata 钉住「MaskFunc 覆盖到哪些字段」这条事实。
//
// 这不是形式主义：项目里「metadata 能不能放内容类字段」的整套判断都建立在它上面。
// v1 只覆盖 input / output；v2 连 trace metadata 也逐值过了一遍 prepare
// （trace.go:231），trace 级 input 同样过（trace.go:114）。
// 上游哪天改掉覆盖面，这条会红，提醒我们重新评估 metadata 的取舍。
func TestMaskFuncMasksTraceInputAndMetadata(t *testing.T) {
	cap := &lfSpanCapture{}
	sanitizer := NewPIISanitizer(200, true)
	h := newLFTestHandler(t, cap, func(c *langfuse.Config) {
		c.MaskFunc = sanitizer.MaskPII
	})

	root, _ := runLFRequest(t, lfTestConfig(), cap, h, TraceRootAttrs{
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

// TestMaskPIIDoesNotTruncate 守 MaskPII 与 SanitizeString 的语义差别。
//
// 官方 Config.MaskFunc 的职责只有「脱敏」，长度由官方自己的
// MaxAttributeValueLength / MaxSpanAttributeBytes 管。
// 若把 SanitizeString 直接当 MaskFunc 用，平台上的 prompt / 回复会被悄悄截到
// ContentMaxChars（默认 200 字）—— 那三方平台就白接了，而且不会有任何报错。
func TestMaskPIIDoesNotTruncate(t *testing.T) {
	s := NewPIISanitizer(200, true)
	long := strings.Repeat("张", 1500) + " alice@example.com " + strings.Repeat("李", 1500)

	masked := s.MaskPII(long)
	if strings.Contains(masked, "alice") {
		t.Error("MaskPII 没有对邮箱本地部分脱敏")
	}
	if n := len([]rune(masked)); n < 2900 {
		t.Errorf("MaskPII 把内容截短到 %d rune —— 官方 MaskFunc 不应负责截断", n)
	}

	// 对照组：同一个 sanitizer 的 SanitizeString 确实会截断（证明上面那条断言有意义，
	// 而不是「反正都不会截断」）。
	if n := len([]rune(s.SanitizeString(long))); n > 210 {
		t.Errorf("SanitizeString 应截断到约 200 rune，实际 %d", n)
	}
}

// TestLangfuseEnabledRequiresAllThreeCredentials 钉住判据的口径：
// 三项凭据缺任意一项都不算启用。这条判据同时被 app.initLangfuseHandler
// 与 recorder.WithTraceRoot 使用，改口径必须两处一致。
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
