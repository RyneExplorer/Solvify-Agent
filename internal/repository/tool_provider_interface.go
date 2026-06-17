package repository

import (
	"context"

	"solvify-agent/internal/model/entity"
)

// ToolProviderRepository 工具供应商仓储接口
type ToolProviderRepository interface {
	Create(ctx context.Context, provider *entity.ToolProvider) error
	Update(ctx context.Context, provider *entity.ToolProvider) error
	Delete(ctx context.Context, id string) error
	GetByID(ctx context.Context, id string) (*entity.ToolProvider, error)
	ListByToolTypeID(ctx context.Context, toolTypeID string) ([]entity.ToolProvider, error)
	ListEnabledByToolTypeID(ctx context.Context, toolTypeID string) ([]entity.ToolProvider, error)
	ExistsByKey(ctx context.Context, toolTypeID, providerKey string) (bool, error)
}
