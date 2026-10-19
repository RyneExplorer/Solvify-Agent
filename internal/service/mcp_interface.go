package service

import (
	"context"

	"solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/model/dto/response"
)

type MCPService interface {
	Templates(context.Context) ([]response.MCPTemplateInfo, error)
	List(ctx context.Context, userID string) ([]response.MCPConnectionInfo, error)
	Get(ctx context.Context, userID, id string) (*response.MCPConnectionInfo, error)
	Create(ctx context.Context, userID string, req request.CreateMCPConnectionRequest) (*response.MCPConnectionInfo, error)
	Update(ctx context.Context, userID, id string, req request.UpdateMCPConnectionRequest) (*response.MCPConnectionInfo, error)
	Delete(ctx context.Context, userID, id string) error
	Probe(ctx context.Context, userID string, req request.ProbeMCPConnectionRequest) (*response.TestResult, error)
}
