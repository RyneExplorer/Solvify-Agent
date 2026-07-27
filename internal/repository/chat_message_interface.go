package repository

import (
	"context"
	"solvify-agent/internal/model/entity"
	"time"
)

// ChatMessageSearchRow 消息关键字搜索数据库行
type ChatMessageSearchRow struct {
	ID           string    `gorm:"column:id"`
	SessionID    string    `gorm:"column:session_id"`
	SessionTitle string    `gorm:"column:session_title"`
	Role         string    `gorm:"column:role"`
	Content      string    `gorm:"column:content"`
	Score        float64   `gorm:"column:score"`
	CreatedAt    time.Time `gorm:"column:created_at"`
}

// ChatMessageRepo 定义聊天消息数据访问接口
type ChatMessageRepo interface {
	Create(ctx context.Context, message *entity.ChatMessage) error
	FindBySessionID(ctx context.Context, sessionID string) ([]entity.ChatMessage, error)
	// FindBySessionIDForContext 摘要/记忆抽取场景专用：全量消息但只取 5 个必要字段，sources/metadata 不传
	FindBySessionIDForContext(ctx context.Context, sessionID string) ([]entity.ChatMessage, error)
	FindRecent(ctx context.Context, sessionID string, limit int) ([]entity.ChatMessage, error)
	// FindRecentForContext 上下文构建专用：只取构建 Prompt 需要的 5 个字段，避免 sources/metadata 大字段浪费 IO
	FindRecentForContext(ctx context.Context, sessionID string, limit int) ([]entity.ChatMessage, error)
	DeleteBySessionID(ctx context.Context, sessionID string) error
	// SearchByKeyword 按关键字搜索用户历史消息
	SearchByKeyword(ctx context.Context, userID, query string, topK int) ([]ChatMessageSearchRow, error)
	// SearchRecentByKeywords 在指定会话中按关键词检索最近消息
	SearchRecentByKeywords(ctx context.Context, sessionID string, keywords []string, limit int) ([]entity.ChatMessage, error)
}
