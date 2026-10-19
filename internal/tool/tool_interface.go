package tool

import (
	"context"

	einoTool "github.com/cloudwego/eino/components/tool"

	"solvify-agent/internal/model/entity"
)

// MCPConnectionStore 是工具装配需要的用户连接读取能力。
type MCPConnectionStore interface {
	ListEnabledByUserID(ctx context.Context, userID string) ([]entity.MCPConnection, error)
	Get(ctx context.Context, userID, id string) (*entity.MCPConnection, error)
}

// ToolFactory 工具工厂接口
type ToolFactory interface {
	// CreateAgentTools 根据用户配置创建 Agent 工具列表
	// 可选 userConfigIDs: 非空时仅加载指定 ID 的配置（仍需满足 enabled 条件）
	CreateAgentTools(ctx context.Context, userID string, userConfigIDs ...string) []einoTool.BaseTool
}
