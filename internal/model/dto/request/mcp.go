package request

import "encoding/json"

type CreateMCPConnectionRequest struct {
	TemplateID  *string         `json:"template_id" binding:"omitempty,uuid"`
	DisplayName string          `json:"display_name" binding:"required,max=100"`
	Connection  json.RawMessage `json:"connection"`
	Config      json.RawMessage `json:"config"`
}
type UpdateMCPConnectionRequest struct {
	DisplayName *string          `json:"display_name" binding:"omitempty,min=1,max=100"`
	Connection  *json.RawMessage `json:"connection"`
	Config      *json.RawMessage `json:"config"`
	IsEnabled   *bool            `json:"is_enabled"`
}
type ProbeMCPConnectionRequest struct {
	ID          string          `json:"id" binding:"omitempty,uuid"`
	DisplayName string          `json:"display_name" binding:"max=100"`
	TemplateID  *string         `json:"template_id" binding:"omitempty,uuid"`
	Connection  json.RawMessage `json:"connection"`
	Config      json.RawMessage `json:"config"`
}
