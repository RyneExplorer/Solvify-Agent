package entity

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// UserMemory 用户长期记忆：保存跨会话的事实、偏好、约束和决策结论
type UserMemory struct {
	ID            string  `gorm:"type:uuid;primaryKey"`
	UserID        string  `gorm:"type:uuid;not null"`
	MemoryType    string  `gorm:"type:varchar(30);not null"` // fact / preference / constraint / decision
	Content       string  `gorm:"type:text;not null"`
	SourceSession *string `gorm:"type:uuid"`
	Confidence    float64 `gorm:"type:float;default:1.0"`
	IsActive      bool    `gorm:"default:true"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// BeforeCreate 在创建前自动生成 UUID
func (m *UserMemory) BeforeCreate(tx *gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.New().String()
	}
	return nil
}

// TableName 返回表名
func (UserMemory) TableName() string {
	return "user_memories"
}
