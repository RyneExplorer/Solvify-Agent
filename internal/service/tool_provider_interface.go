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
	// ListProviderTypes 返回所有已注册的供应商类型（http, mcp, custom）
	ListProviderTypes() []string
	// Test 测试工具连接
	Test(ctx context.Context, req request.TestToolRequest) (*response.TestResult, error)
	// ProbeMCPTools 探测 MCP 供应商的工具清单（不做工具调用）；传入 provider_id 时持久化到该供应商
	ProbeMCPTools(ctx context.Context, req request.TestToolRequest) (*response.TestResult, error)
	// ListMCPServers 列出全部 MCP 供应商（含所属工具类型信息）
	ListMCPServers(ctx context.Context) (*response.ListMCPServersResponse, error)
	// DeleteMCPServerWithCleanup 删除 MCP 供应商；若其所属工具类型随之变空且非 mcp 容器类型，级联删除该类型
	DeleteMCPServerWithCleanup(ctx context.Context, providerID string) error
	// CleanupEmptyToolTypes 删除所有无供应商的非容器工具类型（含其用户配置），返回删除数量
	CleanupEmptyToolTypes(ctx context.Context) (int, error)
}
