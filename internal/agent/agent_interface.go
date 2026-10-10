package agent

import (
	"context"

	"github.com/cloudwego/eino/components/model"

	"solvify-agent/internal/repository"
)

// AgentInterface 是业务层使用的执行和工具预算能力。
type AgentInterface interface {
	Execute(ctx context.Context, req Request, chatModel model.ToolCallingChatModel) (<-chan Event, error)
	EstimateToolsTokens(ctx context.Context, userID string, kbIDs []string, modelName string, profile Profile, mcpDisabled bool, userToolConfigIDs ...string) (int, context.Context, error)
}

// EngineInterface 供 App 在启动阶段装配 Agent，不向业务层暴露工具注册操作。
type EngineInterface interface {
	AgentInterface
	RegisterInternal(name string, order int, dangerous bool, domain ToolDomain, build ToolBuildFn)
	WithCheckpointRepo(repo repository.AgentCheckpointRepo)
}
