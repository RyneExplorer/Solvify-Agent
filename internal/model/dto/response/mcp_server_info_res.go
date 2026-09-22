package response

import "encoding/json"

// MCPServerInfo 管理后台 MCP 服务器聚合信息
type MCPServerInfo struct {
	ID              string          `json:"id"`
	ToolTypeID      string          `json:"tool_type_id"`
	ToolTypeName    string          `json:"tool_type_name"`
	ToolTypeKey     string          `json:"tool_type_key"`
	ProviderKey     string          `json:"provider_key"`
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	IsEnabled       bool            `json:"is_enabled"`
	IsSystem        bool            `json:"is_system"`
	MCPToolManifest json.RawMessage `json:"mcp_tool_manifest"`
}

// ListMCPServersResponse MCP 服务器列表响应
type ListMCPServersResponse struct {
	Servers []MCPServerInfo `json:"servers"`
}