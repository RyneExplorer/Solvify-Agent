package entity

import "time"

// StorageQuota 映射用户存储配额表
type StorageQuota struct {
	ID               string    `gorm:"column:id;type:uuid;default:gen_random_uuid();primaryKey"`
	UserID           string    `gorm:"column:user_id;type:uuid;not null;"`
	MaxStorageBytes  int64     `gorm:"column:max_storage_bytes;not null;default:10737418240"`
	UsedStorageBytes int64     `gorm:"column:used_storage_bytes;not null;default:0"`
	CreatedAt        time.Time `gorm:"column:created_at"`
	UpdatedAt        time.Time `gorm:"column:updated_at"`
}

// TableName 返回存储配额表名
func (StorageQuota) TableName() string {
	return "storage_quotas"
}
