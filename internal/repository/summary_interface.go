package repository

import (
	"context"

	"solvify-agent/internal/model/entity"
)

// SummaryRepo 定义会话摘要数据访问接口
type SummaryRepo interface {
	// GetBySessionID 获取会话摘要
	GetBySessionID(ctx context.Context, sessionID string) (*entity.ChatSummary, error)
	// Upsert 更新或创建摘要
	Upsert(ctx context.Context, summary *entity.ChatSummary) error
}
