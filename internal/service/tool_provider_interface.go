package service

import (
	"context"

	"solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/model/dto/response"
)

// ToolProviderService 工具供应商服务接口
type ToolProviderService interface {
	Create(ctx context.Context, req request.CreateToolProviderRequest) (*response.ToolProviderInfo, error)
	Update(ctx context.Context, id string, req request.UpdateToolProviderRequest) (*response.ToolProviderInfo, error)
	Delete(ctx context.Context, id string) error
	GetByID(ctx context.Context, id string) (*response.ToolProviderInfo, error)
	ListByToolTypeID(ctx context.Context, toolTypeID string) (*response.ListToolProvidersResponse, error)
	ListEnabledByToolTypeID(ctx context.Context, toolTypeID string) (*response.ListToolProvidersResponse, error)
	// ListProviderKeys 返回所有已注册的 provider_key
	ListProviderKeys() []string
}
