package entity

import "time"

// Document 映射文档主表
type Document struct {
	ID              string     `gorm:"column:id;type:uuid;default:gen_random_uuid();primaryKey"`
	UserID          string     `gorm:"column:user_id;type:uuid;not null"`
	KnowledgeBaseID string     `gorm:"column:knowledge_base_id;type:uuid;not null"`
	Title           string     `gorm:"column:title;type:varchar(255);not null"`
	FileName        string     `gorm:"column:file_name;type:varchar(255);not null"`
	FileType        string     `gorm:"column:file_type;type:varchar(32);not null;default:''"`
	FileSize        int64      `gorm:"column:file_size;not null;default:0"`
	StoragePath     string     `gorm:"column:storage_path;not null;default:''"`
	FileHash        string     `gorm:"column:file_hash;type:varchar(128);not null;default:''"`
	SourceType      string     `gorm:"column:source_type;type:varchar(32);default:upload"`
	ExternalID      string     `gorm:"column:external_id;type:varchar(255);default:''"`
	ExternalURL     string     `gorm:"column:external_url;not null;default:''"`
	SourceUpdatedAt *time.Time `gorm:"column:source_updated_at"`
	Status          int        `gorm:"column:status;not null;default:1"`
	ErrorMessage    string     `gorm:"column:error_message;not null;default:''"`
	ReadyAt         *time.Time `gorm:"column:ready_at"`
	CreatedAt       time.Time  `gorm:"column:created_at"`
	UpdatedAt       time.Time  `gorm:"column:updated_at"`
	DeletedAt       *time.Time `gorm:"column:deleted_at"`
	DeleteExpiredAt *time.Time `gorm:"column:delete_expired_at"`
}

// TableName 返回文档表名
func (Document) TableName() string {
	return "documents"
}
