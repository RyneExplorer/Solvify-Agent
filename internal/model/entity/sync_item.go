package entity

import "time"

// SyncItem 映射外部同步文件目录项表
type SyncItem struct {
	ID               string     `gorm:"column:id;type:uuid;default:gen_random_uuid();primaryKey"`
	UserID           string     `gorm:"column:user_id;type:uuid;not null"`
	SyncSourceID     string     `gorm:"column:sync_source_id;type:uuid;not null"`
	KnowledgeBaseID  string     `gorm:"column:knowledge_base_id;type:uuid;not null"`
	ExternalID       string     `gorm:"column:external_id;type:varchar(255);not null"`
	ParentExternalID string     `gorm:"column:parent_external_id;type:varchar(255);not null;default:''"`
	Name             string     `gorm:"column:name;type:varchar(255);not null"`
	ItemType         string     `gorm:"column:item_type;type:varchar(32);not null"`
	Category         string     `gorm:"column:category;type:varchar(64);not null;default:''"`
	Extension        string     `gorm:"column:extension;type:varchar(32);not null;default:''"`
	ExternalURL      string     `gorm:"column:external_url;not null;default:''"`
	FileSize         int64      `gorm:"column:file_size;not null;default:0"`
	HasChildren      bool       `gorm:"column:has_children;not null;default:false"`
	SourceUpdatedAt  *time.Time `gorm:"column:source_updated_at"`
	LocalDocumentID  *string    `gorm:"column:local_document_id;type:uuid"`
	ImportStatus     int        `gorm:"column:import_status;not null;default:1"`
	ErrorMessage     string     `gorm:"column:error_message;not null;default:''"`
	CreatedAt        time.Time  `gorm:"column:created_at"`
	UpdatedAt        time.Time  `gorm:"column:updated_at"`
}

// TableName 返回外部同步文件目录项表名
func (SyncItem) TableName() string {
	return "sync_items"
}
