package observability

import (
	"context"
	"time"
)

type SpanStatus string

const (
	SpanStatusOK       SpanStatus = "ok"
	SpanStatusError    SpanStatus = "error"
	SpanStatusCanceled SpanStatus = "canceled"
)

type Component string

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

type Attrs map[string]any

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

type SpanEvent struct {
	Name      string    `json:"name"`
	Timestamp time.Time `json:"timestamp"`
	Attrs     Attrs     `json:"attrs,omitempty"`
}

type Span struct {
	TraceID    string       `json:"trace_id"`
	SpanID     string       `json:"span_id"`
	ParentID   string       `json:"parent_id,omitempty"`
	Name       string       `json:"name"`
	Component  Component    `json:"component"`
	StartAt    time.Time    `json:"start_at"`
	EndAt      time.Time    `json:"end_at,omitempty"`
	DurationMs int64        `json:"duration_ms,omitempty"`
	Status     SpanStatus   `json:"status"`
	Error      string       `json:"error,omitempty"`
	Attrs      Attrs        `json:"attrs,omitempty"`
	Events     []*SpanEvent `json:"events,omitempty"`
	Children   []*Span      `json:"children,omitempty"`
}

type Trace struct {
	ID         string  `json:"id"`
	RequestID  string  `json:"request_id,omitempty"`
	UserID     string  `json:"user_id,omitempty"`
	SessionID  string  `json:"session_id,omitempty"`
	Root       *Span   `json:"root"`
	SampleRate float64 `json:"sample_rate,omitempty"`
	Sampled    bool    `json:"sampled"`
}

type Feedback struct {
	MessageID   string    `json:"message_id"`
	UserID      string    `json:"user_id"`
	SessionID   string    `json:"session_id,omitempty"`
	Rating      int       `json:"rating"`
	ReasonTag   string    `json:"reason_tag,omitempty"`
	Comment     string    `json:"comment,omitempty"`
	TraceID     string    `json:"trace_id,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

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
	LatencyMs         int64     `json:"latency_ms,omitempty"`
	TokensDelta       int       `json:"tokens_delta,omitempty"`
}

type SinkRecord struct {
	Kind      string      `json:"kind"`
	Timestamp time.Time   `json:"timestamp"`
	Trace     *Trace      `json:"trace,omitempty"`
	Feedback  *Feedback   `json:"feedback,omitempty"`
	AgentStep *AgentStep  `json:"agent_step,omitempty"`
	Attrs     Attrs       `json:"attrs,omitempty"`
}

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
}

type TraceRootAttrs struct {
	UserID     string
	SessionID  string
	MessageID  string
	RequestID  string
	SearchMode string
	ModelID    string
}
