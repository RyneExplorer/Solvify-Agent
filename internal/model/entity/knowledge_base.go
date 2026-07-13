package entity

import "time"

// KnowledgeBase 映射知识库主表
type KnowledgeBase struct {
	ID              string     `gorm:"column:id;type:uuid;default:gen_random_uuid();primaryKey"`
	UserID          string     `gorm:"column:user_id;type:uuid;not null"`
	Name            string     `gorm:"column:name;type:varchar(128);not null"`
	Category        string     `gorm:"column:category;type:varchar(128);not null;default:''"`
	Description     string     `gorm:"column:description"`
	SourceType      string     `gorm:"column:source_type;type:varchar(32);not null;default:local"`
	SourcePlatform  string     `gorm:"column:source_platform;type:varchar(255);not null;default:''"`
	DocumentCount   int        `gorm:"column:document_count;not null;default:0"`
	StorageBytes    int64      `gorm:"column:storage_bytes;not null;default:0"`
	Status          int        `gorm:"column:status;not null;default:1"`
	CreatedAt       time.Time  `gorm:"column:created_at"`
	UpdatedAt       time.Time  `gorm:"column:updated_at"`
	DeletedAt       *time.Time `gorm:"column:deleted_at"`
	DeleteExpiredAt *time.Time `gorm:"column:delete_expired_at"`
}

// TableName 返回知识库表名
func (KnowledgeBase) TableName() string {
	return "knowledge_bases"
}
