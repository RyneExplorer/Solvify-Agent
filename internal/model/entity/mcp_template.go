package entity

import (
	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
	"time"
)

// MCPTemplate 是管理员维护的连接与用户认证字段模板。
type MCPTemplate struct {
	ID  string `gorm:"type:varchar(36);primaryKey"`
	Key string `gorm:"column:template_key;type:text;not null;uniqueIndex"`
	// ToolPrefix 创建后不变，迁移时保留有效 checkpoint 中的工具名称。
	ToolPrefix      string         `gorm:"type:text;not null"`
	Name            string         `gorm:"type:varchar(100);not null"`
	Description     string         `gorm:"type:text"`
	Connection      datatypes.JSON `gorm:"type:jsonb;not null"`
	ConfigSchema    datatypes.JSON `gorm:"type:jsonb"`
	MCPToolManifest datatypes.JSON `gorm:"type:jsonb"`
	IsEnabled       bool
	IsSystem        bool
	DisplayOrder    int
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

func (*MCPTemplate) TableName() string { return "mcp_templates" }
func (t *MCPTemplate) BeforeCreate(*gorm.DB) error {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	return nil
}
