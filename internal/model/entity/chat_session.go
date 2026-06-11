package entity

import "time"

// ChatSession 映射聊天会话表
type ChatSession struct {
	ID        string    `gorm:"column:id;type:uuid;default:gen_random_uuid();primaryKey"`
	UserID    string    `gorm:"column:user_id;type:uuid;not null"`
	Title     string    `gorm:"column:title;size:200;not null;default:''"`
	ModelID   string    `gorm:"column:model_id;type:varchar(36);not null"`
	Status    string    `gorm:"column:status;type:varchar(20);not null;default:'active'"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt time.Time `gorm:"column:updated_at;autoUpdateTime"`
}

// TableName 返回聊天会话表名
func (ChatSession) TableName() string {
	return "chat_sessions"
}
