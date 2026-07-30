package repository

import (
	"context"

	"gorm.io/gorm"

	"solvify-agent/internal/model/entity"
)

// chatMessageRepository 提供聊天消息数据访问实现
type chatMessageRepository struct {
	db *gorm.DB
}

// NewChatMessageRepository 创建聊天消息仓库
func NewChatMessageRepository(db *gorm.DB) ChatMessageRepo {
	return &chatMessageRepository{db: db}
}

// Create 创建聊天消息
func (r *chatMessageRepository) Create(ctx context.Context, message *entity.ChatMessage) error {
	return r.db.WithContext(ctx).Create(message).Error
}

// FindByID 按 ID 获取消息
func (r *chatMessageRepository) FindByID(ctx context.Context, id string) (*entity.ChatMessage, error) {
	var msg entity.ChatMessage
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&msg).Error; err != nil {
		return nil, err
	}
	return &msg, nil
}

// FindBySessionID 获取会话的所有消息
func (r *chatMessageRepository) FindBySessionID(ctx context.Context, sessionID string) ([]entity.ChatMessage, error) {
	var messages []entity.ChatMessage
	err := r.db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		Order("created_at ASC").
		Find(&messages).Error
	return messages, err
}

// FindBySessionIDForContext 会话摘要/记忆抽取场景专用：
// 全量消息，但只 SELECT 构建 Prompt 需要的 5 个字段，sources/metadata 不传
// 对 50 轮以上长会话可减少 90%+ 的数据传输
func (r *chatMessageRepository) FindBySessionIDForContext(ctx context.Context, sessionID string) ([]entity.ChatMessage, error) {
	var messages []entity.ChatMessage
	err := r.db.WithContext(ctx).
		Select("id, session_id, role, content, created_at").
		Where("session_id = ?", sessionID).
		Order("created_at ASC").
		Find(&messages).Error
	return messages, err
}

// FindRecent 获取会话的最近 N 条消息
func (r *chatMessageRepository) FindRecent(ctx context.Context, sessionID string, limit int) ([]entity.ChatMessage, error) {
	var messages []entity.ChatMessage
	err := r.db.WithContext(ctx).
		Where("session_id = ?", sessionID).
		Order("created_at DESC").
		Limit(limit).
		Find(&messages).Error

	// 反转顺序，使其按时间正序
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	return messages, err
}

// FindRecentForContext 上下文构建专用：只取构建 Prompt 必需的 5 个字段
// 避免 SELECT * 把 sources/metadata 两个 JSON 大字段也传回来（可能 > 100KB/条），
// 构建上下文只看 role+content，单条消息体积从 100KB 降到 ~100Byte
func (r *chatMessageRepository) FindRecentForContext(ctx context.Context, sessionID string, limit int) ([]entity.ChatMessage, error) {
	var messages []entity.ChatMessage
	err := r.db.WithContext(ctx).
		Select("id, session_id, role, content, created_at").
		Where("session_id = ?", sessionID).
		Order("created_at DESC").
		Limit(limit).
		Find(&messages).Error

	// 反转顺序，使其按时间正序
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	return messages, err
}

// DeleteBySessionID 删除会话的所有消息
func (r *chatMessageRepository) DeleteBySessionID(ctx context.Context, sessionID string) error {
	return r.db.WithContext(ctx).Where("session_id = ?", sessionID).Delete(&entity.ChatMessage{}).Error
}

// SearchRecentByKeywords 在指定会话中按关键词检索最近消息
func (r *chatMessageRepository) SearchRecentByKeywords(ctx context.Context, sessionID string, keywords []string, limit int) ([]entity.ChatMessage, error) {
	if limit <= 0 {
		limit = 5
	}
	if len(keywords) == 0 {
		return r.FindRecent(ctx, sessionID, limit)
	}

	query := r.db.WithContext(ctx).
		Where("session_id = ?", sessionID)

	// 多个关键词用 OR 连接，任意匹配即可
	for _, kw := range keywords {
		if kw == "" {
			continue
		}
		query = query.Or("content ILIKE ?", "%"+kw+"%")
	}

	var messages []entity.ChatMessage
	err := query.
		Order("created_at DESC").
		Limit(limit).
		Find(&messages).Error

	// 反转顺序，使其按时间正序
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	return messages, err
}

// SearchByKeyword 按关键字搜索用户历史消息
func (r *chatMessageRepository) SearchByKeyword(ctx context.Context, userID, query string, topK int) ([]ChatMessageSearchRow, error) {
	if topK <= 0 {
		topK = 10
	}

	keyword := "%" + query + "%"
	var results []ChatMessageSearchRow
	err := r.db.WithContext(ctx).Raw(`
		SELECT m.id, m.session_id, s.title as session_title, m.role, m.content, 1.0 AS score, m.created_at
		FROM chat_messages m
		JOIN chat_sessions s ON s.id = m.session_id
		WHERE s.user_id = ?
		  AND m.content ILIKE ?
		ORDER BY m.created_at DESC
		LIMIT ?
	`, userID, keyword, topK).Scan(&results).Error

	return results, err
}
