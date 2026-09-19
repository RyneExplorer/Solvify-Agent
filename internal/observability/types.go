package observability

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/trace"
)

// SpanStatus 表示 span 的结束状态。
type SpanStatus string

// SpanStatus 枚举值。
const (
	SpanStatusOK       SpanStatus = "ok"
	SpanStatusError    SpanStatus = "error"
	SpanStatusCanceled SpanStatus = "canceled"
)

// Component 标识 span 所属的组件类别。
type Component string

// Component 枚举值，按业务模块划分。
const (
	ComponentHTTPServer     Component = "http.server"
	ComponentServiceChat    Component = "service.chat"
	ComponentServiceContext Component = "service.context"
	ComponentLLMClient      Component = "llm.client"
	ComponentRAGRetriever   Component = "rag.retriever"
	ComponentRAGReranker    Component = "rag.reranker"
	ComponentRAGExpander    Component = "rag.expander"
	ComponentAgentEngine    Component = "agent.engine"
	ComponentAgentTool      Component = "agent.tool"
	ComponentAgentStep      Component = "agent.step"
	ComponentRepository     Component = "repository"
)

// Attrs 是 span/event 使用的属性集合。
type Attrs map[string]any

// Merge 合并两个 Attrs，返回新对象。
func (a Attrs) Merge(other Attrs) Attrs {
	if len(other) == 0 {
		return a
	}
	out := make(Attrs, len(a)+len(other))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range other {
		out[k] = v
	}
	return out
}

// SpanEvent 表示 span 上的事件。
type SpanEvent struct {
	Name      string    `json:"name"`
	Timestamp time.Time `json:"timestamp"`
	Attrs     Attrs     `json:"attrs,omitempty"`
}

// Span 同时持有 OTel 运行时 span 和落库用的结构化字段。
//
// 设计取舍：
//   - otelSpan 字段不导出、不落盘 JSON（json:"-"），只用于运行时调 SetAttributes / End / AddEvent
//   - 其余字段（Name/Component/Attrs/Children 等）保留，用于 DBSink 写 chat_traces 表（前端可视化数据源）
//   - parent 字段保留：落库 parent-child 树用。OTel 运行时 parent-child 由 SDK 自动维护（正确），
//     但落库需要把 children 挂到 parent 上形成树。不能依赖 trace.SpanFromContext(ctx) 找 parent，
//     因为 ctx 可能被 Eino InitCallbacks 重包装，拿回的是当前 span 自己。
//   - OTel span 内部线程安全；落库前 finalizeTrace 单线程读写，不需要再加锁
//   - DurationMs 等「可测量」字段刻意不带 omitempty：0 是合法的测量值（亚毫秒的 span
//     用整数毫秒表达就是 0），而「字段不存在」的含义是「没采集到」，前端必须能区分这两者。
//     同类字段（TraceResponse / AgentStep / ChatTrace 等）由
//     internal/service/duration_ms_guard_test.go 的反射守卫统一兜住
type Span struct {
	TraceID    string       `json:"trace_id"`
	SpanID     string       `json:"span_id"`
	ParentID   string       `json:"parent_id,omitempty"`
	Name       string       `json:"name"`
	Component  Component    `json:"component"`
	StartAt    time.Time    `json:"start_at"`
	EndAt      time.Time    `json:"end_at,omitempty"`
	DurationMs int64        `json:"duration_ms"`
	Status     SpanStatus   `json:"status"`
	Error      string       `json:"error,omitempty"`
	Attrs      Attrs        `json:"attrs,omitempty"`
	Events     []*SpanEvent `json:"events,omitempty"`
	Children   []*Span      `json:"children,omitempty"`

	// OTelTraceID / OTelSpanID 是「双轨 traceID 对齐」字段，即 OTel SDK 为同一个 span
	// 生成的十六进制 ID，与上面的自研 TraceID / SpanID 是两套完全独立的 ID：
	//   - TraceID / SpanID：自研轨道，randomHex 生成，chat_traces.id 与落库 parent_id 用它
	//   - OTelTraceID / OTelSpanID：OTel 轨道，三方追踪平台（ByteAPM / Jaeger / Tempo 等）按它检索
	// 把后者记下来，DB 里的一条 trace 才能映射回三方平台上的同一条链路（chat_traces.otel_trace_id）。
	//
	// 注意：SpanContext 合法 != 三方平台查得到。OTelExporter=noop 时 TracerProvider 照样生成
	// 合法 SpanContext（这里照样有值），但没有任何 SpanProcessor 消费；采样率 < 1 时整条 trace
	// 还可能被丢弃。「三方是否真的有」由 Trace.OTelExported 表达，不要用本字段是否为空来判断。
	OTelTraceID string `json:"otel_trace_id,omitempty"`
	OTelSpanID  string `json:"otel_span_id,omitempty"`

	// otelSpan 运行时持有 OTel span，用于调 SetAttributes / End / AddEvent。
	// 落库时 json:"-" 忽略，避免 marshal 循环或暴露内部对象。
	otelSpan trace.Span `json:"-"`

	// parent 落库用：指向父 Span，EndSpan 时把当前 span append 到 parent.Children。
	// StartSpan 时从入参 ctx 的 currentSpanKey 取父 span（与 span 的 End 状态解耦），
	// spanByOtel 只是 CurrentSpanFromContext 的兜底反查，不参与建树。
	// json:"-" 避免序列化循环。
	parent *Span `json:"-"`
}

// Trace 表示一次完整的追踪记录。
type Trace struct {
	ID         string  `json:"id"`
	RequestID  string  `json:"request_id,omitempty"`
	UserID     string  `json:"user_id,omitempty"`
	SessionID  string  `json:"session_id,omitempty"`
	Root       *Span   `json:"root"`
	SampleRate float64 `json:"sample_rate,omitempty"`
	Sampled    bool    `json:"sampled"`

	// OTelTraceID 是自研 ID 对应的 OTel traceID（双轨对齐，落 chat_traces.otel_trace_id）。
	// 为空表示这条 trace 没走 OTel 轨道：recorder 未启用，或 ctx 里压根没有 OTel span。
	OTelTraceID string `json:"otel_trace_id,omitempty"`
	// OTelExported 表示这条 trace 是否「真的会出现在三方追踪平台上」。
	// OTelTraceID 非空但本字段为 false，说明只做到了本地关联（ID 一致可写日志串查），
	// 三方平台没有这条数据 —— 前端不要给出点进去是空白的跳转按钮。
	OTelExported bool `json:"otel_exported,omitempty"`
}

// Feedback 表示用户对消息的反馈。
type Feedback struct {
	MessageID   string    `json:"message_id"`
	UserID      string    `json:"user_id"`
	SessionID   string    `json:"session_id,omitempty"`
	Rating      int       `json:"rating"`
	Reasons     []string  `json:"reasons,omitempty"`
	ReasonTag   string    `json:"reason_tag,omitempty"`
	Comment     string    `json:"comment,omitempty"`
	TraceID     string    `json:"trace_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

// AgentStep 表示 Agent 单步执行记录。
type AgentStep struct {
	TaskID            string    `json:"task_id"`
	StepIndex         int       `json:"step_index"`
	StartedAt         time.Time `json:"started_at"`
	EndedAt           time.Time `json:"ended_at,omitempty"`
	ThinkingSummary   string    `json:"thinking_summary,omitempty"`
	ToolName          string    `json:"tool_name,omitempty"`
	ToolInputMasked   string    `json:"tool_input_masked,omitempty"`
	ToolResultSummary string    `json:"tool_result_summary,omitempty"`
	ToolStatus        string    `json:"tool_status,omitempty"`
	ToolError         string    `json:"tool_error,omitempty"`
	LatencyMs         int64     `json:"latency_ms"`
	TokensDelta       int       `json:"tokens_delta,omitempty"`
}

// SinkRecord 是 Sink 写入的统一记录格式。
type SinkRecord struct {
	Kind      string      `json:"kind"`
	Timestamp time.Time   `json:"timestamp"`
	Trace     *Trace      `json:"trace,omitempty"`
	Feedback  *Feedback   `json:"feedback,omitempty"`
	AgentStep *AgentStep  `json:"agent_step,omitempty"`
	Attrs     Attrs       `json:"attrs,omitempty"`
}

// Recorder 是业务代码统一的可观测性入口，内部实现基于 OTel + Prometheus。
// Incr / Observe 代理到 promMetrics 对应的 Prometheus Counter / Histogram。
// /metrics 路由直接挂 promhttp.HandlerFor(GlobalPromRegistry())，不走 Recorder 接口。
type Recorder interface {
	StartSpan(ctx context.Context, name string, component Component, attrs Attrs) (context.Context, *Span)
	EndSpan(ctx context.Context, span *Span, status SpanStatus, err error, attrs Attrs)
	AddEvent(ctx context.Context, span *Span, name string, attrs Attrs)
	Incr(ctx context.Context, metric string, labels map[string]string, delta int64)
	Observe(ctx context.Context, metric string, labels map[string]string, value float64)
	RecordTrace(trace *Trace)
	RecordFeedback(fb *Feedback)
	RecordAgentStep(step *AgentStep)
	MetricsSnapshot() (map[string]any, error)
	Shutdown(ctx context.Context) error
	WithTraceRoot(ctx context.Context, attrs TraceRootAttrs) context.Context
	FlushTrace(ctx context.Context, userID, sessionID, messageID string) string
	ForceSampling(ctx context.Context)
	AddRootAttrs(ctx context.Context, attrs Attrs)
	// MarkTraceError 标记当前 trace 根 span 为错误状态。
	// 典型场景：processMessage / processDeepMode 内部发生错误（LLM 超时、Agent 执行失败等），
	// 但这些方法不返回 error，FlushTrace 无法感知。在错误路径调用此方法，确保根 span 状态正确。
	MarkTraceError(ctx context.Context, err error)
	// PreviewAttr 对 attrs 的预览字段做"PII mask + rune 截断"，
	// 防止原始输入输出把 span_tree JSON 撑爆，也避免敏感信息直接落盘。
	// 典型 maxRunes：200 / 300 / 500 三档。
	PreviewAttr(text string, maxRunes int) string
}

// TraceRootAttrs 携带 trace 根级属性。
type TraceRootAttrs struct {
	UserID     string
	SessionID  string
	MessageID  string
	RequestID  string
	SearchMode string
	ModelID    string
}
