package request

import "encoding/json"

// CreateToolProviderRequest 创建工具供应商请求
type CreateToolProviderRequest struct {
	ToolTypeID     string          `json:"tool_type_id" binding:"required"`
	ProviderKey    string          `json:"provider_key" binding:"required,max=50"`
	Name           string          `json:"name" binding:"required,max=100"`
	Description    string          `json:"description"`
	ProviderType   string          `json:"provider_type" binding:"required,oneof=http mcp custom"`
	ConfigSchema   json.RawMessage `json:"config_schema"`
	InputSchema    json.RawMessage `json:"input_schema"`
	ProviderConfig json.RawMessage `json:"provider_config"`
	AdminConfig    json.RawMessage `json:"admin_config"`
	RateLimit      json.RawMessage `json:"rate_limit"`
}

// UpdateToolProviderRequest 更新工具供应商请求
type UpdateToolProviderRequest struct {
	Name           *string          `json:"name,omitempty" binding:"omitempty,max=100"`
	Description    *string          `json:"description,omitempty"`
	ProviderType   *string          `json:"provider_type,omitempty" binding:"omitempty,oneof=http mcp custom"`
	ConfigSchema   *json.RawMessage `json:"config_schema,omitempty"`
	InputSchema    *json.RawMessage `json:"input_schema,omitempty"`
	ProviderConfig *json.RawMessage `json:"provider_config,omitempty"`
	AdminConfig    *json.RawMessage `json:"admin_config,omitempty"`
	RateLimit      *json.RawMessage `json:"rate_limit,omitempty"`
	IsEnabled      *bool            `json:"is_enabled,omitempty"`
}
