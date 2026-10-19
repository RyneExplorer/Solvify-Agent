package request

import "encoding/json"

type CreateMCPTemplateRequest struct {
	Key          string          `json:"key" binding:"required,max=50"`
	Name         string          `json:"name" binding:"required,max=100"`
	Description  string          `json:"description"`
	Connection   json.RawMessage `json:"connection" binding:"required"`
	ConfigSchema json.RawMessage `json:"config_schema"`
}
type UpdateMCPTemplateRequest struct {
	Name         *string          `json:"name" binding:"omitempty,max=100"`
	Description  *string          `json:"description"`
	Connection   *json.RawMessage `json:"connection"`
	ConfigSchema *json.RawMessage `json:"config_schema"`
	IsEnabled    *bool            `json:"is_enabled"`
}
type ProbeMCPTemplateRequest struct {
	ID         string          `json:"id"`
	Connection json.RawMessage `json:"connection"`
}
