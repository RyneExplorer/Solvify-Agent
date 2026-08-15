package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"solvify-agent/pkg/config"
	"solvify-agent/pkg/logger"
)

// contextKey 用于在 context 中绑定 recorder / traceID / rootAttrs。
type contextKey string

const (
	traceIDKey    contextKey = "obs_trace_id"
	recorderKey   contextKey = "obs_recorder"
	rootAttrsKey  contextKey = "obs_root_attrs"
)

type rootAttrs struct {
	mu         sync.Mutex
	attrs      Attrs
	beginAt    time.Time
	rootDone   bool
	endErr     error
	endStatus  SpanStatus
	endAt      time.Time
	messageID  string
	userID     string
	sessionID  string
	requestID  string
	searchMode string
	modelID    string
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

type traceState struct {
	mu           sync.Mutex
	Trace        *Trace
	decision     SampleDecision
	PendingForce bool
}

// defaultRecorder 内部用 OTel Tracer 管运行时 span，用 promMetrics 管指标。
// Span 结构体仍保留，用于 DBSink 写 chat_traces 表（前端可视化数据源）。
type defaultRecorder struct {
	enabled     bool
	cfg         config.ObservabilityConfig
	sampler     *DefaultSampler
	sanitizer   *PIISanitizer
	sinks       Sink
	dbSink      DBSink
	metrics     *promMetrics
	tracer      trace.Tracer
	traceStates sync.Map
	traceDecide sync.Map
}

// NewRecorder 初始化 Recorder。需要在 InitTracerProvider / InitPrometheusRegistry 之后调用。
func NewRecorder(cfg config.ObservabilityConfig, extraSinks ...Sink) Recorder {
	sanitizer := NewPIISanitizer(cfg.PIIContentMaxChars, cfg.PIIMaskSecret)
	sampler := NewDefaultSampler(cfg.SamplingRate, cfg.ErrorAlwaysSample, cfg.FeedbackAlwaysSample, cfg.SlowThresholdMs, cfg.WhiteListUserIDs)
	logSink := NewLogSink(cfg.ExportLogEnabled, sanitizer, sampler)
	sinks := []Sink{logSink}
	sinks = append(sinks, extraSinks...)
	bs := NewBatchSink(sinks, cfg.SinkBufferSize, cfg.SinkBatchSize, cfg.SinkFlushIntervalMs)
	return &defaultRecorder{
		enabled:   cfg.Enabled,
		cfg:       cfg,
		sampler:   sampler,
		sanitizer: sanitizer,
		sinks:     bs,
		metrics:   GlobalMetrics(),
		tracer:    GlobalTracer(),
	}
}

// NewRecorderWithDBSink 在 NewRecorder 基础上挂 DBSink（写 chat_traces / chat_feedbacks / chat_agent_steps）。
func NewRecorderWithDBSink(cfg config.ObservabilityConfig, db DBSink) Recorder {
	r := NewRecorder(cfg).(*defaultRecorder)
	r.dbSink = db
	return r
}

// TraceIDFromContext 从 context 取 trace_id（由 WithTraceRoot 或 HTTP 中间件写入）。
// OTel 的 SpanContext 不直接暴露 TraceID 字符串，所以保留自研 traceIDKey 用于业务字段关联。
func TraceIDFromContext(ctx context.Context) string {
	v := ctx.Value(traceIDKey)
	if v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

// RecorderFromContext 从 context 取出绑定的 Recorder。
func RecorderFromContext(ctx context.Context) Recorder {
	v := ctx.Value(recorderKey)
	if v == nil {
		return nil
	}
	r, _ := v.(Recorder)
	return r
}

// currentSpanKey 用于 ctx 直接携带当前自研 *Span 引用。
// StartSpan 写入返回的 ctx，子 span 挂树和 CurrentSpanFromContext 都优先读它，
// 不依赖 OTel span 的 IsRecording 状态（eino 流式组件的 OnEnd 会提前 End 父 span）。
type currentSpanKey struct{}

// CurrentSpanFromContext 定位当前正在运行的 span。
// 返回项目自研 *Span（包含 otelSpan 字段），找不到时返回 nil，调用方静默降级。
func CurrentSpanFromContext(ctx context.Context) *Span {
	// 优先走 currentSpanKey：与 End 状态解耦，流式场景父 span 可能已被回调提前 End
	if s, ok := ctx.Value(currentSpanKey{}).(*Span); ok && s != nil {
		return s
	}
	// 兜底：OTel span 指针反查（不经 StartSpan 返回 ctx 的旧调用路径）
	otelSpan := trace.SpanFromContext(ctx)
	if otelSpan == nil {
		return nil
	}
	if !otelSpan.IsRecording() {
		return nil
	}
	if s, ok := spanByOtel.Load(otelSpan); ok {
		return s.(*Span)
	}
	return nil
}

// spanByOtel 把 OTel span 指针关联到项目自研 Span。
// key 是 trace.Span 接口（指针），value 是 *Span。
// StartSpan 写入，EndSpan 删除（避免内存泄漏）。
var spanByOtel sync.Map

// SetSpanAttrs 在 ctx 对应的当前 span 上直接追加/覆盖 attrs。
//
// 典型场景：RAG Retriever、LLM Client 等底层 adapter 在 Graph OnStart 开好 span 之后、
// EndSpan 关闭 span 之前，从内部补全细粒度业务字段（top_k/hit_n/avg_score/token 用量等）。
//
// 实现细节：
//   - 找不到当前 span 时静默返回（不影响业务流程）
//   - OTel span 的 SetAttributes 是幂等的，相同 key 会覆盖
//   - 自动走 PII Sanitizer，避免敏感信息落到 attrs
func SetSpanAttrs(ctx context.Context, attrs Attrs) {
	span := CurrentSpanFromContext(ctx)
	if span == nil || len(attrs) == 0 {
		return
	}
	sanitized := attrs
	if rec := RecorderFromContext(ctx); rec != nil {
		if dr, ok := rec.(*defaultRecorder); ok && dr != nil && dr.sanitizer != nil {
			sanitized = dr.sanitizer.SanitizeAttrs(attrs)
		}
	}
	// 写 OTel span（运行时追踪用）
	if span.otelSpan != nil {
		span.otelSpan.SetAttributes(attrsToOTel(sanitized)...)
	}
	// 写项目自研 Span（落库用）
	if span.Attrs == nil {
		span.Attrs = Attrs{}
	}
	for k, v := range sanitized {
		span.Attrs[k] = v
	}
}

// MergeSpanAttrs 同 SetSpanAttrs，语义别名。
func MergeSpanAttrs(ctx context.Context, attrs Attrs) { SetSpanAttrs(ctx, attrs) }

// AppendSpanAttrs 给指定 span 追加/覆盖 attrs。
//
// 典型场景（ChatModelGenerate Lambda）：流式输出读完最后一个 chunk 后，
// span.EndAt 已被设置，但 root span 还没 finalizeFlush，用此函数补 reply_preview 等字段。
//
// 实现细节：
//   - s == nil 或 attrs == nil 时静默降级
//   - rec 传 RecorderFromContext(ctx)（可选，传 nil 时跳过 PII sanitize 但仍会写入 attrs）
func AppendSpanAttrs(s *Span, attrs Attrs, rec Recorder) {
	if s == nil || len(attrs) == 0 {
		return
	}
	sanitized := attrs
	if dr, ok := rec.(*defaultRecorder); ok && dr != nil && dr.sanitizer != nil {
		sanitized = dr.sanitizer.SanitizeAttrs(attrs)
	}
	// 写 OTel span
	if s.otelSpan != nil {
		s.otelSpan.SetAttributes(attrsToOTel(sanitized)...)
	}
	// 写落库 Span
	if s.Attrs == nil {
		s.Attrs = Attrs{}
	}
	for k, v := range sanitized {
		s.Attrs[k] = v
	}
}

// attrsToOTel 把项目自研 Attrs 转成 OTel attribute.KeyValue 列表。
// OTel 的 SetAttributes 只接受 KeyValue，所以需要做类型转换。
func attrsToOTel(attrs Attrs) []attribute.KeyValue {
	if len(attrs) == 0 {
		return nil
	}
	out := make([]attribute.KeyValue, 0, len(attrs))
	for k, v := range attrs {
		kv, ok := toKeyValue(k, v)
		if !ok {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func toKeyValue(k string, v any) (attribute.KeyValue, bool) {
	switch val := v.(type) {
	case string:
		return attribute.String(k, val), true
	case int:
		return attribute.Int(k, val), true
	case int64:
		return attribute.Int64(k, val), true
	case float64:
		return attribute.Float64(k, val), true
	case bool:
		return attribute.Bool(k, val), true
	case []string:
		return attribute.StringSlice(k, val), true
	case []int:
		return attribute.IntSlice(k, val), true
	case []int64:
		return attribute.Int64Slice(k, val), true
	case []float64:
		return attribute.Float64Slice(k, val), true
	case []bool:
		return attribute.BoolSlice(k, val), true
	default:
		// 其他类型（map / struct / nil）转字符串兜底
		return attribute.String(k, fmt.Sprintf("%v", v)), true
	}
}

func (r *defaultRecorder) StartSpan(ctx context.Context, name string, component Component, attrs Attrs) (context.Context, *Span) {
	if !r.enabled {
		s := &Span{Name: name, Component: component, StartAt: time.Now()}
		return context.WithValue(ctx, traceIDKey, ""), s
	}

	traceID := TraceIDFromContext(ctx)
	if traceID == "" {
		traceID = randomHex(16)
		ctx = context.WithValue(ctx, traceIDKey, traceID)
	}

	// 落库 parent-child：优先从入参 ctx 的 currentSpanKey 取 parent 自研 Span 引用。
	// 不能用 trace.SpanFromContext(ctx).IsRecording() 判断：eino 流式组件（如 adk Agent）的
	// OnEnd 会在输出流刚返回时就 End 掉 span，而该 span 仍作为 ctx 的 current 传给后续子组件，
	// IsRecording()=false 会让整棵子树找不到 parent 变成孤儿（深度模式 trace 断裂的根因）。
	// ctx 引用与 End 状态解耦后，已 End 的 span 仍是合法 parent。
	var parentSpan *Span
	if ps, ok := ctx.Value(currentSpanKey{}).(*Span); ok && ps != nil {
		parentSpan = ps
	}

	// 用 OTel tracer.Start 创建运行时 span，OTel 自动管理 parent-child 关系。
	// 关键收益：运行时追踪的 parent-child 完全交给 OTel SDK，不管 Eino callback / compose.Graph /
	// InitCallbacks 包多少层 context.WithValue，OTel 的 trace.SpanFromContext(ctx) 永远能拿回当前 span。
	ctxWithSpan, otelSpan := r.tracer.Start(ctx, name, trace.WithAttributes(attrsToOTel(r.sanitizer.SanitizeAttrs(attrs))...))

	s := &Span{
		TraceID:   traceID,
		SpanID:    randomHex(8),
		Name:      name,
		Component: component,
		StartAt:   time.Now(),
		Status:    SpanStatusOK,
		Attrs:     r.sanitizer.SanitizeAttrs(attrs),
		otelSpan:  otelSpan,
		parent:    parentSpan,
	}

	// parent_id 用于落库：从 parent Span 拿 SpanID（如果有的话）
	if parentSpan != nil {
		s.ParentID = parentSpan.SpanID
	}

	// 关联 OTel span 到自研 Span，CurrentSpanFromContext 兜底路径用
	spanByOtel.Store(otelSpan, s)

	// ctx 携带自研 Span 引用：子 span 的 parent 查找与 CurrentSpanFromContext 走这里，
	// 与 span 的 End 状态解耦（见上方 parent 查找注释）。
	ctxWithSpan = context.WithValue(ctxWithSpan, currentSpanKey{}, s)

	// 登记 traceState 的根 span。用 LoadOrStore 防止后到的孤儿 span 覆盖已登记的树。
	isChatRoot := ctx.Value(rootAttrsKey) != nil
	if parentSpan == nil {
		if isChatRoot {
			// chat 场景：Root 是 chat.deep/chat.quick 等中间根，publishTrace 时合并到合成 chat.request 下
			if s.Attrs == nil {
				s.Attrs = Attrs{}
			}
			s.Attrs["__chat_intermediate_root"] = true
		}
		r.traceStates.LoadOrStore(traceID, &traceState{Trace: &Trace{ID: traceID, Root: s, SampleRate: r.cfg.SamplingRate}})
	}

	r.metrics.obsSpanStartTotal.WithLabelValues(string(component)).Inc()
	return ctxWithSpan, s
}

func (r *defaultRecorder) AddEvent(ctx context.Context, span *Span, name string, attrs Attrs) {
	if span == nil {
		return
	}
	e := &SpanEvent{Name: name, Timestamp: time.Now(), Attrs: r.sanitizer.SanitizeAttrs(attrs)}
	span.Events = append(span.Events, e)
	// 同步给 OTel span（运行时追踪用）
	if span.otelSpan != nil {
		span.otelSpan.AddEvent(name, trace.WithAttributes(attrsToOTel(e.Attrs)...))
	}
}

func (r *defaultRecorder) EndSpan(ctx context.Context, span *Span, status SpanStatus, err error, attrs Attrs) {
	if span == nil {
		return
	}
	span.EndAt = time.Now()
	span.DurationMs = span.EndAt.Sub(span.StartAt).Milliseconds()
	span.Status = status
	if err != nil {
		span.Error = r.sanitizer.SanitizeString(err.Error())
	}
	if len(attrs) > 0 {
		sanitized := r.sanitizer.SanitizeAttrs(attrs)
		if span.Attrs == nil {
			span.Attrs = Attrs{}
		}
		for k, v := range sanitized {
			span.Attrs[k] = v
		}
		// 同步写 OTel span
		if span.otelSpan != nil {
			span.otelSpan.SetAttributes(attrsToOTel(sanitized)...)
		}
	}

	// 关闭 OTel span：设置 status + 记录 error + End
	if span.otelSpan != nil {
		switch status {
		case SpanStatusError:
			span.otelSpan.SetStatus(codes.Error, span.Error)
			if err != nil {
				span.otelSpan.RecordError(err)
			}
		case SpanStatusCanceled:
			span.otelSpan.SetStatus(codes.Error, "canceled")
		case SpanStatusOK:
			span.otelSpan.SetStatus(codes.Ok, "")
		}
		span.otelSpan.End(trace.WithTimestamp(span.EndAt))
		// 清理 spanByOtel 关联，避免内存泄漏
		spanByOtel.Delete(span.otelSpan)
	}

	// 落库 parent-child：用 StartSpan 时存的 parent 引用挂接 children。
	// 不依赖 trace.SpanFromContext(ctx)：ctx 可能是 eino_callback 透传的 ctxWithSpan，
	// SpanFromContext 拿回的是当前 span 自己，parent != span 永远失败。
	if span.parent != nil {
		if span.parent.Children == nil {
			span.parent.Children = []*Span{}
		}
		span.parent.Children = append(span.parent.Children, span)
	}

	// 根 span 结束时触发 finalizeTrace；chat 中间 root span 跳过
	if span.TraceID != "" {
		isIntermediate := false
		if span.Attrs != nil {
			if v, ok := span.Attrs["__chat_intermediate_root"].(bool); ok && v {
				isIntermediate = true
			}
		}
		if !isIntermediate {
			if stVal, ok := r.traceStates.Load(span.TraceID); ok {
				if st, ok := stVal.(*traceState); ok && st.Trace != nil && st.Trace.Root == span {
					r.finalizeTrace(ctx, span, err)
				}
			}
		}
	}

	r.metrics.obsSpanDurationSec.WithLabelValues(string(span.Component), string(span.Status)).Observe(float64(span.DurationMs) / 1000.0)
}

func (r *defaultRecorder) finalizeTrace(ctx context.Context, root *Span, endErr error) {
	if root == nil {
		return
	}
	traceID := root.TraceID
	userID := ""
	sessionID := ""
	requestID := ""
	if root.Attrs != nil {
		if v, ok := root.Attrs["user_id"]; ok {
			userID, _ = v.(string)
		}
		if v, ok := root.Attrs["session_id"]; ok {
			sessionID, _ = v.(string)
		}
		if v, ok := root.Attrs["request_id"]; ok {
			requestID, _ = v.(string)
		}
	}
	// 兜底：空值统一填 unknown，避免数据库字段为空导致前端列表筛选 / 详情查询失败
	if userID == "" {
		userID = "unknown"
	}
	if sessionID == "" {
		sessionID = "unknown"
	}
	hasErr := endErr != nil || root.Status == SpanStatusError || root.Status == SpanStatusCanceled
	hasFeedback := false
	rawDecision, _ := r.traceDecide.LoadAndDelete(traceID)
	var decision SampleDecision
	if rawDecision != nil {
		decision, _ = rawDecision.(SampleDecision)
	}
	dur := time.Duration(root.DurationMs) * time.Millisecond
	sampled := r.sampler.ShouldSample(traceID, userID, hasErr, dur, hasFeedback, decision)
	t := &Trace{
		ID:         traceID,
		RequestID:  requestID,
		UserID:     userID,
		SessionID:  sessionID,
		Root:       root,
		SampleRate: r.cfg.SamplingRate,
		Sampled:    sampled,
	}
	r.traceStates.Delete(traceID)
	if sampled {
		// 写库用独立 context，避免 HTTP 请求结束后 ctx 被取消导致写库失败
		writeCtx, writeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer writeCancel()
		rec := &SinkRecord{Kind: "trace", Timestamp: time.Now(), Trace: t}
		if e := r.sinks.Write(writeCtx, rec); e != nil {
			logger.Warnf("trace 写入 sink 失败: %v", e)
		}
		if r.dbSink != nil && r.cfg.TraceTableEnabled {
			if err := r.dbSink.WriteTraces(writeCtx, []*Trace{t}); err != nil {
				logger.Warnf("trace 写库失败: %v", err)
				r.metrics.obsDBSinkErrorsTotal.WithLabelValues("trace").Inc()
			}
		}
	} else {
		r.metrics.obsTraceNotSampled.Inc()
	}
}

// Incr 是业务代码统一 metric 入口，内部代理到 Prometheus CounterVec。
func (r *defaultRecorder) Incr(ctx context.Context, metric string, labels map[string]string, delta int64) {
	if !r.enabled {
		return
	}
	incrProm(r.metrics, metric, labels, delta)
}

// Observe 是业务代码统一 histogram 入口，内部代理到 Prometheus HistogramVec。
func (r *defaultRecorder) Observe(ctx context.Context, metric string, labels map[string]string, value float64) {
	if !r.enabled {
		return
	}
	observeProm(r.metrics, metric, labels, value)
}

func (r *defaultRecorder) RecordTrace(trace *Trace) {
	if trace == nil || !r.enabled {
		return
	}
	rec := &SinkRecord{Kind: "trace", Timestamp: time.Now(), Trace: trace}
	if err := r.sinks.Write(context.Background(), rec); err != nil {
		logger.Warnf("RecordTrace: %v", err)
	}
	if r.dbSink != nil && r.cfg.TraceTableEnabled && trace.Sampled {
		if err := r.dbSink.WriteTraces(context.Background(), []*Trace{trace}); err != nil {
			r.metrics.obsDBSinkErrorsTotal.WithLabelValues("trace").Inc()
		}
	}
}

func (r *defaultRecorder) RecordFeedback(fb *Feedback) {
	if fb == nil || !r.enabled {
		return
	}
	if fb.CreatedAt.IsZero() {
		fb.CreatedAt = time.Now()
	}
	if fb.TraceID != "" {
		r.traceDecide.Store(fb.TraceID, SampleDecisionForceKeep)
	}
	rec := &SinkRecord{Kind: "feedback", Timestamp: fb.CreatedAt, Feedback: fb}
	if err := r.sinks.Write(context.Background(), rec); err != nil {
		logger.Warnf("RecordFeedback: %v", err)
	}
	if r.dbSink != nil {
		if err := r.dbSink.WriteFeedbacks(context.Background(), []*Feedback{fb}); err != nil {
			r.metrics.obsDBSinkErrorsTotal.WithLabelValues("feedback").Inc()
		}
	}
}

func (r *defaultRecorder) RecordAgentStep(step *AgentStep) {
	if step == nil || !r.enabled {
		return
	}
	rec := &SinkRecord{Kind: "agent_step", Timestamp: time.Now(), AgentStep: step}
	if err := r.sinks.Write(context.Background(), rec); err != nil {
		logger.Warnf("RecordAgentStep: %v", err)
	}
	if r.dbSink != nil {
		if err := r.dbSink.WriteAgentSteps(context.Background(), []*AgentStep{step}); err != nil {
			r.metrics.obsDBSinkErrorsTotal.WithLabelValues("agent_step").Inc()
		}
	}
}

// MetricsSnapshot 返回 Prometheus 指标快照（JSON 格式）。
// 用于 GET /api/v1/chat/metrics 管理端接口（非 /metrics 标准抓取）。
func (r *defaultRecorder) MetricsSnapshot() (map[string]any, error) {
	if !r.enabled {
		return nil, errors.New("observability disabled")
	}
	return prometheusSnapshot(GlobalPromRegistry()), nil
}

func (r *defaultRecorder) Config() config.ObservabilityConfig { return r.cfg }

func (r *defaultRecorder) SamplingRate() float64 { return r.cfg.SamplingRate }

func (r *defaultRecorder) Shutdown(ctx context.Context) error {
	if r.sinks != nil {
		return r.sinks.Shutdown(ctx)
	}
	return nil
}

func (r *defaultRecorder) WithTraceRoot(ctx context.Context, attrs TraceRootAttrs) context.Context {
	ctx = context.WithValue(ctx, recorderKey, r)
	traceID := TraceIDFromContext(ctx)
	if traceID == "" {
		traceID = randomHex(16)
		ctx = context.WithValue(ctx, traceIDKey, traceID)
	}
	ra := &rootAttrs{
		attrs: Attrs{
			"user_id":     attrs.UserID,
			"session_id":  attrs.SessionID,
			"message_id":  attrs.MessageID,
			"request_id":  attrs.RequestID,
			"search_mode": attrs.SearchMode,
			"model_id":    attrs.ModelID,
		},
		beginAt:    time.Now(),
		userID:     attrs.UserID,
		sessionID:  attrs.SessionID,
		messageID:  attrs.MessageID,
		requestID:  attrs.RequestID,
		searchMode: attrs.SearchMode,
		modelID:    attrs.ModelID,
	}
	return context.WithValue(ctx, rootAttrsKey, ra)
}

func (r *defaultRecorder) AddRootAttrs(ctx context.Context, attrs Attrs) {
	if v := ctx.Value(rootAttrsKey); v != nil {
		if ra, ok := v.(*rootAttrs); ok {
			ra.mu.Lock()
			if ra.attrs == nil {
				ra.attrs = Attrs{}
			}
			for k, val := range r.sanitizer.SanitizeAttrs(attrs) {
				ra.attrs[k] = val
			}
			ra.mu.Unlock()
		}
	}
}

// MarkTraceError 将当前 trace 的根 span 标记为错误状态。
// processMessage / processDeepMode 是 void 方法，内部错误通过事件流发送后无法传递给 FlushTrace。
// 在错误路径调用此方法，写入 rootAttrs.endErr + endStatus，publishTrace 时会据此设置根 span 状态。
func (r *defaultRecorder) MarkTraceError(ctx context.Context, err error) {
	if err == nil {
		return
	}
	v := ctx.Value(rootAttrsKey)
	if v == nil {
		return
	}
	ra, ok := v.(*rootAttrs)
	if !ok {
		return
	}
	ra.mu.Lock()
	ra.endErr = err
	ra.endStatus = SpanStatusError
	ra.mu.Unlock()
}

// PreviewAttr 暴露给 eino_callback 等业务方：先做 PII mask，再按 rune 数截断，
// 同时附带"被截断了多少字符"的尾标，方便前端一眼判断是否有更多内容。
func (r *defaultRecorder) PreviewAttr(text string, maxRunes int) string {
	if r == nil {
		return defaultSanitizerSingleton().TruncatePreview(text, maxRunes)
	}
	return r.sanitizer.TruncatePreview(text, maxRunes)
}

func defaultSanitizerSingleton() *PIISanitizer {
	return NewPIISanitizer(2000, true)
}

func (r *defaultRecorder) ForceSampling(ctx context.Context) {
	traceID := TraceIDFromContext(ctx)
	if traceID == "" {
		return
	}
	r.traceDecide.Store(traceID, SampleDecisionForceKeep)
}

func (r *defaultRecorder) FlushTrace(ctx context.Context, userID, sessionID, messageID string) string {
	if !r.enabled {
		return ""
	}
	traceID := TraceIDFromContext(ctx)
	if traceID == "" {
		return ""
	}
	var ra *rootAttrs
	if v := ctx.Value(rootAttrsKey); v != nil {
		ra, _ = v.(*rootAttrs)
	}
	if ra != nil {
		ra.mu.Lock()
		if ra.userID == "" {
			ra.userID = userID
		}
		if ra.sessionID == "" {
			ra.sessionID = sessionID
		}
		if ra.messageID == "" {
			ra.messageID = messageID
		}
		ra.endAt = time.Now()
		ra.mu.Unlock()
	} else {
		ra = &rootAttrs{
			beginAt:   time.Now(),
			endAt:     time.Now(),
			userID:    userID,
			sessionID: sessionID,
			messageID: messageID,
			endStatus: SpanStatusOK,
		}
	}
	r.publishTrace(ctx, traceID, ra)
	return traceID
}

func (r *defaultRecorder) publishTrace(ctx context.Context, traceID string, ra *rootAttrs) {
	var (
		userID     string
		sessionID  string
		requestID  string
		attrs      Attrs
		beginAt    time.Time
		endAt      time.Time
		endErr     error
		endStatus  SpanStatus
		messageID  string
		searchMode string
		modelID    string
	)
	if ra != nil {
		ra.mu.Lock()
		userID = ra.userID
		sessionID = ra.sessionID
		requestID = ra.requestID
		beginAt = ra.beginAt
		endAt = ra.endAt
		endErr = ra.endErr
		endStatus = ra.endStatus
		messageID = ra.messageID
		searchMode = ra.searchMode
		modelID = ra.modelID
		attrs = make(Attrs, len(ra.attrs))
		for k, v := range ra.attrs {
			attrs[k] = v
		}
		ra.rootDone = true
		ra.mu.Unlock()
	}
	if beginAt.IsZero() {
		beginAt = time.Now()
	}
	if endAt.IsZero() {
		endAt = time.Now()
	}
	if endStatus == "" {
		if endErr != nil {
			endStatus = SpanStatusError
		} else {
			endStatus = SpanStatusOK
		}
	}
	if attrs == nil {
		attrs = Attrs{}
	}
	if userID != "" {
		attrs["user_id"] = userID
	}
	if sessionID != "" {
		attrs["session_id"] = sessionID
	}
	if messageID != "" {
		attrs["message_id"] = messageID
	}
	if requestID != "" {
		attrs["request_id"] = requestID
	}
	if searchMode != "" {
		attrs["search_mode"] = searchMode
	}
	if modelID != "" {
		attrs["model_id"] = modelID
	}
	dur := endAt.Sub(beginAt)
	root := &Span{
		TraceID:    traceID,
		SpanID:     traceID,
		Name:       "chat.request",
		Component:  ComponentServiceChat,
		StartAt:    beginAt,
		EndAt:      endAt,
		DurationMs: dur.Milliseconds(),
		Status:     endStatus,
		Attrs:      r.sanitizer.SanitizeAttrs(attrs),
	}
	if endErr != nil {
		root.Error = r.sanitizer.SanitizeString(endErr.Error())
	}
	if stVal, ok := r.traceStates.LoadAndDelete(traceID); ok {
		if st, ok := stVal.(*traceState); ok && st != nil && st.Trace != nil && st.Trace.Root != nil {
			prev := st.Trace.Root
			if prev.Name != root.Name {
				if root.Children == nil {
					root.Children = []*Span{}
				}
				root.Children = append(root.Children, prev)
			} else {
				if prev.Children != nil {
					if root.Children == nil {
						root.Children = []*Span{}
					}
					root.Children = append(root.Children, prev.Children...)
				}
				if prev.Events != nil {
					root.Events = append(root.Events, prev.Events...)
				}
				if root.Attrs == nil {
					root.Attrs = Attrs{}
				}
				for k, v := range prev.Attrs {
					if _, exists := root.Attrs[k]; !exists {
						root.Attrs[k] = v
					}
				}
			}
		}
	}
	hasErr := endErr != nil || endStatus == SpanStatusError || endStatus == SpanStatusCanceled
	hasFeedback := false
	rawDecision, _ := r.traceDecide.LoadAndDelete(traceID)
	var decision SampleDecision
	if rawDecision != nil {
		decision, _ = rawDecision.(SampleDecision)
	}
	// user_id / session_id 空值兜底：从 attrs 回捞，仍为空则填 unknown
	if userID == "" {
		if root.Attrs != nil {
			if v, ok := root.Attrs["user_id"].(string); ok && v != "" {
				userID = v
			}
		}
		if userID == "" {
			userID = "unknown"
		}
	}
	if sessionID == "" {
		if root.Attrs != nil {
			if v, ok := root.Attrs["session_id"].(string); ok && v != "" {
				sessionID = v
			}
		}
		if sessionID == "" {
			sessionID = "unknown"
		}
	}
	sampled := r.sampler.ShouldSample(traceID, userID, hasErr, dur, hasFeedback, decision)
	t := &Trace{
		ID:         traceID,
		RequestID:  requestID,
		UserID:     userID,
		SessionID:  sessionID,
		Root:       root,
		SampleRate: r.cfg.SamplingRate,
		Sampled:    sampled,
	}
	// 写库用独立 context，避免 HTTP 请求结束后 ctx 被取消导致写库失败
	writeCtx, writeCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer writeCancel()
	rec := &SinkRecord{Kind: "trace", Timestamp: endAt, Trace: t}
	if e := r.sinks.Write(writeCtx, rec); e != nil {
		logger.Warnf("FlushTrace sink 写失败: %v", e)
	}
	if sampled && r.dbSink != nil && r.cfg.TraceTableEnabled {
		if err := r.dbSink.WriteTraces(writeCtx, []*Trace{t}); err != nil {
			logger.Warnf("FlushTrace 写库失败: %v", err)
			r.metrics.obsDBSinkErrorsTotal.WithLabelValues("trace").Inc()
		}
	}
	r.metrics.obsTraceFlushTotal.WithLabelValues(
		boolLabelO(sampled),
		searchModeOrDefault(searchMode),
	).Inc()
	r.metrics.obsTraceDurationSec.WithLabelValues(
		searchModeOrDefault(searchMode),
		string(endStatus),
	).Observe(dur.Seconds())
}

func boolLabelO(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func searchModeOrDefault(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

var (
	_ = context.Background
	_ = errors.New
)

// 保证编译期 defaultRecorder 实现 Recorder 接口
var _ Recorder = (*defaultRecorder)(nil)

// 兼容：metric_dispatcher.go 内部调用，从 metric name 找到对应的 prometheus 指标。
// 如果 metric 名不在已注册列表里，静默丢弃（避免 panic）。
func incrProm(m *promMetrics, name string, labels map[string]string, delta int64) {
	if m == nil {
		return
	}
	labelVals := labelMapToValues(labels)
	switch name {
	case "obs_span_start_total":
		m.obsSpanStartTotal.WithLabelValues(labelVals["component"]).Add(float64(delta))
	case "obs_db_sink_errors_total":
		m.obsDBSinkErrorsTotal.WithLabelValues(labelVals["type"]).Add(float64(delta))
	case "obs_trace_not_sampled_total":
		m.obsTraceNotSampled.Add(float64(delta))
	case "obs_trace_flush_total":
		m.obsTraceFlushTotal.WithLabelValues(labelVals["sampled"], labelVals["search_mode"]).Inc()
	case "http_request_total":
		m.HTTPRequestTotal.WithLabelValues(labelVals["method"], labelVals["route"], labelVals["status_group"]).Inc()
	case "http_error_total":
		m.HTTPErrorTotal.WithLabelValues(labelVals["method"], labelVals["route"], labelVals["status_group"]).Inc()
	case "http_panic_total":
		m.HTTPanicTotal.WithLabelValues(labelVals["method"], labelVals["route"], labelVals["type"]).Inc()
	case "eino_llm_requests_total":
		m.EinoLLMRequestsTotal.WithLabelValues(labelVals["component"], labelVals["name"], labelVals["model_id"]).Inc()
	case "eino_llm_stream_requests_total":
		m.EinoLLMStreamRequestsTotal.WithLabelValues(labelVals["component"], labelVals["name"], labelVals["model_id"]).Inc()
	case "eino_llm_errors_total":
		m.EinoLLMErrorsTotal.WithLabelValues(labelVals["component"], labelVals["name"]).Inc()
	case "eino_retriever_requests_total":
		m.EinoRetrieverRequestsTotal.WithLabelValues(labelVals["component"], labelVals["name"]).Inc()
	case "eino_retriever_errors_total":
		m.EinoRetrieverErrorsTotal.WithLabelValues(labelVals["component"], labelVals["name"]).Inc()
	case "eino_tool_calls_total":
		m.EinoToolCallsTotal.WithLabelValues(labelVals["component"], labelVals["name"], labelVals["tool_name"]).Inc()
	case "eino_tool_errors_total":
		m.EinoToolErrorsTotal.WithLabelValues(labelVals["component"], labelVals["name"], labelVals["tool_name"]).Inc()
	case "agent_tool_calls_total":
		m.AgentToolCallsTotal.WithLabelValues(labelVals["status"], labelVals["tool"]).Inc()
	case "eino_embed_requests_total":
		m.EinoEmbedRequestsTotal.WithLabelValues(labelVals["component"], labelVals["name"], labelVals["model_id"]).Inc()
	case "eino_embed_errors_total":
		m.EinoEmbedErrorsTotal.WithLabelValues(labelVals["component"], labelVals["name"]).Inc()
	case "eino_agent_runs_total":
		m.EinoAgentRunsTotal.WithLabelValues(labelVals["component"], labelVals["name"]).Inc()
	case "eino_agent_errors_total":
		m.EinoAgentErrorsTotal.WithLabelValues(labelVals["component"], labelVals["name"]).Inc()
	case "eino_stream_end_total":
		m.EinoStreamEndTotal.WithLabelValues(labelVals["component"], labelVals["name"]).Inc()
	case "chat_feedback_total":
		m.ChatFeedbackTotal.WithLabelValues(labelVals["rating"], labelVals["reason_tag"]).Inc()
	case "chat_deep_requests_total":
		m.ChatDeepRequestsTotal.WithLabelValues(labelVals["model_id"]).Inc()
	case "chat_deep_errors_total":
		m.ChatDeepErrorsTotal.WithLabelValues(labelVals["stage"]).Inc()
	case "ctx_summary_errors_total":
		m.CtxSummaryErrorsTotal.Add(float64(delta))
	case "ctx_summary_updates_total":
		m.CtxSummaryUpdatesTotal.Add(float64(delta))
	case "ctx_memory_errors_total":
		m.CtxMemoryErrorsTotal.Add(float64(delta))
	case "ctx_memory_extracted_total":
		m.CtxMemoryExtractedTotal.Add(float64(delta))
	case "agent_engine_runs_total":
		m.AgentEngineRunsTotal.Add(float64(delta))
	default:
		// 未注册的 metric 名，静默丢弃（避免 panic 影响业务流程）
	}
}

func observeProm(m *promMetrics, name string, labels map[string]string, value float64) {
	if m == nil {
		return
	}
	labelVals := labelMapToValues(labels)
	switch name {
	case "obs_span_duration_seconds":
		m.obsSpanDurationSec.WithLabelValues(labelVals["component"], labelVals["status"]).Observe(value)
	case "obs_trace_duration_seconds":
		m.obsTraceDurationSec.WithLabelValues(labelVals["search_mode"], labelVals["status"]).Observe(value)
	case "http_request_duration_seconds":
		m.HTTPRequestDuration.WithLabelValues(labelVals["method"], labelVals["route"]).Observe(value)
	case "eino_llm_duration_seconds":
		m.EinoLLMDurationSeconds.WithLabelValues(labelVals["component"], labelVals["name"], labelVals["model_id"]).Observe(value)
	case "eino_llm_total_tokens":
		m.EinoLLMTotalTokens.WithLabelValues(labelVals["component"], labelVals["name"], labelVals["model_id"]).Observe(value)
	case "eino_llm_prompt_tokens":
		m.EinoLLMPromptTokens.WithLabelValues(labelVals["component"], labelVals["name"], labelVals["model_id"]).Observe(value)
	case "eino_llm_completion_tokens":
		m.EinoLLMCompletionTokens.WithLabelValues(labelVals["component"], labelVals["name"], labelVals["model_id"]).Observe(value)
	case "eino_retriever_duration_seconds":
		m.EinoRetrieverDurationSeconds.WithLabelValues(labelVals["component"], labelVals["name"]).Observe(value)
	case "eino_retriever_hit_count":
		m.EinoRetrieverHitCount.WithLabelValues(labelVals["component"], labelVals["name"]).Observe(value)
	case "eino_tool_duration_seconds":
		m.EinoToolDurationSeconds.WithLabelValues(labelVals["component"], labelVals["name"], labelVals["tool_name"]).Observe(value)
	case "eino_embed_duration_seconds":
		m.EinoEmbedDurationSeconds.WithLabelValues(labelVals["component"], labelVals["name"], labelVals["model_id"]).Observe(value)
	case "eino_embed_total_tokens":
		m.EinoEmbedTotalTokens.WithLabelValues(labelVals["component"], labelVals["name"], labelVals["model_id"]).Observe(value)
	case "eino_embed_prompt_tokens":
		m.EinoEmbedPromptTokens.WithLabelValues(labelVals["component"], labelVals["name"], labelVals["model_id"]).Observe(value)
	case "eino_agent_duration_seconds":
		m.EinoAgentDurationSeconds.WithLabelValues(labelVals["component"], labelVals["name"]).Observe(value)
	case "eino_stream_end_seconds":
		m.EinoStreamEndSeconds.WithLabelValues(labelVals["component"], labelVals["name"]).Observe(value)
	case "chat_deep_init_ctx_seconds":
		m.ChatDeepInitCtxSeconds.WithLabelValues(labelVals["model_id"]).Observe(value)
	case "ctx_prompt_tokens_by_block":
		m.CtxPromptTokensByBlock.WithLabelValues(labelVals["model_id"], labelVals["block"]).Observe(value)
	default:
		// 未注册的 metric 名，静默丢弃
	}
}

// labelMapToValues 把 map[string]string 转成 map 用于 WithLabelValues 查找。
// 不存在的 key 返回空字符串（Prometheus 允许空标签值）。
func labelMapToValues(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(labels))
	for k, v := range labels {
		out[k] = v
	}
	return out
}

// prometheusSnapshot 从 Registry gather 指标并转成 JSON 快照。
// 展开每个 Metric 的 labels / value / count / sum / buckets，供 service 层 groupByMetric 消费。
func prometheusSnapshot(reg *prometheus.Registry) map[string]any {
	mfs, err := reg.Gather()
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	out := map[string]any{
		"generated_at_seconds": time.Now().Unix(),
	}
	counters := []any{}
	gauges := []any{}
	histos := []any{}
	for _, mf := range mfs {
		typ := mf.GetType().String()
		name := mf.GetName()
		help := mf.GetHelp()
		for _, m := range mf.GetMetric() {
			labels := []any{}
			for _, lp := range m.GetLabel() {
				labels = append(labels, map[string]any{"name": lp.GetName(), "value": lp.GetValue()})
			}
			switch typ {
			case "COUNTER":
				counters = append(counters, map[string]any{
					"name":   name,
					"help":   help,
					"labels": labels,
					"value":  m.GetCounter().GetValue(),
				})
			case "GAUGE":
				gauges = append(gauges, map[string]any{
					"name":   name,
					"help":   help,
					"labels": labels,
					"value":  m.GetGauge().GetValue(),
				})
			case "HISTOGRAM":
				h := m.GetHistogram()
				buckets := []any{}
				for _, b := range h.GetBucket() {
					var le any = b.GetUpperBound()
					if math.IsInf(le.(float64), 1) {
						le = "+Inf"
					}
					buckets = append(buckets, map[string]any{
						"le":          le,
						"delta_count": int64(b.GetCumulativeCount()),
					})
				}
				histos = append(histos, map[string]any{
					"name":    name,
					"help":    help,
					"labels":  labels,
					"count":   int64(h.GetSampleCount()),
					"sum":     h.GetSampleSum(),
					"buckets": buckets,
				})
			case "SUMMARY":
				s := m.GetSummary()
				histos = append(histos, map[string]any{
					"name":   name,
					"help":   help,
					"labels": labels,
					"count":  int64(s.GetSampleCount()),
					"sum":    s.GetSampleSum(),
				})
			}
		}
	}
	out["counters"] = counters
	out["gauges"] = gauges
	out["histograms"] = histos
	out["enabled"] = true
	return out
}
