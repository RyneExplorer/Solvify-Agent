package response

import (
	"encoding/json"
	"time"
)

type MCPConnectionInfo struct {
	ID          string          `json:"id"`
	TemplateID  *string         `json:"template_id"`
	DisplayName string          `json:"display_name"`
	IsEnabled   bool            `json:"is_enabled"`
	Connection  json.RawMessage `json:"connection,omitempty"`
	Config      json.RawMessage `json:"config,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}
type MCPTemplateInfo struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	ConfigSchema json.RawMessage `json:"config_schema"`
}
