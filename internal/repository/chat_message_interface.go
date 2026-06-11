package repository

import (
	"context"

	"solvify-agent/internal/model/entity"
)

// ChatMessageRepo 定义聊天消息数据访问接口
type ChatMessageRepo interface {
	Create(ctx context.Context, message *entity.ChatMessage) error
	FindBySessionID(ctx context.Context, sessionID string) ([]entity.ChatMessage, error)
	FindRecent(ctx context.Context, sessionID string, limit int) ([]entity.ChatMessage, error)
	DeleteBySessionID(ctx context.Context, sessionID string) error
}
