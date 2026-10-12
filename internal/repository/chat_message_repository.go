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
	return dbFor(ctx, r.db).Create(message).Error
}

// FindByID 按 ID 获取消息
func (r *chatMessageRepository) FindByID(ctx context.Context, id string) (*entity.ChatMessage, error) {
	var msg entity.ChatMessage
	if err := dbFor(ctx, r.db).Where("id = ?", id).First(&msg).Error; err != nil {
		return nil, err
	}
	return &msg, nil
}

// FindBySessionID 获取会话的所有消息
func (r *chatMessageRepository) FindBySessionID(ctx context.Context, sessionID string) ([]entity.ChatMessage, error) {
	var messages []entity.ChatMessage
	err := dbFor(ctx, r.db).
		Where("session_id = ?", sessionID).
		Order("created_at ASC, id").
		Find(&messages).Error
	return messages, err
}

// FindBySessionIDForContext 会话摘要/记忆抽取场景专用：
// 全量消息，但只 SELECT 构建 Prompt 需要的 5 个字段，sources/metadata 不传
// 对 50 轮以上长会话可减少 90%+ 的数据传输
func (r *chatMessageRepository) FindBySessionIDForContext(ctx context.Context, sessionID string) ([]entity.ChatMessage, error) {
	var messages []entity.ChatMessage
	err := dbFor(ctx, r.db).
		Select("id, session_id, role, content, created_at").
		Where("session_id = ?", sessionID).
		Order("created_at ASC, id").
		Find(&messages).Error
	return messages, err
}

// FindRecent 获取会话的最近 N 条消息
func (r *chatMessageRepository) FindRecent(ctx context.Context, sessionID string, limit int) ([]entity.ChatMessage, error) {
	var messages []entity.ChatMessage
	err := dbFor(ctx, r.db).
		Where("session_id = ?", sessionID).
		Order("created_at DESC, id").
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
	err := dbFor(ctx, r.db).
		Select("id, session_id, role, content, created_at").
		Where("session_id = ?", sessionID).
		Order("created_at DESC, id").
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
	return dbFor(ctx, r.db).Where("session_id = ?", sessionID).Delete(&entity.ChatMessage{}).Error
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
	query := dbFor(ctx, r.db).Where("session_id = ?", sessionID)

	if len(keywords) == 1 {
		query = query.Where("content ILIKE ?", "%"+keywords[0]+"%")
	} else {
		sub := dbFor(ctx, r.db).Session(&gorm.Session{NewDB: true})
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
		Order("created_at DESC, id").
		Limit(limit).
		Find(&messages).Error

	// 返回近期匹配排名，展示时间顺序由装配层处理。

	return messages, err
}

// SearchByKeyword 按关键字搜索用户历史消息
func (r *chatMessageRepository) SearchByKeyword(ctx context.Context, userID, query string, topK int) ([]ChatMessageSearchRow, error) {
	if topK <= 0 {
		topK = 10
	}

	keyword := "%" + query + "%"
	var results []ChatMessageSearchRow
	err := dbFor(ctx, r.db).Raw(`
		SELECT m.id, m.session_id, s.title as session_title, m.role, m.content, 1.0 AS score, m.created_at
		FROM chat_messages m
		JOIN chat_sessions s ON s.id = m.session_id
		WHERE s.user_id = ?
		  AND m.content ILIKE ?
		ORDER BY m.created_at DESC, m.id
		LIMIT ?
	`, userID, keyword, topK).Scan(&results).Error

	return results, err
}

// chatMessageVectorDistanceThreshold 是「相关历史」语义召回的余弦距离上限。
//
// 与知识库检索侧同源，不要另拍数字：hybrid_retriever 的 score = 1 - 余弦距离，
// 默认 scoreThreshold = 0.5（internal/rag/hybrid_retriever.go:59-62），
// 即「相似度 ≥ 0.5」⇔「距离 ≤ 0.5」。
// ⚠️ 原来的 0.8 是按 L2 距离定的；度量从 <-> 换成 <=> 之后它会放进大量噪声。
const chatMessageVectorDistanceThreshold = 0.5

// SearchRecentByVector 在指定会话中按向量语义检索最近消息（pgvector 余弦距离 <=>）。
// 仅返回有 embedding 的消息（embedding IS NOT NULL），距离阈值用于过滤不相关结果。
func (r *chatMessageRepository) SearchRecentByVector(ctx context.Context, sessionID string, queryEmbedding []float32, limit int, distanceThreshold float64) ([]entity.ChatMessage, error) {
	if limit <= 0 {
		limit = 5
	}
	if len(queryEmbedding) == 0 {
		return nil, nil
	}
	if distanceThreshold <= 0 {
		distanceThreshold = chatMessageVectorDistanceThreshold
	}

	embeddingStr := formatFloatVector(queryEmbedding)

	var messages []entity.ChatMessage
	err := dbFor(ctx, r.db).Raw(`
		SELECT id, session_id, role, content, model_id, search_mode, knowledge_base_ids, sources, metadata, embedding, created_at
		FROM chat_messages
		WHERE session_id = ?
		  AND embedding IS NOT NULL
		  AND embedding <=> ? <= ?
		ORDER BY embedding <=> ?, id
		LIMIT ?
	`, sessionID, embeddingStr, distanceThreshold, embeddingStr, limit).Scan(&messages).Error

	if err != nil {
		return nil, err
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
	return dbFor(ctx, r.db).Model(&entity.ChatMessage{}).
		Where("id = ?", messageID).
		Update("embedding", embedding).Error
}

// FindMessagesMissingEmbedding 分批取「还没有向量」的消息，供一次性回填任务用。
//
// 判据是 embedding IS NULL ⇒ 写完即离开集合，回填天然幂等、中断后从头再来也不会重复写。
// 翻页用 keyset（id > afterID ORDER BY id）而不是 OFFSET：回填过程中集合在缩小，
// OFFSET 会漏行；id 是全序（末键就是 id），keyset 不会。
//
// ⚠️ afterID 必须用「nil 表示从头」而不是空串：id 是 uuid 列，
// `id > ''` 会被 PostgreSQL 判为 `invalid input syntax for type uuid: ""`（SQLSTATE 22P02）——
// 首屏（空游标）直接报错，整轮回填一条都跑不了。
// 这个 bug 是真库探针跑出来的：单测里替身做的是字符串比较，压根不认 uuid 类型。
func (r *chatMessageRepository) FindMessagesMissingEmbedding(ctx context.Context, afterID string, limit int) ([]entity.ChatMessage, error) {
	if limit <= 0 {
		limit = 200
	}
	q := dbFor(ctx, r.db).
		Model(&entity.ChatMessage{}).
		Select("id", "session_id", "role", "content", "created_at").
		Where("embedding IS NULL").
		Where("content <> ''").
		Order("id").
		Limit(limit)
	if afterID != "" {
		q = q.Where("id > ?", afterID)
	}

	var rows []entity.ChatMessage
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// BatchUpdateEmbedding 一批一次往返写完向量。
//
// 单条 `UPDATE ... FROM (VALUES ...)` 而不是循环 Update：回填上千条时逐条往返
// 差两个数量级；单条语句还天然原子，不必再套事务。
func (r *chatMessageRepository) BatchUpdateEmbedding(ctx context.Context, items []MessageEmbeddingUpdate) error {
	if len(items) == 0 {
		return nil
	}
	var (
		sb   strings.Builder
		args = make([]any, 0, len(items)*2)
	)
	sb.WriteString(`UPDATE chat_messages AS m SET embedding = v.vec::vector FROM (VALUES `)
	for i, it := range items {
		if i > 0 {
			sb.WriteString(", ")
		}
		fmt.Fprintf(&sb, "($%d::uuid, $%d::text)", i*2+1, i*2+2)
		vec, err := it.Embedding.Value()
		if err != nil {
			return fmt.Errorf("消息 %s 的向量序列化失败: %w", it.ID, err)
		}
		args = append(args, it.ID, vec)
	}
	sb.WriteString(`) AS v(id, vec) WHERE m.id = v.id`)
	return dbFor(ctx, r.db).Exec(sb.String(), args...).Error
}
