package repository

import (
	"context"
	"errors"

	"solvify-agent/internal/model/entity"
)

var ErrMCPConnectionChanged = errors.New("MCP配置已更新或删除，请刷新后重试")
var ErrMCPToolStateChanged = errors.New("MCP连接不存在或停用工具数已达上限，请刷新后重试")

type MCPRepository interface {
	Create(ctx context.Context, connection *entity.MCPConnection) error
	Update(ctx context.Context, connection *entity.MCPConnection) error
	SetToolEnabled(ctx context.Context, userID, id, name string, enabled bool) error
	Delete(ctx context.Context, userID, id string) error
	Get(ctx context.Context, userID, id string) (*entity.MCPConnection, error)
	List(ctx context.Context, userID string) ([]entity.MCPConnection, error)
	ListEnabledByUserID(ctx context.Context, userID string) ([]entity.MCPConnection, error)
}
