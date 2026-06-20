package response

import "encoding/json"

// ToolTypeInfo 工具类型信息
type ToolTypeInfo struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	ToolKey       string          `json:"tool_key"`
	Description   string          `json:"description"`
	ExecutionMode string          `json:"execution_mode"`
	InputSchema   json.RawMessage `json:"input_schema"`
	IsEnabled     bool            `json:"is_enabled"`
	ProviderCount int             `json:"provider_count"`
}

// ListToolTypesResponse 工具类型列表响应
type ListToolTypesResponse struct {
	ToolTypes []ToolTypeInfo `json:"tool_types"`
}

// ToolProviderInfo 工具供应商信息
type ToolProviderInfo struct {
	ID             string          `json:"id"`
	ToolTypeID     string          `json:"tool_type_id"`
	ProviderKey    string          `json:"provider_key"`
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	ProviderType   string          `json:"provider_type"`
	ConfigSchema   json.RawMessage `json:"config_schema"`
	InputSchema    json.RawMessage `json:"input_schema"`
	ProviderConfig json.RawMessage `json:"provider_config"`
	AdminConfig    json.RawMessage `json:"admin_config"`
	RateLimit      json.RawMessage `json:"rate_limit"`
	IsEnabled      bool            `json:"is_enabled"`
	DisplayOrder   int             `json:"display_order"`
}

// ListToolProvidersResponse 工具供应商列表响应
type ListToolProvidersResponse struct {
	Providers []ToolProviderInfo `json:"providers"`
}
