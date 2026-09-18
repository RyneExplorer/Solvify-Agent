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
	return dbFor(ctx, r.db).Create(provider).Error
}

func (r *toolProviderRepository) Update(ctx context.Context, provider *entity.ToolProvider) error {
	return dbFor(ctx, r.db).Save(provider).Error
}

func (r *toolProviderRepository) Delete(ctx context.Context, id string) error {
	return dbFor(ctx, r.db).Delete(&entity.ToolProvider{}, "id = ?", id).Error
}

// DeleteByToolTypeID 删除某工具类型下的全部供应商。
//
// 这里**不再**顺手删 user_tool_configs（此前是）。原因：那张表带缓存，写它的地方必须
// 同时失效 tool:config:user:<uid>，而唯一做这件事的是 UserToolConfigRepository。
// 现在级联由 service 在同一个事务里编排两处删除（见 tool_provider_service.Delete）。
// 守卫见 cached_table_write_guard_test.go。
func (r *toolProviderRepository) DeleteByToolTypeID(ctx context.Context, toolTypeID string) error {
	return dbFor(ctx, r.db).Delete(&entity.ToolProvider{}, "tool_type_id = ?", toolTypeID).Error
}

func (r *toolProviderRepository) GetByID(ctx context.Context, id string) (*entity.ToolProvider, error) {
	var provider entity.ToolProvider
	err := dbFor(ctx, r.db).First(&provider, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &provider, nil
}

func (r *toolProviderRepository) GetByProviderKey(ctx context.Context, toolTypeID, providerKey string) (*entity.ToolProvider, error) {
	var provider entity.ToolProvider
	err := dbFor(ctx, r.db).
		Where("tool_type_id = ? AND provider_key = ?", toolTypeID, providerKey).
		First(&provider).Error
	if err != nil {
		return nil, err
	}
	return &provider, nil
}

func (r *toolProviderRepository) ListByToolTypeID(ctx context.Context, toolTypeID string) ([]entity.ToolProvider, error) {
	var providers []entity.ToolProvider
	err := dbFor(ctx, r.db).Where("tool_type_id = ?", toolTypeID).Order("name ASC").Find(&providers).Error
	return providers, err
}

func (r *toolProviderRepository) ListEnabledByToolTypeID(ctx context.Context, toolTypeID string) ([]entity.ToolProvider, error) {
	var providers []entity.ToolProvider
	err := dbFor(ctx, r.db).Where("tool_type_id = ? AND is_enabled = ?", toolTypeID, true).Order("name ASC").Find(&providers).Error
	return providers, err
}

func (r *toolProviderRepository) ExistsByKey(ctx context.Context, toolTypeID, providerKey string) (bool, error) {
	var count int64
	err := dbFor(ctx, r.db).Model(&entity.ToolProvider{}).
		Where("tool_type_id = ? AND provider_key = ?", toolTypeID, providerKey).
		Count(&count).Error
	return count > 0, err
}
