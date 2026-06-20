package repository

import (
	"context"

	"solvify-agent/internal/model/entity"
)

// ToolTypeRepository 工具类型仓储接口
type ToolTypeRepository interface {
	Create(ctx context.Context, toolType *entity.ToolType) error
	Update(ctx context.Context, toolType *entity.ToolType) error
	Delete(ctx context.Context, id string) error
	GetByID(ctx context.Context, id string) (*entity.ToolType, error)
	GetByKey(ctx context.Context, toolKey string) (*entity.ToolType, error)
	List(ctx context.Context) ([]entity.ToolType, error)
	ListEnabled(ctx context.Context) ([]entity.ToolType, error)
	ExistsByKey(ctx context.Context, toolKey string) (bool, error)
	GetProviderCounts(ctx context.Context) (map[string]int, error)
}
