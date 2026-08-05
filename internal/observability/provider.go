package observability

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
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
		var exporter sdktrace.SpanExporter
		exporter, err = buildOTelExporter(ctx, cfg)
		if err != nil {
			logger.Warnf("OTel exporter 初始化失败，回退到 noop: %v", err)
			err = nil
			exporter = nil
		}

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

		// 头采样
		sampler := sdktrace.TraceIDRatioBased(cfg.OTelSamplingRate)
		if cfg.OTelSamplingRate <= 0 {
			sampler = sdktrace.NeverSample()
		} else if cfg.OTelSamplingRate >= 1 {
			sampler = sdktrace.AlwaysSample()
		}

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
		globalTracer = globalTracerProvider.Tracer("solvify-agent")
		otel.SetTracerProvider(globalTracerProvider)
	})

	if globalTracerProvider == nil {
		// 兜底 noop
		noopTP := trace.NewNoopTracerProvider()
		return noopTP, func(context.Context) error { return nil }, nil
	}
	return globalTracerProvider, globalTracerProvider.Shutdown, nil
}

// buildOTelExporter 根据 config 构造对应的 SpanExporter
func buildOTelExporter(ctx context.Context, cfg config.ObservabilityConfig) (sdktrace.SpanExporter, error) {
	switch cfg.OTelExporter {
	case "", "noop":
		return nil, nil
	case "stdout":
		// stdouttrace 把 span 以 JSON 形式打印到 stdout，开发期调试用
		return stdouttrace.New(stdouttrace.WithPrettyPrint())
	case "otlp":
		// 生产期走 OTLP gRPC 推到 Collector（默认 endpoint localhost:4317）
		ctxWithTimeout, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		exp, err := otlptracegrpc.New(ctxWithTimeout,
			otlptracegrpc.WithEndpoint(cfg.OTelOTLPEndpoint),
			otlptracegrpc.WithInsecure(),
		)
		if err != nil {
			return nil, fmt.Errorf("OTLP exporter 创建失败: %w", err)
		}
		return exp, nil
	default:
		return nil, errors.New("unknown otel_exporter: " + cfg.OTelExporter)
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
