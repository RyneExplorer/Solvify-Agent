package repository

import (
	"context"

	"gorm.io/gorm"

	"solvify-agent/internal/model/entity"
)

type userToolConfigRepository struct {
	db *gorm.DB
}

// NewUserToolConfigRepository 创建旧用户工具配置清理仓库
func NewUserToolConfigRepository(db *gorm.DB) UserToolConfigRepository {
	return &userToolConfigRepository{db: db}
}

// 旧表只用于迁移与清理，供应商/类型删除由Service在同一事务内编排。
// DeleteByProviderID 删除某供应商下的旧用户配置。
func (r *userToolConfigRepository) DeleteByProviderID(ctx context.Context, providerID string) error {
	return dbFor(ctx, r.db).Where("provider_id = ?", providerID).Delete(&entity.UserToolConfig{}).Error
}

// DeleteByToolTypeID 删除某工具类型下的旧用户配置。
func (r *userToolConfigRepository) DeleteByToolTypeID(ctx context.Context, toolTypeID string) error {
	return dbFor(ctx, r.db).Where("tool_type_id = ?", toolTypeID).Delete(&entity.UserToolConfig{}).Error
}
