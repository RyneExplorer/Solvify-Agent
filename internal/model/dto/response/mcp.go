package response

import (
	"encoding/json"
	"time"
)

type MCPConnectionInfo struct {
	ID               string          `json:"id"`
	TemplateID       *string         `json:"template_id"`
	DisplayName      string          `json:"display_name"`
	IsEnabled        bool            `json:"is_enabled"`
	Source           string          `json:"source"`
	TemplateName     string          `json:"template_name,omitempty"`
	TemplateEnabled  bool            `json:"template_enabled"`
	EffectiveEnabled bool            `json:"effective_enabled"`
	Connection       json.RawMessage `json:"connection,omitempty"`
	Config           json.RawMessage `json:"config,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

type MCPToolState struct {
	Name      string `json:"name"`
	IsEnabled bool   `json:"is_enabled"`
}

type MCPConnectionTool struct {
	Name             string `json:"name"`
	Description      string `json:"description"`
	IsEnabled        bool   `json:"is_enabled"`
	EffectiveEnabled bool   `json:"effective_enabled"`
}

type MCPConnectionTools struct {
	Tools             []MCPConnectionTool `json:"tools"`
	ConnectionEnabled bool                `json:"connection_enabled"`
}
type MCPTemplateInfo struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	ConfigSchema json.RawMessage `json:"config_schema"`
}
