package entity

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// MCPConnection 是用户的个人连接，或对管理员MCP预置的配置绑定。
type MCPConnection struct {
	ID          string         `gorm:"type:varchar(36);primaryKey" json:"id"`
	UserID      string         `gorm:"type:varchar(36);not null;index" json:"user_id"`
	TemplateID  *string        `gorm:"type:varchar(36)" json:"template_id"`
	DisplayName string         `gorm:"type:varchar(100);not null" json:"display_name"`
	Connection  datatypes.JSON `gorm:"type:jsonb;not null" json:"connection"`
	Config      datatypes.JSON `gorm:"type:jsonb;not null" json:"config"`
	IsEnabled   bool           `json:"is_enabled"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	Template    *ToolProvider  `gorm:"foreignKey:TemplateID" json:"-"`
}

func (*MCPConnection) TableName() string { return "mcp_connections" }
func (c *MCPConnection) BeforeCreate(*gorm.DB) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	return nil
}
