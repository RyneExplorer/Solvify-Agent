package observability

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"solvify-agent/pkg/config"
	"solvify-agent/pkg/logger"
)

type contextKey string

const (
	spanKey    contextKey = "obs_span"
	traceIDKey contextKey = "obs_trace_id"
	recorderKey contextKey = "obs_recorder"
	rootAttrsKey contextKey = "obs_root_attrs"
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

type defaultRecorder struct {
	enabled      bool
	cfg          config.ObservabilityConfig
	sampler      *DefaultSampler
	sanitizer    *PIISanitizer
	sinks        Sink
	dbSink       DBSink
	metrics      *MetricStore
	traceStates  sync.Map
	inflight     sync.Map
	traceDecide  sync.Map
}

func NewRecorder(cfg config.ObservabilityConfig, extraSinks ...Sink) Recorder {
	sanitizer := NewPIISanitizer(cfg.PIIContentMaxChars, cfg.PIIMaskSecret)
	sampler := NewDefaultSampler(cfg.SamplingRate, cfg.ErrorAlwaysSample, cfg.FeedbackAlwaysSample, cfg.SlowThresholdMs, cfg.WhiteListUserIDs)
	logSink := NewLogSink(cfg.ExportLogEnabled, sanitizer, sampler)
	sinks := []Sink{logSink}
	sinks = append(sinks, extraSinks...)
	bs := NewBatchSink(sinks, cfg.SinkBufferSize, cfg.SinkBatchSize, cfg.SinkFlushIntervalMs)
	ms := GlobalMetricStore(cfg.MaxCardinalityLabels)
	return &defaultRecorder{
		enabled:   cfg.Enabled,
		cfg:       cfg,
		sampler:   sampler,
		sanitizer: sanitizer,
		sinks:     bs,
		metrics:   ms,
	}
}

func NewRecorderWithDBSink(cfg config.ObservabilityConfig, db DBSink) Recorder {
	r := NewRecorder(cfg).(*defaultRecorder)
	r.dbSink = db
	return r
}

func spanFromContext(ctx context.Context) *Span {
	v := ctx.Value(spanKey)
	if v == nil {
		return nil
	}
	s, _ := v.(*Span)
	return s
}

func TraceIDFromContext(ctx context.Context) string {
	v := ctx.Value(traceIDKey)
	if v == nil {
		return ""
	}
	s, _ := v.(string)
	return s
}

func RecorderFromContext(ctx context.Context) Recorder {
	v := ctx.Value(recorderKey)
	if v == nil {
		return nil
	}
	r, _ := v.(Recorder)
	return r
}

func (r *defaultRecorder) StartSpan(ctx context.Context, name string, component Component, attrs Attrs) (context.Context, *Span) {
	if !r.enabled {
		s := &Span{Name: name, Component: component, StartAt: time.Now()}
		return context.WithValue(ctx, spanKey, s), s
	}
	traceID := TraceIDFromContext(ctx)
	if traceID == "" {
		traceID = randomHex(16)
		ctx = context.WithValue(ctx, traceIDKey, traceID)
	}
	parent := spanFromContext(ctx)
	s := &Span{
		TraceID:   traceID,
		SpanID:    randomHex(8),
		ParentID:  "",
		Name:      name,
		Component: component,
		StartAt:   time.Now(),
		Status:    SpanStatusOK,
		Attrs:     r.sanitizer.SanitizeAttrs(attrs),
	}
	if parent != nil {
		s.ParentID = parent.SpanID
	}
	ctx = context.WithValue(ctx, spanKey, s)
	if parent == nil {
		st := &traceState{Trace: &Trace{ID: traceID, Root: s, SampleRate: r.cfg.SamplingRate}}
		r.traceStates.Store(traceID, st)
	}
	r.metrics.Incr("obs_span_start_total", map[string]string{"component": string(component)}, 1)
	return ctx, s
}

func (r *defaultRecorder) AddEvent(ctx context.Context, span *Span, name string, attrs Attrs) {
	if span == nil {
		return
	}
	e := &SpanEvent{Name: name, Timestamp: time.Now(), Attrs: r.sanitizer.SanitizeAttrs(attrs)}
	span.Events = append(span.Events, e)
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
		if span.Attrs == nil {
			span.Attrs = Attrs{}
		}
		for k, v := range r.sanitizer.SanitizeAttrs(attrs) {
			span.Attrs[k] = v
		}
	}
	parent := spanFromContext(ctx)
	if parent != nil && parent != span {
		if parent.Children == nil {
			parent.Children = []*Span{}
		}
		parent.Children = append(parent.Children, span)
	}
	if span.ParentID == "" {
		r.finalizeTrace(ctx, span, err)
	}
	r.metrics.Observe("obs_span_duration_seconds", map[string]string{
		"component": string(span.Component),
		"status":    string(span.Status),
	}, float64(span.DurationMs)/1000.0, nil)
}

func (r *defaultRecorder) finalizeTrace(ctx context.Context, root *Span, endErr error) {
	if root == nil {
		return
	}
	traceID := root.TraceID
	userID := ""
	sessionID := ""
	requestID := ""
	if v := ctx.Value(traceIDKey); v != nil {
	}
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
	hasErr := endErr != nil || root.Status == SpanStatusError || root.Status == SpanStatusCanceled
	hasFeedback := false
	rawDecision, _ := r.traceDecide.LoadAndDelete(traceID)
	var decision SampleDecision
	if rawDecision != nil {
		decision, _ = rawDecision.(SampleDecision)
	}
	dur := time.Duration(root.DurationMs) * time.Millisecond
	sampled := r.sampler.ShouldSample(traceID, userID, hasErr, dur, hasFeedback, decision)
	if r.dbSink != nil {
		if v, ok := r.dbSink.(interface {
			Decide(ctx context.Context, traceID string, force bool)
		}); ok {
			_ = v
		}
	}
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
		rec := &SinkRecord{Kind: "trace", Timestamp: time.Now(), Trace: t}
		if e := r.sinks.Write(ctx, rec); e != nil {
			logger.Warnf("trace 写入 sink 失败: %v", e)
		}
		if r.dbSink != nil && r.cfg.TraceTableEnabled {
			if err := r.dbSink.WriteTraces(ctx, []*Trace{t}); err != nil {
				logger.Warnf("trace 写库失败: %v", err)
				r.metrics.Incr("obs_db_sink_errors_total", map[string]string{"type": "trace"}, 1)
			}
		}
	} else {
		r.metrics.Incr("obs_trace_not_sampled_total", nil, 1)
	}
}

func (r *defaultRecorder) Incr(ctx context.Context, metric string, labels map[string]string, delta int64) {
	if !r.enabled {
		return
	}
	r.metrics.Incr(metric, labels, delta)
}

func (r *defaultRecorder) Observe(ctx context.Context, metric string, labels map[string]string, value float64) {
	if !r.enabled {
		return
	}
	r.metrics.Observe(metric, labels, value, nil)
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
			r.metrics.Incr("obs_db_sink_errors_total", map[string]string{"type": "trace"}, 1)
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
			r.metrics.Incr("obs_db_sink_errors_total", map[string]string{"type": "feedback"}, 1)
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
			r.metrics.Incr("obs_db_sink_errors_total", map[string]string{"type": "agent_step"}, 1)
		}
	}
}

func (r *defaultRecorder) MetricsSnapshot() (map[string]any, error) {
	if !r.enabled {
		return nil, errors.New("observability disabled")
	}
	snap := r.metrics.SnapshotJSON()
	snap["enabled"] = true
	snap["sink_stats"] = map[string]any{
		"buffer_size_cfg": r.cfg.SinkBufferSize,
	}
	if bs, ok := r.sinks.(*BatchSink); ok {
		drops, writes := bs.Stats()
		snap["sink_stats"] = map[string]any{
			"dropped_records_total": drops,
			"written_records_total": writes,
		}
	}
	return snap, nil
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
	if messageID != "" {
		_ = messageID
	}
	if searchMode != "" {
		_ = searchMode
	}
	if modelID != "" {
		_ = modelID
	}
	hasErr := endErr != nil || endStatus == SpanStatusError || endStatus == SpanStatusCanceled
	hasFeedback := false
	rawDecision, _ := r.traceDecide.LoadAndDelete(traceID)
	var decision SampleDecision
	if rawDecision != nil {
		decision, _ = rawDecision.(SampleDecision)
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
	_ = t
	rec := &SinkRecord{Kind: "trace", Timestamp: endAt, Trace: t}
	if e := r.sinks.Write(ctx, rec); e != nil {
		logger.Warnf("FlushTrace sink 写失败: %v", e)
	}
	if sampled && r.dbSink != nil && r.cfg.TraceTableEnabled {
		if err := r.dbSink.WriteTraces(ctx, []*Trace{t}); err != nil {
			logger.Warnf("FlushTrace 写库失败: %v", err)
			r.metrics.Incr("obs_db_sink_errors_total", map[string]string{"type": "trace"}, 1)
		}
	}
	r.metrics.Incr("obs_trace_flush_total", map[string]string{
		"sampled":     boolLabelO(sampled),
		"search_mode": searchModeOrDefault(searchMode),
	}, 1)
	r.metrics.Observe("obs_trace_duration_seconds", map[string]string{
		"search_mode": searchModeOrDefault(searchMode),
		"status":      string(endStatus),
	}, dur.Seconds(), nil)
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
