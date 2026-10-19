package service

import (
	"context"
	"solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/model/dto/response"
)

type MCPTemplateService interface {
	Create(context.Context, request.CreateMCPTemplateRequest) (*response.MCPTemplateAdminInfo, error)
	Update(context.Context, string, request.UpdateMCPTemplateRequest) (*response.MCPTemplateAdminInfo, error)
	Delete(context.Context, string) error
	GetByID(context.Context, string) (*response.MCPTemplateAdminInfo, error)
	List(context.Context) ([]response.MCPTemplateAdminInfo, error)
	Probe(context.Context, request.ProbeMCPTemplateRequest) (*response.TestResult, error)
}
