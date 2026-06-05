package repository

import (
	"context"

	"solvify-agent/internal/model/entity"
)

// UserModelConfigRepo 定义用户模型配置数据访问接口
type UserModelConfigRepo interface {
	Create(ctx context.Context, config *entity.UserModelConfig) error
	Update(ctx context.Context, config *entity.UserModelConfig) error
	Delete(ctx context.Context, id string, userID string) error
	GetByID(ctx context.Context, id string, userID string) (*entity.UserModelConfig, error)
	ListByUserID(ctx context.Context, userID string) ([]entity.UserModelConfig, error)
	ExistsByModelID(ctx context.Context, userID string, modelID string, excludeID string) (bool, error)
}
