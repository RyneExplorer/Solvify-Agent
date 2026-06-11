package repository

import (
	"context"

	"solvify-agent/internal/model/entity"
)

// ChatSessionRepo 定义聊天会话数据访问接口
type ChatSessionRepo interface {
	Create(ctx context.Context, session *entity.ChatSession) error
	FindByID(ctx context.Context, id string) (*entity.ChatSession, error)
	ListByUserID(ctx context.Context, userID string) ([]entity.ChatSession, error)
	UpdateTitle(ctx context.Context, id string, title string) error
	Delete(ctx context.Context, id string) error
}
