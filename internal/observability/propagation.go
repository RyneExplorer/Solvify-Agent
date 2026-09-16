package observability

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"
)

// tracerName 是 OTel 的 instrumentation scope 名称。
// 自研 span（recorder）与出站 HTTP span 共用同一个名字，三方平台按 scope 归组时才是一类。
const tracerName = "solvify-agent"

// textMapPropagator 是本服务要安装的传播器：W3C TraceContext + W3C Baggage。
//
// 为什么必须显式安装：OTel 的全局默认传播器是一个「没有任何 delegate 的复合传播器」，
// Inject 不写任何头、Extract 不读任何头 —— 跨进程 trace 上下文完全不传递。
// 在调用 InitPropagator 之前，本项目出站请求不带 traceparent，入站带来的 traceparent
// 也被直接忽略，链路必然断在服务边界上。这是「本地 trace 完整、三方平台看不到上下游」
// 最常见的根因。
//
// Baggage 当前不写入任何内容（空 baggage 不会产生 baggage 头），只是把管道预留出来，
// 将来跨服务传非敏感标签时不用再动全局配置。
var textMapPropagator = propagation.NewCompositeTextMapPropagator(
	propagation.TraceContext{},
	propagation.Baggage{},
)

// InitPropagator 把 W3C 传播器装到 OTel 全局，由 InitTracerProvider 调用。
//
// 必须装到全局、而不是就地用包级变量，原因是 OTel 的约定是「传播器只有一个全局实例」：
// 三方自动埋点（otelhttp、gRPC interceptor、将来接入的 SDK）一律读 otel.GetTextMapPropagator()。
// 如果只有本包的函数用包级变量，就会出现「我们的 span 打着 traceparent、自动埋点打的却是
// 另一套（或干脆不打）」的分裂，排查时极难想到。
// 所以本文件里的注入/提取也统一走全局实例，让 InitPropagator 成为唯一开关。
//
// 无条件设置（即使 OTelExporter=noop 也设），换来三种 exporter 模式下注入/提取行为一致：
// 自研 Recorder 的可用性与 OTelExporter 无关，一旦行为按 exporter 分裂，就会出现
// 「本地调试一切正常、上生产语义换了一套」这类极难排查的问题。
func InitPropagator() {
	otel.SetTextMapPropagator(textMapPropagator)
}

// DetachedTraceContext 返回一个「保留 trace 上下文、但切断取消/超时」的派生 context。
//
// 适用场景：HTTP 响应返回之后仍要继续跑的后台任务（异步生成会话摘要、抽取用户记忆等）。
// 这类任务不能用请求的 ctx —— 响应一返回请求 ctx 就被取消，后台任务会被立刻打断；
// 但也不能图省事写 context.Background()：那会把当前 SpanContext 一并丢掉，
// 后台 span 失去父节点、各自成为独立根 trace。后果在自研页面上看不出来，
// 一接三方平台就是 trace 列表里一批「孤儿 trace」，且拿不到用户/会话归属。
//
// 实现只搬 SpanContext，不搬 Done/Err/Deadline 通道，所以取消信号被真正切断，
// 而父子关系与 traceID 保持不变。父 span 即使已经 End 也仍是合法 parent
// （OTel 的父子关系在建 span 时确定，与父的 End 状态无关），因此本函数对
// 「请求已结束、后台才开跑」的场景同样成立。
func DetachedTraceContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		// 没有有效 span（noop provider 或本就不在 trace 内）时退化为 Background，
		// 行为与改动前一致，不伪造任何 span 上下文。
		return context.Background()
	}
	return trace.ContextWithSpanContext(context.Background(), sc)
}

// InboundTraceInfo 描述入站请求携带的远程 trace 上下文，供中间件记属性与排查使用。
type InboundTraceInfo struct {
	// Present 表示请求头里有一份合法、且标记为 remote 的 SpanContext。
	Present bool
	// TraceID / SpanID 是上游 traceID 与上游父 spanID，Present 为 true 时有值。
	TraceID string
	SpanID  string
	// Sampled 是上游对这条 trace 的采样决定。
	//
	// 上游明确说不采样时，ParentBased 采样策略下本服务也会跟着不记录。这是 W3C 跨服务
	// 追踪的预期语义，但外在现象是「本地采样率调成 1 也一条 span 都没有」，
	// 所以必须把这个信号显式暴露出来，否则只能靠猜。
	Sampled bool
}

// ExtractRemoteContext 从入站 HTTP 头提取上游的 trace 上下文。
//
// 返回的 ctx 若携带合法的远程 SpanContext，之后 tracer.Start 出来的 span 会成为上游
// span 的子节点并复用同一个 traceID —— 这是跨服务链路连起来的关键一步。不提取的话，
// 每个服务各自新建根 span，三方平台上就是一串互不相关的孤立 trace。
//
// 第二个返回值报告提取结果；没有可用的远程上下文时原样返回入参 ctx。
func ExtractRemoteContext(ctx context.Context, header http.Header) (context.Context, InboundTraceInfo) {
	out := otel.GetTextMapPropagator().Extract(ctx, propagation.HeaderCarrier(header))
	sc := trace.SpanContextFromContext(out)
	if !sc.IsValid() || !sc.IsRemote() {
		return ctx, InboundTraceInfo{}
	}
	return out, InboundTraceInfo{
		Present: true,
		TraceID: sc.TraceID().String(),
		SpanID:  sc.SpanID().String(),
		Sampled: sc.IsSampled(),
	}
}

// InjectContext 把 ctx 里的 trace 上下文写进出站 HTTP 头（traceparent / tracestate，
// 以及非空时的 baggage）。ctx 里没有合法 SpanContext 时是安全的空操作。
//
// 走 otel.GetTextMapPropagator() 而不是包级变量，保证「本函数的注入」与「三方自动埋点的注入」
// 用的是同一个传播器（见 InitPropagator 的注释）。
func InjectContext(ctx context.Context, header http.Header) {
	if header == nil {
		return
	}
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(header))
}

// tracingTransport 给出站 HTTP 补上「客户端 span + traceparent 注入」。
type tracingTransport struct {
	base http.RoundTripper
}

// HTTPTransport 把一个 http.RoundTripper 包装成带追踪能力的 RoundTripper。
//
// 用法：&http.Client{Transport: observability.HTTPTransport(base)}
// base 为 nil 时退回 http.DefaultTransport，方便直接包裸 http.Client。
//
// 它做两件事：
//  1. 为每次出站请求建一个 SpanKindClient 的 span，并补全 HTTP client 语义属性
//     （http.request.method / url.full / server.address / server.port /
//     http.response.status_code / error.type）。这些是三方追踪平台 HTTP 面板
//     直接读取的标准键，自研属性名替代不了。
//  2. 把当前 trace 上下文注入请求头，让下游服务或模型 API 能在同一条 trace 上
//     继续追加 span。注入必须发生在 span.Start 之后 —— 否则写出去的是父 span 的
//     spanID，下游会挂到错误的位置上，链路看起来「接上了」其实接错了。
//
// 这里没有引入官方 otelhttp：otel 核心 semconv 已经提供了全部所需的标准属性
// 构造函数，自研约 60 行即可覆盖，无需新增模块依赖，也避免了与 otel v1.45.0
// 不配套的 contrib 版本带来的版本漂移。
func HTTPTransport(base http.RoundTripper) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &tracingTransport{base: base}
}

func (t *tracingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	// 每次请求才解析 tracer，而不是构造时抓一次：HTTPTransport 很可能在
	// InitTracerProvider 之前就被包到 http.Client 上（包级变量初始化早于 app 启动），
	// 构造时抓会永久绑定到 noop provider，后面再初始化也救不回来。
	tracer := otel.Tracer(tracerName)

	// span 名用 HTTP 方法本身（OTel HTTP client 语义约定的默认值）。
	// 不拼 host/path 是为了控制 span 名的基数：工具类调用（http_provider）的目标 URL
	// 由用户输入决定，拼进 span 名会让 span 名数量随用户输入无限膨胀，
	// 按 span 名聚合的图表直接失效。定位下游靠 server.address / url.full 属性。
	ctx, span := tracer.Start(ctx, req.Method,
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(httpClientAttrs(req)...),
	)
	defer span.End()

	// RoundTripper 的契约要求不得修改传入的请求，所以克隆一份再写头。
	outReq := req.Clone(ctx)
	if outReq.Header == nil {
		outReq.Header = http.Header{}
	}

	// 优先用携带本 span 的 ctx 注入；若当前拿不到合法 SpanContext
	// （典型场景：OTelExporter=noop，TracerProvider 是 noop 的），退回原始 ctx ——
	// 那里可能还留着入站提取到的远程 SpanContext，这样即使本地不记录，
	// 也仍把上游链路原样透传给下游，不去主动切断别人的 trace。
	injectCtx := ctx
	if !trace.SpanFromContext(ctx).SpanContext().IsValid() {
		injectCtx = req.Context()
	}
	InjectContext(injectCtx, outReq.Header)

	resp, err := t.base.RoundTrip(outReq)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		// error.type 用 Go 错误类型：错误消息里可能含 URL/凭据，不适合上报。
		span.SetAttributes(semconv.ErrorTypeKey.String(fmt.Sprintf("%T", err)))
		return nil, err
	}
	if resp != nil {
		span.SetAttributes(semconv.HTTPResponseStatusCode(resp.StatusCode))
		if resp.StatusCode >= 400 {
			span.SetStatus(codes.Error, resp.Status)
			// 语义约定里 4xx/5xx 的 error.type 取状态码字符串，
			// 这样平台按 error.type 聚合时，能区分「下游报错」和「网络不通」。
			span.SetAttributes(semconv.ErrorTypeKey.String(strconv.Itoa(resp.StatusCode)))
		}
	}
	return resp, nil
}

// httpClientAttrs 组装出站请求的 HTTP client 语义属性。
//
// 注意这里绕过了 PII Sanitizer，直接以 OTel 原生属性写进 SDK span —— 这是刻意的：
// Sanitizer 的 PIIContentMaxChars 会把字符串截到 200 rune（见 recorder.go），
// url.full 这类长值会被截断成无意义的半截 URL；而本函数只产出受控的固定键
// （方法 / 已剥离 query 的 URL / 主机 / 端口 / 状态码 / error.type），
// 不含用户自由文本，所以不需要、也不应该走截断。新增字段前请先确认这一点。
func httpClientAttrs(req *http.Request) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, 5)
	attrs = append(attrs, semconv.HTTPRequestMethodKey.String(req.Method))

	u := req.URL
	if u == nil {
		return attrs
	}
	attrs = append(attrs, semconv.URLFull(safeURLFull(u)))
	if host := u.Hostname(); host != "" {
		attrs = append(attrs, semconv.ServerAddress(host))
	}
	if port := urlPort(u); port > 0 {
		attrs = append(attrs, semconv.ServerPort(port))
	}
	return attrs
}

// safeURLFull 只保留 scheme://host/path，主动丢掉 query、userinfo 与 fragment。
//
// 原因：url.full 会被写进三方追踪平台并长期留存，而 query string 里经常出现
// token / api_key / 签名参数（不少服务就是这么传凭据的），原样上报等于凭据外泄。
// path 本身已足够定位到具体下游接口，排障信息基本不损失。
func safeURLFull(u *url.URL) string {
	if u == nil {
		return ""
	}
	trimmed := *u
	trimmed.RawQuery = ""
	trimmed.ForceQuery = false
	trimmed.User = nil
	trimmed.Fragment = ""
	return trimmed.String()
}

// urlPort 取 URL 的端口，未显式写了就按 scheme 补默认端口。
func urlPort(u *url.URL) int {
	if u == nil {
		return 0
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err == nil {
			return n
		}
		return 0
	}
	switch u.Scheme {
	case "http":
		return 80
	case "https":
		return 443
	default:
		return 0
	}
}
