package repository

import (
	"context"
	"time"

	"solvify-agent/internal/model/entity"
)

// AdminSessionRow 管理员会话列表行（含用户名与消息数）
type AdminSessionRow struct {
	entity.ChatSession
	Username     string
	MessageCount int64
}

// ChatSessionRepo 定义聊天会话数据访问接口
type ChatSessionRepo interface {
	Create(ctx context.Context, session *entity.ChatSession) error
	FindByID(ctx context.Context, id string) (*entity.ChatSession, error)
	ListByUserID(ctx context.Context, userID string) ([]entity.ChatSession, error)
	UpdateTitle(ctx context.Context, id string, title string) error
	Delete(ctx context.Context, id string) error
	// AdminList 管理员分页查询会话列表，keyword 匹配标题或用户名
	AdminList(ctx context.Context, offset, limit int, keyword, status string) ([]AdminSessionRow, int64, error)
	// ListExpired 返回指定时间之前未更新的会话 ID 列表
	ListExpired(ctx context.Context, before time.Time) ([]string, error)
}
