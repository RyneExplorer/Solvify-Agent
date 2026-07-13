package service

import (
	"context"

	requestdto "solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
)

// ChatServiceInterface 定义聊天服务接口
type ChatServiceInterface interface {
	CreateSession(ctx context.Context, userID string, req requestdto.CreateSessionRequest) (dto.SessionResponse, error)
	GetSession(ctx context.Context, userID, sessionID string) (dto.SessionResponse, error)
	ListSessions(ctx context.Context, userID string) ([]dto.SessionResponse, error)
	UpdateSessionTitle(ctx context.Context, userID, sessionID string, req requestdto.UpdateSessionRequest) error
	DeleteSession(ctx context.Context, userID, sessionID string) error
	SendMessage(ctx context.Context, userID, sessionID string, req requestdto.SendMessageRequest) (<-chan dto.StreamEvent, error)
	GetMessages(ctx context.Context, userID, sessionID string) ([]dto.MessageResponse, error)
}
