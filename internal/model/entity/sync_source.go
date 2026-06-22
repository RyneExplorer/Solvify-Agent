package entity

import (
	"time"

	"gorm.io/datatypes"
)

// SyncSource 映射同步源配置表
type SyncSource struct {
	ID               string         `gorm:"column:id;type:uuid;default:gen_random_uuid();primaryKey"`
	UserID           string         `gorm:"column:user_id;type:uuid;not null"`
	KnowledgeBaseID  string         `gorm:"column:knowledge_base_id;type:uuid;not null"`
	Name             string         `gorm:"column:name;type:varchar(128);not null"`
	Platform         string         `gorm:"column:platform;type:varchar(32);not null"`
	SourceConfig     datatypes.JSON `gorm:"column:source_config;type:jsonb;not null;default:'{}'"`
	Status           int            `gorm:"column:status;not null;default:1"`
	LastSyncAt       *time.Time     `gorm:"column:last_sync_at"`
	LastErrorMessage string         `gorm:"column:last_error_message;default:''"`
	CreatedAt        time.Time      `gorm:"column:created_at"`
	UpdatedAt        time.Time      `gorm:"column:updated_at"`
	DeletedAt        *time.Time     `gorm:"column:deleted_at"`
}

// TableName 返回同步源配置表名
func (SyncSource) TableName() string {
	return "sync_sources"
}
