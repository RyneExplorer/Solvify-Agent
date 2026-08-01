package repository

import (
	"context"
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

// ChunkDetail chunk 详情（含关联文档和知识库信息）
type ChunkDetail struct {
	ID                string `gorm:"column:id"`
	DocumentID        string `gorm:"column:document_id"`
	KnowledgeBaseID   string `gorm:"column:knowledge_base_id"`
	Content           string `gorm:"column:content"`
	SectionTitle      string `gorm:"column:section_title"`
	DocumentTitle     string `gorm:"column:document_title"`
	KnowledgeBaseName string `gorm:"column:knowledge_base_name"`
}

// DocumentChunkRepository 定义 chunk 数据访问能力
type DocumentChunkRepository interface {
	FindByID(ctx context.Context, userID, chunkID string) (ChunkDetail, bool, error)
	// SearchByKeyword 按关键字搜索文档内容
	SearchByKeyword(ctx context.Context, userID, query string, topK int) ([]DocumentSearchRow, error)
}
