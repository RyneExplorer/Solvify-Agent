package repository

import (
	"context"
	"fmt"
	"strings"

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

	// 关键：session_id 是硬过滤，多个关键词之间用 OR 连接，但必须整体包在 AND 里。
	// 错误写法：Where(session_id).Or(ILIKE)... → 实际 SQL 是 WHERE session_id = ? OR content ILIKE ?
	//          会匹配到全库所有包含关键词的消息，再 ORDER BY + LIMIT，数据量上来必炸。
	// 正确 SQL：WHERE session_id = ? AND (content ILIKE ? OR content ILIKE ? ...)
	query := r.db.WithContext(ctx).Where("session_id = ?", sessionID)

	if len(keywords) == 1 {
		query = query.Where("content ILIKE ?", "%"+keywords[0]+"%")
	} else {
		sub := r.db.Session(&gorm.Session{NewDB: true})
		for i, kw := range keywords {
			if i == 0 {
				sub = sub.Where("content ILIKE ?", "%"+kw+"%")
			} else {
				sub = sub.Or("content ILIKE ?", "%"+kw+"%")
			}
		}
		query = query.Where(sub)
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

// SearchRecentByVector 在指定会话中按向量语义检索最近消息（pgvector 余弦距离）。
// 仅返回有 embedding 的消息（embedding IS NOT NULL），距离阈值用于过滤不相关结果。
func (r *chatMessageRepository) SearchRecentByVector(ctx context.Context, sessionID string, queryEmbedding []float32, limit int, distanceThreshold float64) ([]entity.ChatMessage, error) {
	if limit <= 0 {
		limit = 5
	}
	if len(queryEmbedding) == 0 {
		return nil, nil
	}
	if distanceThreshold <= 0 {
		distanceThreshold = 0.8
	}

	embeddingStr := formatFloatVector(queryEmbedding)

	var messages []entity.ChatMessage
	err := r.db.WithContext(ctx).Raw(`
		SELECT id, session_id, role, content, model_id, search_mode, knowledge_base_ids, sources, metadata, embedding, created_at
		FROM chat_messages
		WHERE session_id = ?
		  AND embedding IS NOT NULL
		  AND embedding <-> ? <= ?
		ORDER BY embedding <-> ?
		LIMIT ?
	`, sessionID, embeddingStr, distanceThreshold, embeddingStr, limit).Scan(&messages).Error

	if err != nil {
		return nil, err
	}

	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	return messages, nil
}

// formatFloatVector 把 []float32 格式化为 pgvector 字面量 '[1.0,2.0,...]'
func formatFloatVector(v []float32) string {
	if len(v) == 0 {
		return "[]"
	}
	var sb strings.Builder
	sb.WriteByte('[')
	for i, f := range v {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(fmt.Sprintf("%.6f", f))
	}
	sb.WriteByte(']')
	return sb.String()
}

// UpdateEmbedding 更新指定消息的向量表示
func (r *chatMessageRepository) UpdateEmbedding(ctx context.Context, messageID string, embedding entity.FloatVector) error {
	return r.db.WithContext(ctx).Model(&entity.ChatMessage{}).
		Where("id = ?", messageID).
		Update("embedding", embedding).Error
}

