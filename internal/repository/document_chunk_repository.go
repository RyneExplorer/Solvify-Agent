package repository

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"
)

// documentChunkRepository 封装 chunk GORM 数据访问
type documentChunkRepository struct {
	db *gorm.DB
}

// NewDocumentChunkRepository 创建 chunk 数据仓储
func NewDocumentChunkRepository(db *gorm.DB) DocumentChunkRepository {
	return &documentChunkRepository{db: db}
}

// FindByID 根据 ID 查询当前用户的 chunk（含文档标题和知识库名称）
func (r *documentChunkRepository) FindByID(ctx context.Context, userID, chunkID string) (ChunkDetail, bool, error) {
	var row ChunkDetail
	err := r.db.WithContext(ctx).
		Table("document_chunks dc").
		Select("dc.id, dc.document_id, dc.knowledge_base_id, dc.content, dc.section_title, COALESCE(d.title, '') as document_title, COALESCE(kb.name, '') as knowledge_base_name").
		Joins("LEFT JOIN documents d ON d.id = dc.document_id").
		Joins("LEFT JOIN knowledge_bases kb ON kb.id = dc.knowledge_base_id").
		Where("dc.id = ? AND dc.user_id = ?", chunkID, userID).
		First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ChunkDetail{}, false, nil
	}
	return row, err == nil, err
}

// SearchByKeyword 按关键字搜索文档内容
func (r *documentChunkRepository) SearchByKeyword(ctx context.Context, userID, query string, topK int) ([]DocumentSearchRow, error) {
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
