package entity

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// ToolType 系统工具类型（管理员管理）
type ToolType struct {
	ID            string         `gorm:"type:varchar(36);primaryKey" json:"id"`
	Name          string         `gorm:"type:varchar(100);not null" json:"name"`
	ToolKey       string         `gorm:"type:varchar(50);not null;uniqueIndex" json:"tool_key"`
	Description   string         `gorm:"type:text" json:"description"`
	ExecutionMode string         `gorm:"type:varchar(20);default:sync" json:"execution_mode"`
	InputSchema   datatypes.JSON `gorm:"type:jsonb" json:"input_schema"` // Agent 调用参数 Schema（可选，覆盖供应商的）
	IsEnabled     bool           `gorm:"default:true;index" json:"is_enabled"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
}

func (t *ToolType) BeforeCreate(tx *gorm.DB) error {
	if t.ID == "" {
		t.ID = uuid.New().String()
	}
	return nil
}

func (*ToolType) TableName() string {
	return "tool_types"
}
