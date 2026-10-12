package repository

import (
	"context"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"solvify-agent/internal/model/entity"
)

type feedbackRepository struct {
	db *gorm.DB
}

// NewFeedbackRepository 创建消息反馈仓库
func NewFeedbackRepository(db *gorm.DB) FeedbackRepo {
	return &feedbackRepository{db: db}
}

// CreateFeedback 创建消息反馈记录
func (r *feedbackRepository) CreateFeedback(ctx context.Context, fb *entity.MessageFeedback) error {
	if fb.ID == "" {
		fb.ID = uuid.New().String()
	}
	if fb.CreatedAt.IsZero() {
		fb.CreatedAt = time.Now()
	}
	return dbFor(ctx, r.db).Create(fb).Error
}

// ListByMessage 按消息 ID 和用户 ID 查询反馈列表
func (r *feedbackRepository) ListByMessage(ctx context.Context, messageID, userID string) ([]entity.MessageFeedback, error) {
	var rows []entity.MessageFeedback
	err := dbFor(ctx, r.db).
		Where("message_id = ? AND user_id = ?", messageID, userID).
		Order("created_at DESC, id").
		Find(&rows).Error
	return rows, err
}

// ListByUser 分页查询指定用户的反馈列表
func (r *feedbackRepository) ListByUser(ctx context.Context, userID string, offset, limit int) ([]entity.MessageFeedback, int64, error) {
	var total int64
	q := dbFor(ctx, r.db).Model(&entity.MessageFeedback{}).Where("user_id = ?", userID)
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []entity.MessageFeedback
	err := q.Order("created_at DESC, id").Offset(offset).Limit(limit).Find(&rows).Error
	return rows, total, err
}
