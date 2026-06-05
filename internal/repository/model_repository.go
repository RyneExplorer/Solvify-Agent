package repository

import (
	"context"

	"gorm.io/gorm"

	"solvify-agent/internal/model/entity"
)

// modelRepository 提供模型数据访问实现
type modelRepository struct {
	db *gorm.DB
}

// NewModelRepository 创建模型仓库
func NewModelRepository(db *gorm.DB) ModelRepo {
	return &modelRepository{db: db}
}

// Create 创建系统模型
func (r *modelRepository) Create(ctx context.Context, model *entity.Model) error {
	return r.db.WithContext(ctx).Create(model).Error
}

// Update 更新系统模型
func (r *modelRepository) Update(ctx context.Context, model *entity.Model) error {
	return r.db.WithContext(ctx).Save(model).Error
}

// Delete 删除系统模型
func (r *modelRepository) Delete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Delete(&entity.Model{}, "id = ?", id).Error
}

// List 获取所有启用的系统模型
func (r *modelRepository) List(ctx context.Context) ([]entity.Model, error) {
	var models []entity.Model
	err := r.db.WithContext(ctx).
		Where("is_enabled = ?", true).
		Order("name ASC").
		Find(&models).Error
	return models, err
}

// GetByID 根据 ID 获取模型
func (r *modelRepository) GetByID(ctx context.Context, id string) (*entity.Model, error) {
	var model entity.Model
	err := r.db.WithContext(ctx).
		Where("id = ?", id).
		First(&model).Error
	if err != nil {
		return nil, err
	}
	return &model, nil
}

// ExistsByModelID 检查 model_id 是否已存在
func (r *modelRepository) ExistsByModelID(ctx context.Context, modelID string, excludeID string) (bool, error) {
	var count int64
	query := r.db.WithContext(ctx).Model(&entity.Model{}).Where("model_id = ?", modelID)
	if excludeID != "" {
		query = query.Where("id != ?", excludeID)
	}
	err := query.Count(&count).Error
	return count > 0, err
}
