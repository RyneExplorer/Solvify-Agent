package service

import (
	"context"

	requestdto "solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
)

type FeedbackRequest struct {
	Rating    int    `json:"rating"`
	ReasonTag string `json:"reason_tag"`
	Comment   string `json:"comment"`
}

type FeedbackListResponse struct {
	Total int64 `json:"total"`
	Items any   `json:"items"`
}

type TraceResponse struct {
	ID         string `json:"id"`
	RequestID  string `json:"request_id,omitempty"`
	UserID     string `json:"user_id,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	SampleRate float64 `json:"sample_rate,omitempty"`
	Sampled    bool   `json:"sampled"`
	DurationMs int64  `json:"duration_ms,omitempty"`
	Status     string `json:"status,omitempty"`
	Error      string `json:"error,omitempty"`
	Attrs      any    `json:"attrs,omitempty"`
	SpanTree   any    `json:"span_tree,omitempty"`
	CreatedAt  string `json:"created_at"`
}

type TraceListResponse struct {
	Total int64 `json:"total"`
	Items any   `json:"items"`
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
	GetTrace(ctx context.Context, userID, traceID string) (*TraceResponse, error)
	ListSessionTraces(ctx context.Context, userID, sessionID string, offset, limit int) (TraceListResponse, error)
	GetMetricsSnapshot() (map[string]any, error)
}
