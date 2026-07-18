package repository

import (
	"context"

	"gorm.io/gorm"

	"solvify-agent/internal/model/entity"
)

// summaryRepository 提供会话摘要数据访问实现
type summaryRepository struct {
	db *gorm.DB
}

// NewSummaryRepository 创建摘要仓库
func NewSummaryRepository(db *gorm.DB) SummaryRepo {
	return &summaryRepository{db: db}
}

// GetBySessionID 获取会话摘要
func (r *summaryRepository) GetBySessionID(ctx context.Context, sessionID string) (*entity.ChatSummary, error) {
	var summary entity.ChatSummary
	err := r.db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		First(&summary).Error
	if err == gorm.ErrRecordNotFound {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &summary, nil
}

// Upsert 更新或创建摘要
func (r *summaryRepository) Upsert(ctx context.Context, summary *entity.ChatSummary) error {
	return r.db.WithContext(ctx).
		Where("session_id = ?", summary.SessionID).
		Assign(map[string]interface{}{
			"summary":         summary.Summary,
			"covered_count":   summary.CoveredCount,
			"last_message_id": summary.LastMessageID,
		}).
		FirstOrCreate(summary).Error
}
