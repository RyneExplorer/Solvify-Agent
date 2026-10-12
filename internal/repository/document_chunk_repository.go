package repository

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"

	"solvify-agent/internal/model/entity"
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
	err := dbFor(ctx, r.db).
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
	err := dbFor(ctx, r.db).Raw(searchByKeywordSQL, keyword, keywordArray, userID, keyword, keywordArray, topK).Scan(&rows).Error

	return rows, err
}

// searchByKeywordSQL 是「关键字搜索」读取 chunk 正文的 SQL。
//
// ⚠️ 它必须拼上 entity.RetrievedChunkVisibilitySQL：这是**第二个**读取 chunk 内容的出口，
// 曾经漏了这条边界 ⇒ 用户已删除的文档仍能被搜到（实测确认，违反"可见性是所有 chunk
// 出口的共同约束"）。边界定义与 rag 侧共用同一个常量，避免出现两个来源。
//
// ⚠️ 排序必须全序：分值是 CASE 出来的三种取值（1.0 / 0.8 / 0.0），同分是常态，
// 所以末尾必须有唯一键 dc.id。
// 这里曾经写的是 `, dc.created_at DESC` —— 看着像第二排序键，实则是**假兜底**：
// 同一篇文档的 chunk 是同一批插入的、created_at 完全相同
// （实测评测库 5 篇文档 × 每篇 3 块，count(DISTINCT created_at) 恒为 1），
// 而「同分并列」最常发生的恰恰就是同一篇文档的块之间。
// 守卫：document_chunk_repository_test.go。
var searchByKeywordSQL = `
		SELECT dc.id, dc.knowledge_base_id, dc.document_id, COALESCE(d.title, '') as title, dc.content,
			CASE
				WHEN dc.content ILIKE ? THEN 1.0
				WHEN dc.keywords && ?::text[] THEN 0.8
				ELSE 0.0
			END as score
		FROM document_chunks dc
		LEFT JOIN documents d ON d.id = dc.document_id
		WHERE dc.user_id = ?
		  AND (dc.content ILIKE ? OR dc.keywords && ?::text[])` + entity.RetrievedChunkVisibilitySQL + `
		ORDER BY score DESC, dc.id
		LIMIT ?`

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
