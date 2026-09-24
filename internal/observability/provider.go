package observability

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"
	"go.opentelemetry.io/otel/trace"

	"solvify-agent/pkg/config"
	"solvify-agent/pkg/logger")

var (
	globalTracerProvider *sdktrace.TracerProvider
	globalTracer         trace.Tracer
	globalPromRegistry  *prometheus.Registry
	globalMetrics        *promMetrics

	tracerInitOnce sync.Once
	promInitOnce   sync.Once

	// otelExportActive 标记 OTel SDK 是否挂了真实 SpanExporter（InitTracerProvider 里设置）。
	//
	// 为什么需要这个标志：SpanContext 合法 != 这条 trace 真的会被导出。
	//   - OTelExporter=noop 时 exporter == nil，TracerProvider 没有 SpanProcessor，
	//     但 tracer.Start 照样生成合法 SpanContext（有 traceID），三方平台却查不到任何数据
	//   - 采样率 < 1 时 OTel 可能整条 trace 丢弃，同样查不到
	//   - 反过来，自研 track 只关心自己的采样率，会把 Trace 写进 chat_traces
	// 所以「三方平台到底有没有」= otelExportActive && SpanContext.IsSampled()，
	// 由 Trace.OTelExported 对外表达。开发环境默认 noop，这个标志恒为 false。
	otelExportActive atomic.Bool
)

// promMetrics 集中持有所有 Prometheus 指标变量。
type promMetrics struct {
	// OTel SDK 自身指标
	obsSpanStartTotal    *prometheus.CounterVec
	obsSpanDurationSec   *prometheus.HistogramVec
	obsDBSinkErrorsTotal *prometheus.CounterVec
	obsTraceNotSampled   prometheus.Counter
	obsTraceFlushTotal   *prometheus.CounterVec
	obsTraceDurationSec  *prometheus.HistogramVec

	// HTTP
	HTTPRequestTotal       *prometheus.CounterVec
	HTTPRequestDuration   *prometheus.HistogramVec
	HTTPRequestInflight    *prometheus.GaugeVec
	HTTPErrorTotal        *prometheus.CounterVec
	HTTPanicTotal         *prometheus.CounterVec

	// Eino LLM
	EinoLLMRequestsTotal      *prometheus.CounterVec
	EinoLLMDurationSeconds    *prometheus.HistogramVec
	EinoLLMTotalTokens        *prometheus.HistogramVec
	EinoLLMPromptTokens       *prometheus.HistogramVec
	EinoLLMCompletionTokens   *prometheus.HistogramVec
	EinoLLMStreamRequestsTotal *prometheus.CounterVec
	EinoLLMErrorsTotal        *prometheus.CounterVec

	// Eino Retriever
	EinoRetrieverRequestsTotal   *prometheus.CounterVec
	EinoRetrieverDurationSeconds *prometheus.HistogramVec
	EinoRetrieverHitCount        *prometheus.HistogramVec
	EinoRetrieverErrorsTotal     *prometheus.CounterVec

	// Eino Tool
	EinoToolCallsTotal     *prometheus.CounterVec
	EinoToolDurationSeconds *prometheus.HistogramVec
	EinoToolErrorsTotal    *prometheus.CounterVec
	AgentToolCallsTotal    *prometheus.CounterVec

	// Eino Embedding
	EinoEmbedRequestsTotal   *prometheus.CounterVec
	EinoEmbedDurationSeconds *prometheus.HistogramVec
	EinoEmbedTotalTokens     *prometheus.HistogramVec
	EinoEmbedPromptTokens    *prometheus.HistogramVec
	EinoEmbedErrorsTotal     *prometheus.CounterVec

	// Eino Agent / Graph
	EinoAgentRunsTotal      *prometheus.CounterVec
	EinoAgentDurationSeconds *prometheus.HistogramVec
	EinoAgentErrorsTotal    *prometheus.CounterVec

	// Eino Stream
	EinoStreamEndTotal   *prometheus.CounterVec
	EinoStreamEndSeconds *prometheus.HistogramVec

	// 业务指标
	ChatFeedbackTotal          *prometheus.CounterVec
	ChatDeepRequestsTotal      *prometheus.CounterVec
	ChatDeepErrorsTotal        *prometheus.CounterVec
	ChatDeepInitCtxSeconds     *prometheus.HistogramVec
	CtxSummaryErrorsTotal      prometheus.Counter
	CtxSummaryUpdatesTotal     prometheus.Counter
	CtxMemoryErrorsTotal       prometheus.Counter
	CtxMemoryExtractedTotal    prometheus.Counter
	CtxPromptTokensByBlock     *prometheus.HistogramVec
	AgentEngineRunsTotal       prometheus.Counter
	AgentEngineErrorsTotal    *prometheus.CounterVec
}

// InitTracerProvider 初始化 OTel TracerProvider，启动早期调一次。
func InitTracerProvider(ctx context.Context, cfg config.ObservabilityConfig) (tp trace.TracerProvider, shutdown func(context.Context) error, err error) {
	tracerInitOnce.Do(func() {
		// 传播器必须先装好，且与 exporter 无关：
		// 无论后面走 noop / stdout / otlp，出站注入与入站提取的语义都要一致。
		InitPropagator()

		var exporter sdktrace.SpanExporter
		exporter, err = buildOTelExporter(ctx, cfg)
		if err != nil {
			// 走到这里只可能是 stdouttrace 构造失败：配置取值非法已经被 config.Validate
			// 拦在启动前（见那里的 otel_exporter 校验）。后果是 span 不会被导出，
			// 但 span 生成、trace_id、自研落库都不受影响，三方链路本来也不走这条路
			// ⇒ 记 Error 让人看见，但不阻断启动。
			logger.Errorf("OTel exporter 初始化失败，span 将不会被导出，回退 noop: %v", err)
			err = nil
			exporter = nil
		}
		// 记录「是否真有 exporter 消费 span」，供 Trace.OTelExported 判断三方平台有无数据。
		otelExportActive.Store(exporter != nil)

		// Resource 描述服务身份
		res, resErr := resource.New(ctx,
			resource.WithAttributes(
				semconv.ServiceName(cfg.OTelServiceName),
				semconv.ServiceVersion("0.1.0"),
			),
		)
		if resErr != nil {
			logger.Warnf("OTel resource 初始化失败: %v", resErr)
		}

		sampler := samplerFor(cfg.OTelSamplingRate, cfg.OTelBizRoutes)
		// 采样策略必须在启动日志里可见：otel_biz_routes 若因键名拼错而没绑上，
		// 表现是「业务链路悄悄退回按采样率抽样」，从外部完全看不出来。
		logger.Infof("OTel 采样策略: 业务路由必留=%v；其余按采样率 %v", cfg.OTelBizRoutes, cfg.OTelSamplingRate)

		if exporter == nil {
			globalTracerProvider = sdktrace.NewTracerProvider(
				sdktrace.WithSampler(sampler),
				sdktrace.WithResource(res),
			)
		} else {
			globalTracerProvider = sdktrace.NewTracerProvider(
				sdktrace.WithSampler(sampler),
				sdktrace.WithResource(res),
				sdktrace.WithBatcher(exporter, sdktrace.WithBatchTimeout(200*time.Millisecond)),
			)
		}
		globalTracer = globalTracerProvider.Tracer(tracerName)
		otel.SetTracerProvider(globalTracerProvider)
	})

	if globalTracerProvider == nil {
		// 兜底 noop
		noopTP := trace.NewNoopTracerProvider()
		return noopTP, func(context.Context) error { return nil }, nil
	}
	return globalTracerProvider, globalTracerProvider.Shutdown, nil
}

// samplerFor 按本地采样率构造采样器：root span 按本地采样率决定，子 span 跟随父 span。
//
// 为什么外层必须包 ParentBased，而不是直接用 TraceIDRatioBased：
// 后者不看父 span，独立按本地采样率掷骰子。上游已经决定丢弃这条 trace，本地仍可能
// 采到并单独上报，三方平台上就会出现只有本服务半截 span 的孤儿 trace —— 比不采样更
// 误导人。ParentBased 的语义是「ctx 里有远程父 span 就跟随它的决定，没有才用 root
// sampler」，这才是跨服务追踪需要的一致性。
//
// 副作用：上游明确标记不采样时，即使本地采样率是 1 也不会记录。该信号由 http.request
// span 的 otel.inbound_parent_sampled 属性暴露，避免「调了采样率却没有 span」无从排查。
//
// 采样率只管「非业务路由」：命中 bizRoutes 的根 span 由 bizRouteSampler 无条件保留，
// 那几条链路带完整的 RAG / LLM 子树，是排障和给客户看的对象。
// 关键：这**不影响自研轨**。chat_traces 落不落库由 DefaultSampler 独立决定，
// 凡是带 session / message 归属的 trace 一律必留（见 SampleRequest.Required），
// 前端追踪页看到的链路一条不少 —— 这里只决定「往三方平台发多少」。
// 两条轨道判据不同的原因：输入不同（OTel 侧头采样时只知道路由，session_id 要等
// WithTraceRoot 才可知）、代价不同（三方按 trace 计费，本地库不花钱），所以是刻意
// 分成两个粒度，而不是漏了收口。配置则统一来自 ObservabilityConfig 一个来源。
func samplerFor(rate float64, bizRoutes []string) sdktrace.Sampler {
	var rootSampler sdktrace.Sampler
	switch {
	case rate <= 0:
		rootSampler = sdktrace.NeverSample()
	case rate >= 1:
		rootSampler = sdktrace.AlwaysSample()
	default:
		rootSampler = sdktrace.TraceIDRatioBased(rate)
	}
	if len(bizRoutes) > 0 {
		routes := make(map[string]struct{}, len(bizRoutes))
		for _, r := range bizRoutes {
			routes[r] = struct{}{}
		}
		rootSampler = bizRouteSampler{fallback: rootSampler, routes: routes}
	}
	return sdktrace.ParentBased(rootSampler)
}

// bizRouteSampler 让「业务路由」的根 span 无条件保留，其余交给 fallback 采样器。
//
// 为什么必须在根 span 上一次性决策，而不是每个 span 各判各的：OTel 的采样决定是整条
// trace 共用的。按 span 单独判会出现「根被丢、子被留」的碎片，三方平台上就是一棵没有
// 入口的树 —— 平台连它属于哪个请求都还原不出来，比整条不采更误导人。
// 根定下 Decision 之后，子 span 由外层 ParentBased 一路继承。
//
// 为什么不必自己判断「有没有父」：本类型始终被 sdktrace.ParentBased 包着，而 ParentBased
// 只在父 span 无效时才回调 root sampler（见 SDK trace/sampling.go）。也就是说它天然只会
// 收到根 span。有远程父 span 时跟随上游决定、业务白名单不生效 —— 这是刻意的：
// 跨服务链路要么整条留、要么整条丢，半截链路最误导人。
//
// 匹配用的是「精确相等」而不是前缀：所以配置里多写一条空串只会永远不命中，
// 不会退化成通配（前缀匹配才会，实测那会把追踪页轮询噪声整个放回来，见 OTelBizRoutes）。
type bizRouteSampler struct {
	fallback sdktrace.Sampler
	routes   map[string]struct{}
}

func (s bizRouteSampler) ShouldSample(p sdktrace.SamplingParameters) sdktrace.SamplingResult {
	if route, ok := routeAttr(p.Attributes); ok {
		if _, hit := s.routes[route]; hit {
			// 刻意不掷骰子：业务链路必留的含义就是「与采样率无关」
			return sdktrace.SamplingResult{Decision: sdktrace.RecordAndSample}
		}
	}
	return s.fallback.ShouldSample(p)
}

func (s bizRouteSampler) Description() string {
	return "BizRouteThen{" + s.fallback.Description() + "}"
}

// routeAttr 从 span 起始属性里取 HTTP 路由模板。
//
// 为什么只能读属性：Sampler 接口只拿得到 SamplingParameters，拿不到 gin.Context。
// 好在 gin 中间件建 http.request span 时已把 route 放进 recAttrs，StartSpan 又用
// trace.WithAttributes 交给 tracer.Start，SDK 会原样放进 SamplingParameters.Attributes
// （见 SDK trace/tracer.go）。所以业务侧不需要额外打任何标记 —— route 本身就是
// 「这个 span 属于哪条路由」的唯一声明，再补一个同义属性就变成两个来源了。
//
// 属性名与 gin_middleware.go 的 recAttrs["route"] 是同名契约，由
// TestTraceMiddlewareRouteAttrReachesSampler 端到端守住（改一边会红）。
func routeAttr(attrs []attribute.KeyValue) (string, bool) {
	for _, kv := range attrs {
		if string(kv.Key) == "route" && kv.Value.Type() == attribute.STRING {
			return kv.Value.AsString(), true
		}
	}
	return "", false
}

// buildOTelExporter 根据 config 构造对应的 SpanExporter。
//
// ⚠️ 这里刻意不再提供 OTLP 出口：三方追踪已统一交给官方 eino → Langfuse callback
// （见 internal/app/app.go 的 initLangfuseHandler），走平台私有 ingestion API，
// 既不需要 OTLP，也不需要 Collector 垫片。保留 OTel SDK 是为了两件事：
//   - 自研 span 树上记录的 otel_trace_id（双轨对齐、日志串查）
//   - stdout 模式便于本地看 span 长什么样
//
// 合法取值只有 config.OTelExporterNoop / config.OTelExporterStdout（空串等同 noop），
// 且与 config.Validate 引用【同一组常量】—— 不会出现「校验说合法、实现说不认识」。
// 写成别的值（例如老配置里残留的 otlp）会在 config.Validate 就被拦下、服务直接起不来，
// 根本走不到本函数；下面的 default 分支只是防御，因为本函数是纯函数、可能被单独调用。
func buildOTelExporter(ctx context.Context, cfg config.ObservabilityConfig) (sdktrace.SpanExporter, error) {
	switch cfg.OTelExporter {
	case config.OTelExporterNoop, "":
		return nil, nil
	case config.OTelExporterStdout:
		// stdouttrace 把 span 以 JSON 形式打印到 stdout，开发期调试用
		return stdouttrace.New(stdouttrace.WithPrettyPrint())
	default:
		return nil, fmt.Errorf("未知的 otel_exporter: %q（合法值只有 %s/%s；"+
			"OTLP 出口已移除，三方链路请改用 langfuse_* 配置走官方 eino callback）",
			cfg.OTelExporter, config.OTelExporterNoop, config.OTelExporterStdout)
	}
}

// InitPrometheusRegistry 初始化独立的 Prometheus Registry + 所有指标变量。
// 使用独立 Registry 而非 prometheus.DefaultRegisterer，避免和 expvar 或其他库冲突。
//
// 必须在应用启动早期调用一次，调用后全局可访问 globalPromRegistry 和 globalMetrics。
func InitPrometheusRegistry(cfg config.ObservabilityConfig) *prometheus.Registry {
	promInitOnce.Do(func() {
		globalPromRegistry = prometheus.NewRegistry()
		globalMetrics = newPromMetrics(globalPromRegistry)
	})
	return globalPromRegistry
}

// newPromMetrics 在指定 Registry 上注册所有 Prometheus 指标。
// 使用 promauto 工厂保证注册时 panic 能在启动期就暴露问题。
func newPromMetrics(reg *prometheus.Registry) *promMetrics {
	factory := promauto.With(reg)

	m := &promMetrics{
		// ── OTel SDK 自身指标 ──
		obsSpanStartTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "obs_span_start_total",
			Help: "Span 启动计数，按 component 维度统计",
		}, []string{"component"}),
		obsSpanDurationSec: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "obs_span_duration_seconds",
			Help:    "Span 持续时间分布",
			Buckets: prometheus.DefBuckets,
		}, []string{"component", "status"}),
		obsDBSinkErrorsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "obs_db_sink_errors_total",
			Help: "DBSink 写库失败计数",
		}, []string{"type"}),
		obsTraceNotSampled: factory.NewCounter(prometheus.CounterOpts{
			Name: "obs_trace_not_sampled_total",
			Help: "未采样的 trace 计数",
		}),
		obsTraceFlushTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "obs_trace_flush_total",
			Help: "trace flush 计数",
		}, []string{"sampled", "search_mode"}),
		obsTraceDurationSec: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "obs_trace_duration_seconds",
			Help:    "trace 整体持续时间分布",
			Buckets: prometheus.DefBuckets,
		}, []string{"search_mode", "status"}),

		// ── HTTP 指标 ──
		HTTPRequestTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "http_request_total",
			Help: "HTTP 请求总数",
		}, []string{"method", "route", "status_group"}),
		HTTPRequestDuration: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP 请求耗时分布",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route"}),
		HTTPRequestInflight: factory.NewGaugeVec(prometheus.GaugeOpts{
			Name: "http_request_inflight",
			Help: "当前在途 HTTP 请求",
		}, []string{"method", "route"}),
		HTTPErrorTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "http_error_total",
			Help: "HTTP 错误请求计数（status>=400）",
		}, []string{"method", "route", "status_group"}),
		HTTPanicTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "http_panic_total",
			Help: "HTTP 处理 panic 计数",
		}, []string{"method", "route", "type"}),

		// ── Eino LLM 指标 ──
		EinoLLMRequestsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "eino_llm_requests_total",
			Help: "Eino LLM 调用次数",
		}, []string{"component", "name", "model_id"}),
		EinoLLMDurationSeconds: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "eino_llm_duration_seconds",
			Help:    "Eino LLM 调用耗时",
			Buckets: prometheus.DefBuckets,
		}, []string{"component", "name", "model_id"}),
		EinoLLMTotalTokens: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "eino_llm_total_tokens",
			Help:    "Eino LLM 总 token 用量",
			Buckets: []float64{1, 10, 50, 100, 500, 1000, 5000, 10000, 50000},
		}, []string{"component", "name", "model_id"}),
		EinoLLMPromptTokens: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "eino_llm_prompt_tokens",
			Help:    "Eino LLM 输入 token 用量",
			Buckets: []float64{1, 10, 50, 100, 500, 1000, 5000, 10000, 50000},
		}, []string{"component", "name", "model_id"}),
		EinoLLMCompletionTokens: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "eino_llm_completion_tokens",
			Help:    "Eino LLM 输出 token 用量",
			Buckets: []float64{1, 10, 50, 100, 500, 1000, 5000, 10000, 50000},
		}, []string{"component", "name", "model_id"}),
		EinoLLMStreamRequestsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "eino_llm_stream_requests_total",
			Help: "Eino LLM 流式调用次数（兼容旧名）",
		}, []string{"component", "name", "model_id"}),
		EinoLLMErrorsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "eino_llm_errors_total",
			Help: "Eino LLM 调用错误计数",
		}, []string{"component", "name"}),

		// ── Eino Retriever 指标 ──
		EinoRetrieverRequestsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "eino_retriever_requests_total",
			Help: "Eino Retriever 调用次数",
		}, []string{"component", "name"}),
		EinoRetrieverDurationSeconds: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "eino_retriever_duration_seconds",
			Help:    "Eino Retriever 调用耗时",
			Buckets: prometheus.DefBuckets,
		}, []string{"component", "name"}),
		EinoRetrieverHitCount: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "eino_retriever_hit_count",
			Help:    "Eino Retriever 命中文档数分布",
			Buckets: []float64{0, 1, 3, 5, 10, 20, 50, 100},
		}, []string{"component", "name"}),
		EinoRetrieverErrorsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "eino_retriever_errors_total",
			Help: "Eino Retriever 错误计数",
		}, []string{"component", "name"}),

		// ── Eino Tool 指标 ──
		EinoToolCallsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "eino_tool_calls_total",
			Help: "Eino Tool 调用次数",
		}, []string{"component", "name", "tool_name"}),
		EinoToolDurationSeconds: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "eino_tool_duration_seconds",
			Help:    "Eino Tool 调用耗时",
			Buckets: prometheus.DefBuckets,
		}, []string{"component", "name", "tool_name"}),
		EinoToolErrorsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "eino_tool_errors_total",
			Help: "Eino Tool 错误计数",
		}, []string{"component", "name", "tool_name"}),
		AgentToolCallsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "agent_tool_calls_total",
			Help: "工具调用次数（兼容旧名，带 status=success/error）",
		}, []string{"status", "tool"}),

		// ── Eino Embedding 指标 ──
		EinoEmbedRequestsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "eino_embed_requests_total",
			Help: "Eino Embedding 调用次数",
		}, []string{"component", "name", "model_id"}),
		EinoEmbedDurationSeconds: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "eino_embed_duration_seconds",
			Help:    "Eino Embedding 调用耗时",
			Buckets: prometheus.DefBuckets,
		}, []string{"component", "name", "model_id"}),
		EinoEmbedTotalTokens: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "eino_embed_total_tokens",
			Help:    "Eino Embedding 总 token 用量",
			Buckets: []float64{1, 10, 50, 100, 500, 1000, 5000, 10000, 50000},
		}, []string{"component", "name", "model_id"}),
		EinoEmbedPromptTokens: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "eino_embed_prompt_tokens",
			Help:    "Eino Embedding 输入 token 用量",
			Buckets: []float64{1, 10, 50, 100, 500, 1000, 5000, 10000, 50000},
		}, []string{"component", "name", "model_id"}),
		EinoEmbedErrorsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "eino_embed_errors_total",
			Help: "Eino Embedding 错误计数",
		}, []string{"component", "name"}),

		// ── Eino Agent / Graph 指标 ──
		EinoAgentRunsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "eino_agent_runs_total",
			Help: "Eino Agent / Graph 执行次数",
		}, []string{"component", "name"}),
		EinoAgentDurationSeconds: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "eino_agent_duration_seconds",
			Help:    "Eino Agent / Graph 执行耗时",
			Buckets: prometheus.DefBuckets,
		}, []string{"component", "name"}),
		EinoAgentErrorsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "eino_agent_errors_total",
			Help: "Eino Agent / Graph 错误计数",
		}, []string{"component", "name"}),

		// ── Eino Stream 指标 ──
		EinoStreamEndTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "eino_stream_end_total",
			Help: "Eino 流式输出结束计数",
		}, []string{"component", "name"}),
		EinoStreamEndSeconds: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "eino_stream_end_seconds",
			Help:    "Eino 流式输出总耗时",
			Buckets: prometheus.DefBuckets,
		}, []string{"component", "name"}),

		// ── 业务指标 ──
		ChatFeedbackTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "chat_feedback_total",
			Help: "用户反馈计数",
		}, []string{"rating", "reason_tag"}),
		ChatDeepRequestsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "chat_deep_requests_total",
			Help: "深度模式请求总数",
		}, []string{"model_id"}),
		ChatDeepErrorsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "chat_deep_errors_total",
			Help: "深度模式错误计数",
		}, []string{"stage"}),
		ChatDeepInitCtxSeconds: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "chat_deep_init_ctx_seconds",
			Help:    "深度模式上下文初始化耗时",
			Buckets: prometheus.DefBuckets,
		}, []string{"model_id"}),
		CtxSummaryErrorsTotal: factory.NewCounter(prometheus.CounterOpts{
			Name: "ctx_summary_errors_total",
			Help: "上下文摘要错误计数",
		}),
		CtxSummaryUpdatesTotal: factory.NewCounter(prometheus.CounterOpts{
			Name: "ctx_summary_updates_total",
			Help: "上下文摘要更新次数",
		}),
		CtxMemoryErrorsTotal: factory.NewCounter(prometheus.CounterOpts{
			Name: "ctx_memory_errors_total",
			Help: "记忆抽取错误计数",
		}),
		CtxMemoryExtractedTotal: factory.NewCounter(prometheus.CounterOpts{
			Name: "ctx_memory_extracted_total",
			Help: "已抽取记忆条目数",
		}),
		CtxPromptTokensByBlock: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "ctx_prompt_tokens_by_block",
			Help:    "上下文各分块 token 用量",
			Buckets: []float64{1, 10, 50, 100, 500, 1000, 5000, 10000, 50000},
		}, []string{"model_id", "block"}),
		AgentEngineRunsTotal: factory.NewCounter(prometheus.CounterOpts{
			Name: "agent_engine_runs_total",
			Help: "Agent 引擎执行次数",
		}),
		AgentEngineErrorsTotal: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "agent_engine_errors_total",
			Help: "Agent 引擎错误计数",
		}, []string{"stage"}),
	}
	return m
}

// GlobalPromRegistry 返回全局 Prometheus Registry（启动后非 nil）
func GlobalPromRegistry() *prometheus.Registry {
	if globalPromRegistry == nil {
		// 兜底：未初始化时返回一个空 Registry，避免 nil panic
		return prometheus.NewRegistry()
	}
	return globalPromRegistry
}

// GlobalMetrics 返回全局 promMetrics（启动后非 nil）
//
// 兜底行为：若未调过 InitPrometheusRegistry，则创建一个临时空 metrics 注册到独立 Registry，
// 保证 NewRecorder 在初始化阶段拿到非 nil 的 metrics，避免后续 Incr/Observe 调用 nil panic。
// 这种兜底场景下 /metrics 路由不会暴露这些指标（因为不在 globalPromRegistry 里）。
func GlobalMetrics() *promMetrics {
	if globalMetrics == nil {
		globalMetrics = newPromMetrics(prometheus.NewRegistry())
	}
	return globalMetrics
}

// GlobalTracer 返回全局 OTel Tracer（启动后非 nil）
func GlobalTracer() trace.Tracer {
	if globalTracer == nil {
		// 兜底：未初始化时返回 noop tracer
		return trace.NewNoopTracerProvider().Tracer("solvify-fallback")
	}
	return globalTracer
}
