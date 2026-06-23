package repository

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"

	"solvify-agent/internal/model/entity"
)

// DocumentSearchRow 文档关键字搜索数据库行
type DocumentSearchRow struct {
	ID              string  `gorm:"column:id"`
	KnowledgeBaseID string  `gorm:"column:knowledge_base_id"`
	DocumentID      string  `gorm:"column:document_id"`
	Title           string  `gorm:"column:title"`
	Content         string  `gorm:"column:content"`
	Score           float64 `gorm:"column:score"`
}

// ChunkRepository 定义 chunk 数据访问能力
type ChunkRepository interface {
	FindByID(ctx context.Context, chunkID string) (entity.DocumentChunk, bool, error)
	// SearchByKeyword 按关键字搜索文档内容
	SearchByKeyword(ctx context.Context, userID, query string, topK int) ([]DocumentSearchRow, error)
}

// chunkRepository 封装 chunk GORM 数据访问
type chunkRepository struct {
	db *gorm.DB
}

// NewChunkRepository 创建 chunk 数据仓储
func NewChunkRepository(db *gorm.DB) ChunkRepository {
	return &chunkRepository{db: db}
}

// FindByID 根据 ID 查询 chunk
func (r *chunkRepository) FindByID(ctx context.Context, chunkID string) (entity.DocumentChunk, bool, error) {
	var chunk entity.DocumentChunk
	err := r.db.WithContext(ctx).
		Select("id", "document_id", "content", "section_title").
		Where("id = ?", chunkID).
		First(&chunk).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return entity.DocumentChunk{}, false, nil
	}
	return chunk, err == nil, err
}

// SearchByKeyword 按关键字搜索文档内容
func (r *chunkRepository) SearchByKeyword(ctx context.Context, userID, query string, topK int) ([]DocumentSearchRow, error) {
	keyword := "%" + query + "%"
	keywordArray := buildKeywordArray(query)

	var rows []DocumentSearchRow
	err := r.db.WithContext(ctx).Raw(`
		SELECT dc.id, dc.knowledge_base_id, dc.document_id, COALESCE(d.title, '') as title, dc.content,
			CASE
				WHEN dc.content ILIKE ? THEN 1.0
				WHEN dc.keywords && ?::text[] THEN 0.8
				ELSE 0.0
			END as score
		FROM document_chunks dc
		LEFT JOIN documents d ON d.id = dc.document_id
		WHERE dc.user_id = ?
		  AND (dc.content ILIKE ? OR dc.keywords && ?::text[])
		ORDER BY score DESC, dc.created_at DESC
		LIMIT ?
	`, keyword, keywordArray, userID, keyword, keywordArray, topK).Scan(&rows).Error

	return rows, err
}

// buildKeywordArray 将查询拆分为 PostgreSQL text[] 字面量
func buildKeywordArray(query string) string {
	words := strings.Fields(query)
	if len(words) == 0 {
		return "{}"
	}
	var sb strings.Builder
	sb.WriteString("{")
	for i, w := range words {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString("\"")
		sb.WriteString(strings.ReplaceAll(w, "\"", "\\\""))
		sb.WriteString("\"")
	}
	sb.WriteString("}")
	return sb.String()
}
