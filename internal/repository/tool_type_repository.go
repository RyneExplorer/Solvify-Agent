package repository

import (
	"context"

	"gorm.io/gorm"

	"solvify-agent/internal/model/entity"
)

type toolTypeRepository struct {
	db *gorm.DB
}

// NewToolTypeRepository 创建工具类型仓储实例
func NewToolTypeRepository(db *gorm.DB) ToolTypeRepository {
	return &toolTypeRepository{db: db}
}

func (r *toolTypeRepository) Create(ctx context.Context, toolType *entity.ToolType) error {
	return r.db.WithContext(ctx).Create(toolType).Error
}

func (r *toolTypeRepository) Update(ctx context.Context, toolType *entity.ToolType) error {
	return r.db.WithContext(ctx).Save(toolType).Error
}

func (r *toolTypeRepository) Delete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Delete(&entity.ToolType{}, "id = ?", id).Error
}

func (r *toolTypeRepository) GetByID(ctx context.Context, id string) (*entity.ToolType, error) {
	var toolType entity.ToolType
	err := r.db.WithContext(ctx).First(&toolType, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &toolType, nil
}

func (r *toolTypeRepository) GetByKey(ctx context.Context, toolKey string) (*entity.ToolType, error) {
	var toolType entity.ToolType
	err := r.db.WithContext(ctx).First(&toolType, "tool_key = ?", toolKey).Error
	if err != nil {
		return nil, err
	}
	return &toolType, nil
}

func (r *toolTypeRepository) List(ctx context.Context) ([]entity.ToolType, error) {
	var toolTypes []entity.ToolType
	err := r.db.WithContext(ctx).Order("name ASC").Find(&toolTypes).Error
	return toolTypes, err
}

func (r *toolTypeRepository) ListEnabled(ctx context.Context) ([]entity.ToolType, error) {
	var toolTypes []entity.ToolType
	err := r.db.WithContext(ctx).Where("is_enabled = ?", true).Order("name ASC").Find(&toolTypes).Error
	return toolTypes, err
}

func (r *toolTypeRepository) ExistsByKey(ctx context.Context, toolKey string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&entity.ToolType{}).Where("tool_key = ?", toolKey).Count(&count).Error
	return count > 0, err
}
