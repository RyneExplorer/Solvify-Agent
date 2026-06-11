package entity

import (
	"time"

	"gorm.io/datatypes"
)

// DocumentChunk 映射文档分块表
type DocumentChunk struct {
	ID              string         `gorm:"column:id;type:uuid;default:gen_random_uuid();primaryKey"`
	UserID          string         `gorm:"column:user_id;type:uuid;not null"`
	KnowledgeBaseID string         `gorm:"column:knowledge_base_id;type:uuid;not null"`
	DocumentID      string         `gorm:"column:document_id;type:uuid;not null"`
	VersionID       string         `gorm:"column:version_id;type:uuid;not null"`
	ChunkIndex      int            `gorm:"column:chunk_index;not null"`
	SectionTitle    string         `gorm:"column:section_title;not null;default:''"`
	Content         string         `gorm:"column:content;type:text;not null"`
	TokenCount      int            `gorm:"column:token_count;not null;default:0"`
	PageNumber      *int           `gorm:"column:page_number"`
	EmbeddingModel  string         `gorm:"column:embedding_model;size:128;not null;default:''"`
	Embedding       datatypes.JSON `gorm:"column:embedding;type:vector(1024)"`
	Metadata        datatypes.JSON `gorm:"column:metadata;type:jsonb;not null;default:'{}'::jsonb"`
	CreatedAt       time.Time      `gorm:"column:created_at;autoCreateTime"`
}

// TableName 返回文档分块表名
func (DocumentChunk) TableName() string {
	return "document_chunks"
}
