package response

import "encoding/json"

// UserToolConfigInfo 用户工具配置信息
type UserToolConfigInfo struct {
	ID           string          `json:"id"`
	ToolTypeID   string          `json:"tool_type_id"`
	ToolTypeName string          `json:"tool_type_name"`
	ToolTypeKey  string          `json:"tool_type_key"`
	ProviderID   string          `json:"provider_id"`
	ProviderName string          `json:"provider_name"`
	DisplayName  string          `json:"display_name"`
	Config       json.RawMessage `json:"config"`
	IsEnabled    bool            `json:"is_enabled"`
}

// ListUserToolConfigsResponse 用户工具配置列表响应
type ListUserToolConfigsResponse struct {
	Configs []UserToolConfigInfo `json:"configs"`
}
