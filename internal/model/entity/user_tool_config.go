package entity

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// UserToolConfig 用户工具配置
type UserToolConfig struct {
	ID          string         `gorm:"type:varchar(36);primaryKey" json:"id"`
	UserID      string         `gorm:"type:varchar(36);not null;index" json:"user_id"`
	ToolTypeID  string         `gorm:"type:varchar(36);not null" json:"tool_type_id"`
	ProviderID  string         `gorm:"type:varchar(36);not null" json:"provider_id"`
	DisplayName string         `gorm:"type:varchar(100)" json:"display_name"`
	Config      datatypes.JSON `gorm:"type:jsonb;not null" json:"config"` // 用户填写的参数
	IsEnabled   bool           `gorm:"default:true" json:"is_enabled"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`

	ToolType     ToolType     `gorm:"foreignKey:ToolTypeID" json:"tool_type,omitempty"`
	ToolProvider ToolProvider `gorm:"foreignKey:ProviderID" json:"tool_provider,omitempty"`
}

func (c *UserToolConfig) BeforeCreate(tx *gorm.DB) error {
	if c.ID == "" {
		c.ID = uuid.New().String()
	}
	return nil
}

func (UserToolConfig) TableName() string {
	return "user_tool_configs"
}
