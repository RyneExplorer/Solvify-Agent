package repository

import (
	"context"

	"solvify-agent/internal/model/entity"
)

// ModelRepo 定义系统模型数据访问接口
type ModelRepo interface {
	Create(ctx context.Context, model *entity.Model) error
	Update(ctx context.Context, model *entity.Model) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context) ([]entity.Model, error)
	GetByID(ctx context.Context, id string) (*entity.Model, error)
	ExistsByModelID(ctx context.Context, modelID string, excludeID string) (bool, error)
}
