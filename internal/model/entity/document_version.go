package entity

import "time"

// DocumentVersion 映射文档版本表
type DocumentVersion struct {
	ID            string    `gorm:"column:id;type:uuid;default:gen_random_uuid();primaryKey"`
	UserID        string    `gorm:"column:user_id;type:uuid;not null"`
	DocumentID    string    `gorm:"column:document_id;type:uuid;not null"`
	VersionNo     int       `gorm:"column:version_no;not null;default:1"`
	Content       string    `gorm:"column:content;not null;default:''"`
	ContentHash   string    `gorm:"column:content_hash;size:128;not null;default:''"`
	ChangeSummary string    `gorm:"column:change_summary;not null;default:''"`
	CreatedAt     time.Time `gorm:"column:created_at"`
}

// TableName 返回文档版本表名
func (DocumentVersion) TableName() string {
	return "document_versions"
}
