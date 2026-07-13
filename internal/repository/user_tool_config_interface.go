package repository

import (
	"context"

	"solvify-agent/internal/model/entity"
)

// UserToolConfigRepository 用户工具配置仓储接口
type UserToolConfigRepository interface {
	Create(ctx context.Context, config *entity.UserToolConfig) error
	Update(ctx context.Context, config *entity.UserToolConfig) error
	Delete(ctx context.Context, id string) error
	GetByID(ctx context.Context, id string) (*entity.UserToolConfig, error)
	GetByUserAndToolType(ctx context.Context, userID, toolTypeID string) (*entity.UserToolConfig, error)
	GetByUserAndProvider(ctx context.Context, userID, providerID string) (*entity.UserToolConfig, error)
	DisableOthersByToolType(ctx context.Context, userID, toolTypeID, exceptID string) error
	ListByUserID(ctx context.Context, userID string) ([]entity.UserToolConfig, error)
	ListEnabledByUserID(ctx context.Context, userID string) ([]entity.UserToolConfig, error)
}
