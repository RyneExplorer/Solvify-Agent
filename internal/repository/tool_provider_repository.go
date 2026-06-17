package repository

import (
	"context"

	"gorm.io/gorm"

	"solvify-agent/internal/model/entity"
)

type toolProviderRepository struct {
	db *gorm.DB
}

// NewToolProviderRepository 创建工具供应商仓储实例
func NewToolProviderRepository(db *gorm.DB) ToolProviderRepository {
	return &toolProviderRepository{db: db}
}

func (r *toolProviderRepository) Create(ctx context.Context, provider *entity.ToolProvider) error {
	return r.db.WithContext(ctx).Create(provider).Error
}

func (r *toolProviderRepository) Update(ctx context.Context, provider *entity.ToolProvider) error {
	return r.db.WithContext(ctx).Save(provider).Error
}

func (r *toolProviderRepository) Delete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Delete(&entity.ToolProvider{}, "id = ?", id).Error
}

func (r *toolProviderRepository) GetByID(ctx context.Context, id string) (*entity.ToolProvider, error) {
	var provider entity.ToolProvider
	err := r.db.WithContext(ctx).First(&provider, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &provider, nil
}

func (r *toolProviderRepository) ListByToolTypeID(ctx context.Context, toolTypeID string) ([]entity.ToolProvider, error) {
	var providers []entity.ToolProvider
	err := r.db.WithContext(ctx).Where("tool_type_id = ?", toolTypeID).Order("name ASC").Find(&providers).Error
	return providers, err
}

func (r *toolProviderRepository) ListEnabledByToolTypeID(ctx context.Context, toolTypeID string) ([]entity.ToolProvider, error) {
	var providers []entity.ToolProvider
	err := r.db.WithContext(ctx).Where("tool_type_id = ? AND is_enabled = ?", toolTypeID, true).Order("name ASC").Find(&providers).Error
	return providers, err
}

func (r *toolProviderRepository) ExistsByKey(ctx context.Context, toolTypeID, providerKey string) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).Model(&entity.ToolProvider{}).
		Where("tool_type_id = ? AND provider_key = ?", toolTypeID, providerKey).
		Count(&count).Error
	return count > 0, err
}
