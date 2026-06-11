package repository

import (
	"context"

	"gorm.io/gorm"

	"solvify-agent/internal/model/entity"
)

// chatSessionRepository 提供聊天会话数据访问实现
type chatSessionRepository struct {
	db *gorm.DB
}

// NewChatSessionRepository 创建聊天会话仓库
func NewChatSessionRepository(db *gorm.DB) ChatSessionRepo {
	return &chatSessionRepository{db: db}
}

// Create 创建聊天会话
func (r *chatSessionRepository) Create(ctx context.Context, session *entity.ChatSession) error {
	return r.db.WithContext(ctx).Create(session).Error
}

// FindByID 根据 ID 获取聊天会话
func (r *chatSessionRepository) FindByID(ctx context.Context, id string) (*entity.ChatSession, error) {
	var session entity.ChatSession
	err := r.db.WithContext(ctx).
		Where("id = ?", id).
		First(&session).Error
	if err != nil {
		return nil, err
	}
	return &session, nil
}

// ListByUserID 获取用户的所有聊天会话
func (r *chatSessionRepository) ListByUserID(ctx context.Context, userID string) ([]entity.ChatSession, error) {
	var sessions []entity.ChatSession
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("updated_at DESC").
		Find(&sessions).Error
	return sessions, err
}

// UpdateTitle 更新会话标题
func (r *chatSessionRepository) UpdateTitle(ctx context.Context, id string, title string) error {
	return r.db.WithContext(ctx).
		Model(&entity.ChatSession{}).
		Where("id = ?", id).
		Update("title", title).Error
}

// Delete 删除会话
func (r *chatSessionRepository) Delete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&entity.ChatSession{}).Error
}
