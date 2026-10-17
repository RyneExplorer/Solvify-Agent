package repository

import "context"

// UserToolConfigRepository 仅保留旧配置的级联清理能力；MCP连接使用MCPRepository。
type UserToolConfigRepository interface {
	DeleteByProviderID(ctx context.Context, providerID string) error
	DeleteByToolTypeID(ctx context.Context, toolTypeID string) error
}
