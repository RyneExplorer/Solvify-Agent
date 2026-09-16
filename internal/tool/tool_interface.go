package tool

import (
	"context"

	einoTool "github.com/cloudwego/eino/components/tool"

	"solvify-agent/internal/model/entity"
)

// ProviderRegistry 供应商注册表接口
type ProviderRegistry interface {
	Register(providerType string, provider Provider)
	Get(providerType string) Provider
	List() map[string]Provider
	Keys() []string
}

// UserToolConfigStore 用户工具配置存储——tool 层需要的最小读接口
type UserToolConfigStore interface {
	ListEnabledByUserID(ctx context.Context, userID string) ([]entity.UserToolConfig, error)
}

// ToolTypeStore 工具类型存储——tool 层需要的最小接口
type ToolTypeStore interface {
	GetByKey(ctx context.Context, toolKey string) (*entity.ToolType, error)
}

// ToolFactory 工具工厂接口
type ToolFactory interface {
	// CreateAgentTools 根据用户配置创建 Agent 工具列表
	// 可选 userConfigIDs: 非空时仅加载指定 ID 的配置（仍需满足 enabled 条件）
	CreateAgentTools(ctx context.Context, userID string, userConfigIDs ...string) []einoTool.BaseTool
}
