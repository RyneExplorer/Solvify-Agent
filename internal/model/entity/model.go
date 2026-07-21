package entity

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Model 描述系统预置 AI 模型配置（所有用户可用）
type Model struct {
	ID               string         `gorm:"type:varchar(36);primaryKey" json:"id"`
	Name             string         `gorm:"type:varchar(100);not null" json:"name"`
	Provider         string         `gorm:"type:varchar(50);not null" json:"provider"`
	ModelID          string         `gorm:"type:varchar(100);not null" json:"model_id"`
	BaseURL          string         `gorm:"type:varchar(500)" json:"base_url,omitempty"`
	APIKey           string         `gorm:"type:varchar(500)" json:"api_key,omitempty"`
	IsEnabled        bool           `gorm:"default:true" json:"is_enabled"`
	Config           datatypes.JSON `gorm:"type:jsonb" json:"config,omitempty"`
	MaxContextLength int            `gorm:"default:8192" json:"max_context_length"`
	CreatedAt        time.Time      `gorm:"autoCreateTime" json:"created_at"`
	UpdatedAt        time.Time      `gorm:"autoUpdateTime" json:"updated_at"`
}

// BeforeCreate 在创建前生成 UUID
func (m *Model) BeforeCreate(tx *gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.New().String()
	}
	return nil
}

// TableName 返回表名
func (*Model) TableName() string {
	return "models"
}
