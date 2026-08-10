package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"gorm.io/datatypes"
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

// AdminList 管理员分页查询会话列表
func (r *chatSessionRepository) AdminList(ctx context.Context, offset, limit int, keyword, status string) ([]AdminSessionRow, int64, error) {
	base := r.db.WithContext(ctx).Table("chat_sessions as s").
		Select("s.*, u.username, COUNT(m.id) as message_count").
		Joins("JOIN users u ON s.user_id = u.id").
		Joins("LEFT JOIN chat_messages m ON s.id = m.session_id")

	if keyword != "" {
		base = base.Where("s.title ILIKE ? OR u.username ILIKE ?", "%"+keyword+"%", "%"+keyword+"%")
	}
	if status != "" {
		base = base.Where("s.status = ?", status)
	}

	var total int64
	if err := base.Group("s.id, u.username").Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var rows []AdminSessionRow
	if err := base.Group("s.id, u.username").
		Order("s.updated_at DESC").
		Offset(offset).
		Limit(limit).
		Find(&rows).Error; err != nil {
		return nil, 0, err
	}

	return rows, total, nil
}

// ListExpired 返回指定时间之前未更新的会话 ID 列表
func (r *chatSessionRepository) ListExpired(ctx context.Context, before time.Time) ([]string, error) {
	var ids []string
	err := r.db.WithContext(ctx).
		Model(&entity.ChatSession{}).
		Where("updated_at < ?", before).
		Pluck("id", &ids).Error
	return ids, err
}
func (r *chatSessionRepository) SetPendingClarify(ctx context.Context, id string, data []byte) error {
	return r.db.WithContext(ctx).Model(&entity.ChatSession{}).
		Where("id = ?", id).
		Update("pending_clarify", datatypes.JSON(data)).Error
}

func (r *chatSessionRepository) ClearPendingClarify(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Model(&entity.ChatSession{}).
		Where("id = ?", id).
		Update("pending_clarify", datatypes.JSON("{}")).Error
}

func (r *chatSessionRepository) SetPendingCheckpoint(ctx context.Context, id string, data []byte) error {
	return r.db.WithContext(ctx).Model(&entity.ChatSession{}).
		Where("id = ?", id).
		Update("pending_checkpoint", datatypes.JSON(data)).Error
}

func (r *chatSessionRepository) ClearPendingCheckpoint(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Model(&entity.ChatSession{}).
		Where("id = ?", id).
		Update("pending_checkpoint", datatypes.JSON("{}")).Error
}