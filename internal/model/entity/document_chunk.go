package entity

import (
	"database/sql/driver"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/datatypes"
)

// TextArray 映射 PostgreSQL text[] 字段
type TextArray []string

// FloatVector 映射 PostgreSQL pgvector 字段
type FloatVector []float32

// Value 转换为 pgvector 字面量
func (v FloatVector) Value() (driver.Value, error) {
	if len(v) == 0 {
		return nil, nil
	}

	items := make([]string, 0, len(v))
	for _, item := range v {
		items = append(items, strconv.FormatFloat(float64(item), 'f', -1, 32))
	}
	return "[" + strings.Join(items, ",") + "]", nil
}

// Scan 读取 pgvector 字面量
func (v *FloatVector) Scan(value any) error {
	if value == nil {
		*v = nil
		return nil
	}

	var text string
	switch data := value.(type) {
	case string:
		text = data
	case []byte:
		text = string(data)
	default:
		return fmt.Errorf("不支持的 vector 类型: %T", value)
	}

	text = strings.TrimSpace(strings.TrimPrefix(strings.TrimSuffix(text, "]"), "["))
	if text == "" {
		*v = nil
		return nil
	}

	parts := strings.Split(text, ",")
	items := make([]float32, 0, len(parts))
	for _, part := range parts {
		num, err := strconv.ParseFloat(strings.TrimSpace(part), 32)
		if err != nil {
			return fmt.Errorf("解析 vector 失败: %w", err)
		}
		items = append(items, float32(num))
	}
	*v = items
	return nil
}

// GormDataType 返回 pgvector 数据类型
func (FloatVector) GormDataType() string {
	return "vector"
}

// Value 转换为 PostgreSQL 数组字面量
func (v TextArray) Value() (driver.Value, error) {
	if len(v) == 0 {
		return "{}", nil
	}

	items := make([]string, 0, len(v))
	for _, item := range v {
		escaped := strings.ReplaceAll(item, `\`, `\\`)
		escaped = strings.ReplaceAll(escaped, `"`, `\"`)
		items = append(items, `"`+escaped+`"`)
	}
	return "{" + strings.Join(items, ",") + "}", nil
}

// Scan 读取 PostgreSQL 数组字面量
func (v *TextArray) Scan(value any) error {
	if value == nil {
		*v = TextArray{}
		return nil
	}

	var text string
	switch data := value.(type) {
	case string:
		text = data
	case []byte:
		text = string(data)
	default:
		return fmt.Errorf("不支持的 text[] 类型: %T", value)
	}

	text = strings.TrimPrefix(strings.TrimSuffix(text, "}"), "{")
	if text == "" {
		*v = TextArray{}
		return nil
	}

	parts := strings.Split(text, ",")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		items = append(items, strings.Trim(part, `"`))
	}
	*v = items
	return nil
}

// DocumentChunk 映射文档分块表
type DocumentChunk struct {
	ID              string         `gorm:"column:id;type:uuid;default:gen_random_uuid();primaryKey"`
	UserID          string         `gorm:"column:user_id;type:uuid;not null"`
	KnowledgeBaseID string         `gorm:"column:knowledge_base_id;type:uuid;not null"`
	DocumentID      string         `gorm:"column:document_id;type:uuid;not null"`
	VersionID       string         `gorm:"column:version_id;type:uuid;not null"`
	ChunkIndex      int            `gorm:"column:chunk_index;not null"`
	SectionTitle    string         `gorm:"column:section_title;not null;default:''"`
	Content         string         `gorm:"column:content;not null"`
	TokenCount      int            `gorm:"column:token_count;not null;default:0"`
	PageNumber      *int           `gorm:"column:page_number"`
	EmbeddingModel  string         `gorm:"column:embedding_model;size:128;not null;default:''"`
	Embedding       FloatVector    `gorm:"column:embedding;type:vector(1024);-:migration"`
	Keywords        TextArray      `gorm:"column:keywords;type:text[];default:'{}'"`
	Metadata        datatypes.JSON `gorm:"column:metadata;type:jsonb;not null;default:'{}'"`
	CreatedAt       time.Time      `gorm:"column:created_at"`
}

// TableName 返回文档分块表名
func (DocumentChunk) TableName() string {
	return "document_chunks"
}
