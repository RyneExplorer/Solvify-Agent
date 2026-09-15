package observability

import (
	"context"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// 这一组测试覆盖「trace 上下文跨进程传递」（propagation）。
//
// 背景：在补齐这一层之前，项目只在进程内部有 trace —— 出站请求不带任何 trace 头，
// 入站请求带来的 traceparent 也被忽略，链路必然断在服务边界上：
// 三方平台看到的是「一堆互不相关的孤立 trace」，而不是「网关 → 本服务 → 模型 API」。
//
// 本文件把下面几条性质变成可证伪的断言：
//  1. 装上 W3C 传播器后，Inject 写出的 traceparent 格式合法，且指向当前 span；
//  2. 入站带 traceparent 时，http.request span 复用上游的 traceID、并且父 span 是
//     上游那个 span（且标记 remote）—— 这是「跨服务串成一条链路」的直接证据；
//  3. 入站不带 traceparent 时行为不变，HTTP 入口仍是根 span；
//  4. 出站 Transport 真的把 traceparent 发到了对端，对端看到的 traceID 与父 span 一致、
//     spanID 是新建的 client span（证明注入发生在 span.Start 之后，没有挂错父节点），
//     同时记录 SpanKindClient 的 span 与标准 HTTP 语义属性，且 url.full 不泄漏 query；
//  5. ParentBased 采样：上游明确标记不采样时本地也不记录，避免产出半截孤儿 trace。

const (
	// W3C TraceContext 规范示例里的取值，用它方便和规范文档对照。
	upstreamTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"
	upstreamSpanID  = "00f067aa0ba902b7"
)

// installGlobalPropagator 装上生产用的全局传播器。
//
// 为什么不像其它 helper 那样在 cleanup 里还原：OTel 的全局传播器是进程级单例，
// 而且「默认委托器 → 真实传播器」的委托只能发生一次（内部是 sync.Once），
// 没有反安装的途径 —— 试着把默认委托器塞回去只会触发一条 "no delegate configured"
// 的内部错误日志，全局值实际不变。反正生产里也是启动时装一次不再改，所以这里直接装，
// 与生产语义一致。
func installGlobalPropagator(t *testing.T) {
	t.Helper()
	InitPropagator()
}

// installGlobalSDKTracer 把 *OTel 全局* TracerProvider 换成带 SpanRecorder 的真实 SDK。
//
// 注意与同包 installSDKTracer 的区别：那个只替换包级 globalTracer（供自研 Recorder 用），
// 而 HTTPTransport 与传播器走的是 otel.Tracer(...) 这条全局路径，必须换全局 provider 才生效。
func installGlobalSDKTracer(t *testing.T, sampler sdktrace.Sampler) *tracetest.SpanRecorder {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSampler(sampler),
		// SpanRecorder 本身就是 SpanProcessor（OnStart/OnEnd 同步记录），
		// span.End 之后立即可在 Ended() 里读到，测试无需等待或轮询。
		sdktrace.WithSpanProcessor(rec),
	)
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(prev)
		_ = tp.Shutdown(context.Background())
	})
	return rec
}

// splitTraceparent 把 traceparent 拆成 [version, traceID, spanID, flags]，顺带校验结构。
func splitTraceparent(t *testing.T, value string) []string {
	t.Helper()
	if value == "" {
		t.Fatal("未注入 traceparent：全局传播器可能没装上 W3C TraceContext")
	}
	parts := strings.Split(value, "-")
	if len(parts) != 4 {
		t.Fatalf("traceparent 结构应为 4 段，实际 %q", value)
	}
	if len(parts[1]) != 32 {
		t.Fatalf("traceID 应为 32 位十六进制，实际 %q", parts[1])
	}
	if len(parts[2]) != 16 {
		t.Fatalf("spanID 应为 16 位十六进制，实际 %q", parts[2])
	}
	return parts
}

// spanAttrs 把 ReadOnlySpan 的属性摊平成 map，便于按语义约定键名断言。
func spanAttrs(s sdktrace.ReadOnlySpan) map[string]string {
	out := make(map[string]string, len(s.Attributes()))
	for _, kv := range s.Attributes() {
		out[string(kv.Key)] = kv.Value.Emit()
	}
	return out
}

// onlySpan 取 recorder 里唯一记录下来的 span；数量不为 1 直接失败。
// 用「先 End 再回读」而不是断言 Start 返回的 trace.Span：父 span 信息
// （Parent()）只有 SDK 的 ReadOnlySpan 才暴露，trace.Span 接口没有这个方法。
func onlySpan(t *testing.T, rec *tracetest.SpanRecorder) sdktrace.ReadOnlySpan {
	t.Helper()
	spans := rec.Ended()
	if len(spans) != 1 {
		t.Fatalf("期望记录到恰好 1 个 span，实际 %d 个", len(spans))
	}
	return spans[0]
}

// TestPropagatorInjectsW3CTraceparent 断言注入出的 traceparent 结构合法且指向当前 span。
//
// 回归价值：删掉 InitPropagator 里的 otel.SetTextMapPropagator，本测试立刻变红
// （全局默认传播器没有任何 delegate，Inject 是空操作）。
func TestPropagatorInjectsW3CTraceparent(t *testing.T) {
	installGlobalPropagator(t)
	installGlobalSDKTracer(t, sdktrace.AlwaysSample())

	ctx, span := otel.Tracer(tracerName).Start(context.Background(), "op")
	defer span.End()

	header := http.Header{}
	InjectContext(ctx, header)

	parts := splitTraceparent(t, header.Get("traceparent"))
	if parts[0] != "00" {
		t.Errorf("traceparent version 应为 00，实际 %q", parts[0])
	}
	sc := span.SpanContext()
	if parts[1] != sc.TraceID().String() {
		t.Errorf("traceparent 的 traceID 应为当前 span 的 %s，实际 %s", sc.TraceID(), parts[1])
	}
	if parts[2] != sc.SpanID().String() {
		t.Errorf("traceparent 的 spanID 应为当前 span 的 %s，实际 %s", sc.SpanID(), parts[2])
	}
	if parts[3] != "01" {
		t.Errorf("AlwaysSample 下采样标记应为 01，实际 %q", parts[3])
	}
	if got := header.Get("baggage"); got != "" {
		t.Errorf("空 baggage 不应产生 baggage 头，实际 %q", got)
	}
}

// TestExtractRemoteContextMakesInboundSpanChildOfUpstream 断言入站 span 复用上游 traceID 且以
// 上游 span 为父节点。这是「跨服务链路串成一条 trace」的直接证据。
//
// 回归价值：删掉中间件里的 ExtractRemoteContext 调用（或本函数退回不提取），
// http.request 会变成新的根 span，traceID 不再等于上游值，本测试立刻变红。
func TestExtractRemoteContextMakesInboundSpanChildOfUpstream(t *testing.T) {
	installGlobalPropagator(t)
	rec := installGlobalSDKTracer(t, sdktrace.AlwaysSample())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat/sessions/s1/messages", nil)
	req.Header.Set("traceparent", "00-"+upstreamTraceID+"-"+upstreamSpanID+"-01")

	ctx, info := ExtractRemoteContext(context.Background(), req.Header)
	if !info.Present {
		t.Fatal("请求头带合法 traceparent，却报告未命中远程父 span")
	}
	if info.TraceID != upstreamTraceID {
		t.Errorf("提取到的 traceID 应为 %s，实际 %s", upstreamTraceID, info.TraceID)
	}
	if info.SpanID != upstreamSpanID {
		t.Errorf("提取到的父 spanID 应为 %s，实际 %s", upstreamSpanID, info.SpanID)
	}
	if !info.Sampled {
		t.Error("flags=01 应解析为已采样")
	}

	_, span := otel.Tracer(tracerName).Start(ctx, "http.request", trace.WithSpanKind(trace.SpanKindServer))
	span.End()

	srvSpan := onlySpan(t, rec)
	sc := srvSpan.SpanContext()
	if got := sc.TraceID().String(); got != upstreamTraceID {
		t.Fatalf("入站 span 未复用上游 traceID：期望 %s，实际 %s（链路会在服务边界断成两条 trace）",
			upstreamTraceID, got)
	}
	parent := srvSpan.Parent()
	if parent.SpanID().String() != upstreamSpanID {
		t.Errorf("父 spanID 应为上游的 %s，实际 %s", upstreamSpanID, parent.SpanID())
	}
	if !parent.IsRemote() {
		t.Error("从请求头提取的父 span 必须标记为 remote，否则平台无法还原跨服务拓扑")
	}
	if sc.SpanID().String() == upstreamSpanID {
		t.Error("新建 span 的 spanID 不应等于上游父 span 的 spanID")
	}
}

// TestNoInboundTraceparentStartsNewRootTrace 断言没有上游 traceparent 时行为与改动前一致：
// HTTP 入口仍是根 span，不会凭空继承一个不存在的上游。
func TestNoInboundTraceparentStartsNewRootTrace(t *testing.T) {
	installGlobalPropagator(t)
	rec := installGlobalSDKTracer(t, sdktrace.AlwaysSample())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", nil)

	ctx, info := ExtractRemoteContext(context.Background(), req.Header)
	if info.Present {
		t.Fatal("请求头没有 traceparent，不应报告命中远程父 span")
	}

	_, span := otel.Tracer(tracerName).Start(ctx, "http.request", trace.WithSpanKind(trace.SpanKindServer))
	span.End()

	srvSpan := onlySpan(t, rec)
	if srvSpan.Parent().IsValid() {
		t.Errorf("无上游时应为根 span，实际父 span=%v", srvSpan.Parent())
	}
	if !srvSpan.SpanContext().IsValid() {
		t.Error("应生成合法的 SpanContext")
	}
}

// TestHTTPTransportInjectsTraceparentAndRecordsClientSpan 断言出站请求真的带上了 traceparent，
// 且新 span（而非父 span）的 spanID 被写进了请求头。
//
// 回归价值：把 InjectContext 挪到 tracer.Start 之前，注入出去的就是父 span 的 spanID，
// 下游会挂到错误的父节点上 —— 链路看似接上了其实接错了，本测试对这一点专门断言。
// 同时断言 url.full 已剥离 query：query 里常带 token / 签名参数，不能上报到三方平台。
func TestHTTPTransportInjectsTraceparentAndRecordsClientSpan(t *testing.T) {
	installGlobalPropagator(t)
	rec := installGlobalSDKTracer(t, sdktrace.AlwaysSample())

	seen := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get("traceparent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// query 里故意放一个「凭据」，用来验证它不会出现在上报的 url.full 里。
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		srv.URL+"/v1/chat/completions?api_key=SUPERSECRET", nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}

	parentCtx, parentSpan := otel.Tracer(tracerName).Start(context.Background(), "llm.chat")
	defer parentSpan.End()
	req = req.WithContext(parentCtx)

	client := &http.Client{Transport: HTTPTransport(nil)}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("出站请求失败: %v", err)
	}
	_ = resp.Body.Close()

	parts := splitTraceparent(t, <-seen)
	if parts[1] != parentSpan.SpanContext().TraceID().String() {
		t.Errorf("出站 traceparent 的 traceID 应等于父 span 的 %s，实际 %s",
			parentSpan.SpanContext().TraceID(), parts[1])
	}
	if parts[2] == parentSpan.SpanContext().SpanID().String() {
		t.Fatal("出站 traceparent 用的是父 span 的 spanID：注入发生在 span.Start 之前，下游会挂错父节点")
	}
	if parts[3] != "01" {
		t.Errorf("采样标记应继承父 span 的 01，实际 %q", parts[3])
	}

	var clientSpan sdktrace.ReadOnlySpan
	for _, s := range rec.Ended() {
		if s.SpanKind() == trace.SpanKindClient {
			clientSpan = s
			break
		}
	}
	if clientSpan == nil {
		t.Fatal("出站请求没有产生 SpanKindClient 的 span")
	}
	if clientSpan.Name() != http.MethodPost {
		t.Errorf("client span 名应为 HTTP 方法 %q，实际 %q", http.MethodPost, clientSpan.Name())
	}
	if got := clientSpan.SpanContext().TraceID().String(); got != parentSpan.SpanContext().TraceID().String() {
		t.Errorf("client span 应挂在父 span 同一条 trace 上，期望 %s 实际 %s",
			parentSpan.SpanContext().TraceID(), got)
	}

	attrs := spanAttrs(clientSpan)
	if got := attrs["http.request.method"]; got != http.MethodPost {
		t.Errorf("http.request.method 应为 %s，实际 %q", http.MethodPost, got)
	}
	host, port, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("解析测试服务地址失败: %v", err)
	}
	if got := attrs["server.address"]; got != host {
		t.Errorf("server.address 应为 %s，实际 %q", host, got)
	}
	if got := attrs["server.port"]; got != port {
		t.Errorf("server.port 应为 %s，实际 %q", port, got)
	}
	if got := attrs["http.response.status_code"]; got != "200" {
		t.Errorf("http.response.status_code 应为 200，实际 %q", got)
	}
	urlFull := attrs["url.full"]
	if !strings.HasPrefix(urlFull, "http://"+host+":"+port+"/v1/chat/completions") {
		t.Errorf("url.full 应保留 scheme/host/path，实际 %q", urlFull)
	}
	if strings.Contains(urlFull, "SUPERSECRET") {
		t.Errorf("url.full 泄漏了 query 里的凭据: %q", urlFull)
	}
}

// TestHTTPTransportForwardsInboundTraceparentWithoutLocalExporter 断言本服务不记录时，
// 仍把入站 traceparent 原样透传给下游。
//
// 场景：OTelExporter=noop（开发环境默认）时本地 TracerProvider 是 noop 的，
// tracer.Start 拿不到合法 SpanContext。此时若照旧注入，写出去的是一份空上下文，
// 等于把上游已经在维护的链路在本服务这里掐断 —— 下游再想串起来就没机会了。
func TestHTTPTransportForwardsInboundTraceparentWithoutLocalExporter(t *testing.T) {
	installGlobalPropagator(t)
	// 刻意不装全局 SDK TracerProvider，模拟导出的 noop 模式。

	seen := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen <- r.Header.Get("traceparent")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/x", nil)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	inbound := http.Header{}
	inbound.Set("traceparent", "00-"+upstreamTraceID+"-"+upstreamSpanID+"-01")
	reqCtx, info := ExtractRemoteContext(req.Context(), inbound)
	if !info.Present {
		t.Fatal("入站 traceparent 未被识别")
	}
	req = req.WithContext(reqCtx)

	client := &http.Client{Transport: HTTPTransport(nil)}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("出站请求失败: %v", err)
	}
	_ = resp.Body.Close()

	parts := splitTraceparent(t, <-seen)
	if parts[1] != upstreamTraceID {
		t.Errorf("本地不导出时仍应把上游 traceID %s 透传给下游，实际 %s", upstreamTraceID, parts[1])
	}
}

// TestParentBasedSamplerHonorsUnsampledUpstream 断言上游标记「不采样」时本地也不记录。
//
// 这是 samplerFor 必须包 ParentBased 的直接证据：裸 TraceIDRatioBased/AlwaysSample 会无视
// 上游决定、照样本地记录，于是三方平台上出现只有本服务半截 span 的孤儿 trace。
// 回归价值：把 samplerFor 里的 sdktrace.ParentBased 去掉，本测试立刻变红。
func TestParentBasedSamplerHonorsUnsampledUpstream(t *testing.T) {
	installGlobalPropagator(t)
	// 采样率刻意拉满（rate >= 1）：即使这样，也必须听上游的「不采样」决定。
	rec := installGlobalSDKTracer(t, samplerFor(1.0))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/chat", nil)
	req.Header.Set("traceparent", "00-"+upstreamTraceID+"-"+upstreamSpanID+"-00")

	ctx, info := ExtractRemoteContext(context.Background(), req.Header)
	if !info.Present {
		t.Fatal("请求头带合法 traceparent，却报告未命中远程父 span")
	}
	if info.Sampled {
		t.Error("flags=00 应解析为未采样")
	}

	_, span := otel.Tracer(tracerName).Start(ctx, "http.request", trace.WithSpanKind(trace.SpanKindServer))
	span.End()

	if n := len(rec.Ended()); n != 0 {
		t.Fatalf("上游标记不采样时本地不应产出 span，实际记录 %d 条（会产生半截孤儿 trace）", n)
	}
}

// TestSamplerForNeverSampleWithoutParent 守住另一侧：本地采样率为 0 且没有上游时，一条都不采。
func TestSamplerForNeverSampleWithoutParent(t *testing.T) {
	installGlobalPropagator(t)
	rec := installGlobalSDKTracer(t, samplerFor(0))

	_, span := otel.Tracer(tracerName).Start(context.Background(), "http.request")
	span.End()

	if n := len(rec.Ended()); n != 0 {
		t.Fatalf("本地采样率为 0 且无上游时不应产出 span，实际记录 %d 条", n)
	}
}

// TestSpanKindForMapsServerAndClient 守住 SpanKind 映射：三方平台靠它区分服务端入口与出站调用。
func TestSpanKindForMapsServerAndClient(t *testing.T) {
	cases := []struct {
		component Component
		want      trace.SpanKind
	}{
		{ComponentHTTPServer, trace.SpanKindServer},
		{ComponentLLMClient, trace.SpanKindClient},
		{ComponentServiceChat, trace.SpanKindInternal},
		{ComponentRepository, trace.SpanKindInternal},
	}
	for _, c := range cases {
		if got := spanKindFor(c.component); got != c.want {
			t.Errorf("spanKindFor(%s) = %v，期望 %v", c.component, got, c.want)
		}
	}
}

// TestExtractRemoteContextRejectsMalformedHeader 断言畸形的 traceparent 不会被当成上游，
// 而是安全降级成「没有上游」—— 不能让一个坏头把本服务的 trace 结构带偏。
//
// 用两个用例覆盖两类典型坏输入：结构不合法（段数不够）、all-zero traceID（W3C 明令无效）。
// 第二个用例同时兼顾「全零 ID 经 hex 往返后仍应被判为无效」。
func TestExtractRemoteContextRejectsMalformedHeader(t *testing.T) {
	installGlobalPropagator(t)

	if raw, err := hex.DecodeString(strings.Repeat("0", 32)); err != nil || len(raw) != 16 {
		t.Fatalf("构造全零 traceID 失败: %v", err)
	}

	cases := []struct {
		name  string
		value string
	}{
		{"段数不足", "00-" + upstreamTraceID},
		{"traceID 全零", "00-" + strings.Repeat("0", 32) + "-" + upstreamSpanID + "-01"},
		{"空值", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			header := http.Header{}
			if c.value != "" {
				header.Set("traceparent", c.value)
			}
			ctx, info := ExtractRemoteContext(context.Background(), header)
			if info.Present {
				t.Fatalf("畸形 traceparent %q 不应被识别为上游父 span", c.value)
			}
			if ctx == nil {
				t.Fatal("畸形输入必须返回可用的 ctx，不能让调用方拿到 nil")
			}
		})
	}
}
