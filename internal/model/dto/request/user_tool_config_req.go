package request

import "encoding/json"

// CreateUserToolConfigRequest 创建用户工具配置请求
type CreateUserToolConfigRequest struct {
	ToolTypeID  string          `json:"tool_type_id" binding:"required"`
	ProviderID  string          `json:"provider_id" binding:"required"`
	DisplayName string          `json:"display_name" binding:"max=100"`
	Config      json.RawMessage `json:"config" binding:"required"`
}

// UpdateUserToolConfigRequest 更新用户工具配置请求
type UpdateUserToolConfigRequest struct {
	ProviderID  *string          `json:"provider_id,omitempty"`
	DisplayName *string          `json:"display_name,omitempty" binding:"omitempty,max=100"`
	Config      *json.RawMessage `json:"config,omitempty"`
	IsEnabled   *bool            `json:"is_enabled,omitempty"`
}
