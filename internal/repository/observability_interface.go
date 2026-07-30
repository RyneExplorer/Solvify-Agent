package repository

import (
	"context"
	"time"

	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/observability"
)

type FeedbackRepo interface {
	CreateFeedback(ctx context.Context, fb *entity.MessageFeedback) error
	ListByMessage(ctx context.Context, messageID, userID string) ([]entity.MessageFeedback, error)
	ListByUser(ctx context.Context, userID string, offset, limit int) ([]entity.MessageFeedback, int64, error)
}

type ChatTraceRepo interface {
	CreateChatTrace(ctx context.Context, trace *entity.ChatTrace) error
	FindByID(ctx context.Context, id string) (*entity.ChatTrace, error)
	ListBySession(ctx context.Context, sessionID, userID string, offset, limit int) ([]entity.ChatTrace, int64, error)
	DeleteOlderThan(ctx context.Context, before time.Time) (int64, error)
}

type AgentTaskRepo interface {
	CreateAgentTask(ctx context.Context, task *entity.AgentTask) error
	AppendStep(ctx context.Context, step *entity.AgentTaskStep) error
	MarkEnded(ctx context.Context, taskID string, status, abortReason, errorSummary string, tokensPrompt, tokensCompletion int, cost float64, rating *int) error
	FindByTraceID(ctx context.Context, traceID string) (*entity.AgentTask, []entity.AgentTaskStep, error)
}

type ObservabilityRepo interface {
	observability.DBSink
	FeedbackRepo
	ChatTraceRepo
	AgentTaskRepo
}
