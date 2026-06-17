package tool

import (
	"context"

	einoTool "github.com/cloudwego/eino/components/tool"

	"solvify-agent/internal/model/entity"
)

// Provider 工具供应商接口
//
// 每个 Provider 对应一个具体的第三方服务（SerpAPI、Tavily 等），由开发者一次性实现。
// 所有技术参数（URL、认证方式、参数映射、响应解析）硬编码在 Execute 中，
// 管理员通过 adminConfig 配置业务参数，用户通过 userConfig 提供 API Key。
type Provider interface {
	Name() string

	// GetInputSchema Agent 调用时的参数定义（固定，代码写死）
	GetInputSchema() map[string]interface{}

	// GetConfigSchema 用户需要填写的配置参数定义（固定，代码写死）
	GetConfigSchema() map[string]interface{}

	// Validate 校验用户配置
	Validate(userConfig map[string]interface{}) error

	// Execute 执行工具调用
	// toolInput:    LLM 传来的参数
	// userConfig:   用户配置（API Key 等）
	// adminConfig:  管理员配置的业务参数
	Execute(ctx context.Context, toolInput, userConfig, adminConfig map[string]interface{}) (string, error)
}

// ProviderRegistry 供应商注册表接口
type ProviderRegistry interface {
	Register(key string, provider Provider)
	Get(key string) Provider
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
	CreateAgentTools(ctx context.Context, userID string) []einoTool.BaseTool
}
