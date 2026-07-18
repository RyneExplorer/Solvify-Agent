package entity

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// ChatSummary 会话摘要：保存单一会话的摘要，替代早期原始消息
type ChatSummary struct {
	ID            string  `gorm:"type:uuid;primaryKey"`
	SessionID     string  `gorm:"type:uuid;not null;uniqueIndex"`
	Summary       string  `gorm:"type:text;not null"`
	CoveredCount  int     `gorm:"default:0"`
	LastMessageID *string `gorm:"type:uuid"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// BeforeCreate 在创建前自动生成 UUID
func (s *ChatSummary) BeforeCreate(tx *gorm.DB) error {
	if s.ID == "" {
		s.ID = uuid.New().String()
	}
	return nil
}

// TableName 返回表名
func (ChatSummary) TableName() string {
	return "chat_summaries"
}
