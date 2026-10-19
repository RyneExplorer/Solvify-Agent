package entity

import "time"

// ContextArtifact 原文不可变，审批和执行状态仍以运行记录为准。
type ContextArtifact struct {
	ID         string `gorm:"primaryKey;type:varchar(64)"`
	UserID     string `gorm:"type:varchar(36);not null;index"`
	SessionID  string `gorm:"type:uuid;not null;index"`
	RunID      string `gorm:"type:varchar(64);not null;index"`
	AgentID    string `gorm:"type:varchar(100);not null"`
	TaskID     string `gorm:"type:varchar(100);not null"`
	ToolCallID string `gorm:"type:text;not null"`
	Content    string `gorm:"type:text;not null"`
	Hash       string `gorm:"type:varchar(64);not null"`
	Bytes      int64  `gorm:"not null"`
	Version    int    `gorm:"not null"`
	CreatedAt  time.Time
	ExpiredAt  time.Time `gorm:"not null;index"`
}

func (ContextArtifact) TableName() string { return "context_artifacts" }
