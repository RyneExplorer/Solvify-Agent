package service

import (
	"context"

	requestdto "solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
)

type FeedbackRequest struct {
	Rating    int      `json:"rating"`
	Reasons   []string `json:"reasons"`
	Comment   string   `json:"comment"`
	IsQuick   bool     `json:"is_quick_reply"`
}

type FeedbackListResponse struct {
	Total     int64 `json:"total"`
	Feedbacks any   `json:"feedbacks"`
}

type TraceAgentTaskResponse struct {
	ID                string  `json:"id"`
	TraceID           string  `json:"trace_id,omitempty"`
	SessionID         string  `json:"session_id,omitempty"`
	UserID            string  `json:"user_id,omitempty"`
	ModelID           string  `json:"model_id,omitempty"`
	SearchMode        string  `json:"search_mode,omitempty"`
	StartedAt         string  `json:"started_at"`
	EndedAt           string  `json:"ended_at,omitempty"`
	TotalSteps        int     `json:"total_steps,omitempty"`
	ToolCalls         int     `json:"tool_calls,omitempty"`
	Status            string  `json:"status,omitempty"`
	AbortReason       string  `json:"abort_reason,omitempty"`
	TokensPrompt      int     `json:"tokens_prompt,omitempty"`
	TokensCompletion  int     `json:"tokens_completion,omitempty"`
	TotalCost         float64 `json:"total_cost,omitempty"`
	ErrorSummary      string  `json:"error_summary,omitempty"`
}

type TraceAgentStepResponse struct {
	StepIndex         int    `json:"step_index"`
	StartedAt         string `json:"started_at"`
	EndedAt           string `json:"ended_at,omitempty"`
	ThinkingSummary   string `json:"thinking_summary,omitempty"`
	ToolName          string `json:"tool_name,omitempty"`
	ToolInputMasked   string `json:"tool_input_masked,omitempty"`
	ToolResultSummary string `json:"tool_result_summary,omitempty"`
	ToolStatus        string `json:"tool_status,omitempty"`
	ToolError         string `json:"tool_error,omitempty"`
	LatencyMs         int64  `json:"latency_ms,omitempty"`
}

type TraceResponse struct {
	ID         string  `json:"id"`
	RequestID  string  `json:"request_id,omitempty"`
	UserID     string  `json:"user_id,omitempty"`
	SessionID  string  `json:"session_id,omitempty"`
	SearchMode string  `json:"search_mode,omitempty"`
	SampleRate float64 `json:"sample_rate,omitempty"`
	Sampled    bool    `json:"sampled"`
	DurationMs int64   `json:"duration_ms,omitempty"`
	Status     string  `json:"status,omitempty"`
	Error      string  `json:"error,omitempty"`
	Attrs      any     `json:"attrs,omitempty"`
	SpanTree   any     `json:"span_tree,omitempty"`
	AgentTask  *TraceAgentTaskResponse  `json:"agent_task,omitempty"`
	AgentSteps []TraceAgentStepResponse `json:"agent_steps,omitempty"`
	CreatedAt  string  `json:"created_at"`
}

type TraceListResponse struct {
	Total  int64 `json:"total"`
	Traces any   `json:"traces"`
}

// chatAgentTaskEntityToResponse 把 entity.AgentTask 转 TraceAgentTaskResponse
func chatAgentTaskEntityToResponse(t *entity.AgentTask) *TraceAgentTaskResponse {
	if t == nil {
		return nil
	}
	ended := ""
	if t.EndedAt != nil {
		ended = t.EndedAt.Format("2006-01-02 15:04:05")
	}
	return &TraceAgentTaskResponse{
		ID:               t.ID,
		TraceID:          t.TraceID,
		SessionID:        t.SessionID,
		UserID:           t.UserID,
		ModelID:          t.ModelID,
		SearchMode:       t.SearchMode,
		StartedAt:        t.StartedAt.Format("2006-01-02 15:04:05"),
		EndedAt:          ended,
		TotalSteps:       t.TotalSteps,
		ToolCalls:        t.ToolCalls,
		Status:           t.Status,
		AbortReason:      t.AbortReason,
		TokensPrompt:     t.TokensPrompt,
		TokensCompletion: t.TokensCompletion,
		TotalCost:        t.TotalCost,
		ErrorSummary:     t.ErrorSummary,
	}
}

// chatAgentStepEntityToResponse 把 []entity.AgentTaskStep 转 []TraceAgentStepResponse
func chatAgentStepEntityToResponse(steps []entity.AgentTaskStep) []TraceAgentStepResponse {
	if len(steps) == 0 {
		return nil
	}
	out := make([]TraceAgentStepResponse, 0, len(steps))
	for _, s := range steps {
		ended := ""
		if s.EndedAt != nil {
			ended = s.EndedAt.Format("2006-01-02 15:04:05")
		}
		out = append(out, TraceAgentStepResponse{
			StepIndex:         s.StepIndex,
			StartedAt:         s.StartedAt.Format("2006-01-02 15:04:05"),
			EndedAt:           ended,
			ThinkingSummary:   s.ThinkingSummary,
			ToolName:          s.ToolName,
			ToolInputMasked:   s.ToolInputMasked,
			ToolResultSummary: s.ToolResultSummary,
			ToolStatus:        s.ToolStatus,
			ToolError:         s.ToolError,
			LatencyMs:         s.LatencyMs,
		})
	}
	return out
}

// ChatServiceInterface 定义聊天服务接口
type ChatServiceInterface interface {
	CreateSession(ctx context.Context, userID string, req requestdto.CreateSessionRequest) (dto.SessionResponse, error)
	GetSession(ctx context.Context, userID, sessionID string) (dto.SessionResponse, error)
	ListSessions(ctx context.Context, userID string) ([]dto.SessionResponse, error)
	UpdateSessionTitle(ctx context.Context, userID, sessionID string, req requestdto.UpdateSessionRequest) error
	DeleteSession(ctx context.Context, userID, sessionID string) error
	SendMessage(ctx context.Context, userID, sessionID string, req requestdto.SendMessageRequest) (<-chan dto.StreamEvent, error)
	GetMessages(ctx context.Context, userID, sessionID string) ([]dto.MessageResponse, error)
	SubmitFeedback(ctx context.Context, userID, messageID string, req FeedbackRequest) error
	ListFeedbacks(ctx context.Context, userID string, offset, limit int) (FeedbackListResponse, error)
	GetTrace(ctx context.Context, userID, traceID string, isAdmin bool) (*TraceResponse, error)
	ListSessionTraces(ctx context.Context, userID, sessionID string, isAdmin bool, offset, limit int) (TraceListResponse, error)
	AdminListTraces(ctx context.Context, sessionID string, rating int, status string, offset, limit int) (TraceListResponse, error)
	GetMetricsSnapshot() (map[string]any, error)
}
