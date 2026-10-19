package response

import "encoding/json"

// MCPTemplateAdminInfo 列表不返回凭据，编辑详情才返回连接。
type MCPTemplateAdminInfo struct {
	ID              string          `json:"id"`
	Key             string          `json:"key"`
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	Connection      json.RawMessage `json:"connection,omitempty"`
	ConfigSchema    json.RawMessage `json:"config_schema"`
	IsEnabled       bool            `json:"is_enabled"`
	IsSystem        bool            `json:"is_system"`
	MCPToolManifest json.RawMessage `json:"mcp_tool_manifest"`
}
