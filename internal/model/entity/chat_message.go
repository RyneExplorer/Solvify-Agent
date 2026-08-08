package entity

import (
	"time"

	"gorm.io/datatypes"
)

// ChatMessage 映射聊天消息表
type ChatMessage struct {
	ID               string         `gorm:"column:id;type:uuid;default:gen_random_uuid();primaryKey"`
	SessionID        string         `gorm:"column:session_id;type:uuid;not null"`
	Role             string         `gorm:"column:role;type:varchar(20);not null"`
	Content          string         `gorm:"column:content;type:text;not null;default:''"`
	ModelID          string         `gorm:"column:model_id;type:varchar(36)"`
	SearchMode       string         `gorm:"column:search_mode;type:varchar(20);not null;default:'quick'"`
	KnowledgeBaseIDs datatypes.JSON `gorm:"column:knowledge_base_ids;type:jsonb;not null;default:'[]'::jsonb"`
	Sources          datatypes.JSON `gorm:"column:sources;type:jsonb"`
	Metadata         datatypes.JSON `gorm:"column:metadata;type:jsonb;not null;default:'{}'::jsonb"`
	// Embedding 消息内容的向量表示，用于语义相关历史检索
	Embedding  FloatVector `gorm:"column:embedding;type:vector(1024)"`
	CreatedAt  time.Time   `gorm:"column:created_at;autoCreateTime"`
}

// TableName 返回聊天消息表名
func (ChatMessage) TableName() string {
	return "chat_messages"
}
