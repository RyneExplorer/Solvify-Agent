package entity

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// ToolProvider 供应商实例（管理员配置）
type ToolProvider struct {
	ID             string         `gorm:"type:varchar(36);primaryKey" json:"id"`
	ToolTypeID     string         `gorm:"type:varchar(36);not null;index" json:"tool_type_id"`
	ProviderKey    string         `gorm:"type:varchar(50);not null" json:"provider_key"`
	Name           string         `gorm:"type:varchar(100);not null" json:"name"`
	Description    string         `gorm:"type:text" json:"description"`
	ProviderType   string         `gorm:"type:varchar(20);not null;default:http" json:"provider_type"` // http, mcp, custom
	ConfigSchema   datatypes.JSON `gorm:"type:jsonb" json:"config_schema"`                             // 用户配置表单 Schema (JSON Schema)
	InputSchema    datatypes.JSON `gorm:"type:jsonb" json:"input_schema"`                              // Agent 调用参数 Schema
	ProviderConfig datatypes.JSON `gorm:"type:jsonb" json:"provider_config"`                           // 供应商配置（HTTP 配置等）
	AdminConfig    datatypes.JSON `gorm:"type:jsonb" json:"admin_config"`                              // 管理员业务参数
	RateLimit      datatypes.JSON `gorm:"type:jsonb" json:"rate_limit"`                                // 限流配置
	IsEnabled      bool           `gorm:"default:true;index" json:"is_enabled"`
	DisplayOrder   int            `gorm:"default:0" json:"display_order"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`

	ToolType ToolType `gorm:"foreignKey:ToolTypeID" json:"tool_type,omitempty"`
}

func (p *ToolProvider) BeforeCreate(tx *gorm.DB) error {
	if p.ID == "" {
		p.ID = uuid.New().String()
	}
	return nil
}

func (*ToolProvider) TableName() string {
	return "tool_providers"
}
