package request

import "encoding/json"

// CreateToolProviderRequest 创建工具供应商请求
type CreateToolProviderRequest struct {
	ToolTypeID  string          `json:"tool_type_id" binding:"required"`
	Name        string          `json:"name" binding:"required,max=100"`
	ProviderKey string          `json:"provider_key" binding:"required,max=50"`
	Description string          `json:"description"`
	AdminConfig json.RawMessage `json:"admin_config"`
	RateLimit   json.RawMessage `json:"rate_limit"`
}

// UpdateToolProviderRequest 更新工具供应商请求
type UpdateToolProviderRequest struct {
	Name        *string          `json:"name,omitempty" binding:"omitempty,max=100"`
	Description *string          `json:"description,omitempty"`
	AdminConfig *json.RawMessage `json:"admin_config,omitempty"`
	RateLimit   *json.RawMessage `json:"rate_limit,omitempty"`
	IsEnabled   *bool            `json:"is_enabled,omitempty"`
}
