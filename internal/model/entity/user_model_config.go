package entity

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// UserModelConfig 描述用户自定义模型配置
type UserModelConfig struct {
	ID          string         `gorm:"type:varchar(36);primaryKey" json:"id"`
	UserID      string         `gorm:"type:varchar(36);not null;index" json:"user_id"`
	DisplayName string         `gorm:"type:varchar(100);not null" json:"display_name"`
	APIFormat   string         `gorm:"type:varchar(20);not null" json:"api_format"`
	BaseURL     string         `gorm:"type:varchar(500);not null" json:"base_url"`
	ModelID     string         `gorm:"type:varchar(100);not null" json:"model_id"`
	APIKey      string         `gorm:"type:varchar(500)" json:"-"` // 可选，本地模型不需要
	Config      datatypes.JSON `gorm:"type:jsonb" json:"config,omitempty"`
	CreatedAt   time.Time      `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt   time.Time      `gorm:"autoUpdateTime" json:"updated_at"`
}

// BeforeCreate 在创建前生成 UUID
func (m *UserModelConfig) BeforeCreate(tx *gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.New().String()
	}
	return nil
}

// TableName 返回表名
func (*UserModelConfig) TableName() string {
	return "user_model_configs"
}
