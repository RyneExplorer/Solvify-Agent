package response

import "encoding/json"

// ToolTypeInfo 工具类型信息
type ToolTypeInfo struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	ToolKey       string `json:"tool_key"`
	Description   string `json:"description"`
	ExecutionMode string `json:"execution_mode"`
	IsEnabled     bool   `json:"is_enabled"`
	ProviderCount int    `json:"provider_count"`
}

// ListToolTypesResponse 工具类型列表响应
type ListToolTypesResponse struct {
	ToolTypes []ToolTypeInfo `json:"tool_types"`
}

// ToolProviderInfo 工具供应商信息
type ToolProviderInfo struct {
	ID           string          `json:"id"`
	ToolTypeID   string          `json:"tool_type_id"`
	Name         string          `json:"name"`
	ProviderKey  string          `json:"provider_key"`
	Description  string          `json:"description"`
	ConfigSchema json.RawMessage `json:"config_schema"` // 来自 Provider 代码，非 DB
	AdminConfig  json.RawMessage `json:"admin_config"`  // 管理员业务参数
	RateLimit    json.RawMessage `json:"rate_limit"`
	IsEnabled    bool            `json:"is_enabled"`
	DisplayOrder int             `json:"display_order"`
}

// ListToolProvidersResponse 工具供应商列表响应
type ListToolProvidersResponse struct {
	Providers []ToolProviderInfo `json:"providers"`
}
