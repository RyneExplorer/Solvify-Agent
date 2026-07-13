package repository

import (
	"context"

	"gorm.io/gorm"

	"solvify-agent/internal/model/entity"
)

type userToolConfigRepository struct {
	db *gorm.DB
}

// NewUserToolConfigRepository 创建用户工具配置仓储实例
func NewUserToolConfigRepository(db *gorm.DB) UserToolConfigRepository {
	return &userToolConfigRepository{db: db}
}

func (r *userToolConfigRepository) Create(ctx context.Context, config *entity.UserToolConfig) error {
	return r.db.WithContext(ctx).Create(config).Error
}

func (r *userToolConfigRepository) Update(ctx context.Context, config *entity.UserToolConfig) error {
	return r.db.WithContext(ctx).Save(config).Error
}

func (r *userToolConfigRepository) Delete(ctx context.Context, id string) error {
	return r.db.WithContext(ctx).Delete(&entity.UserToolConfig{}, "id = ?", id).Error
}

func (r *userToolConfigRepository) GetByID(ctx context.Context, id string) (*entity.UserToolConfig, error) {
	var config entity.UserToolConfig
	err := r.db.WithContext(ctx).
		Preload("ToolType").
		Preload("ToolProvider").
		First(&config, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &config, nil
}

func (r *userToolConfigRepository) GetByUserAndToolType(ctx context.Context, userID, toolTypeID string) (*entity.UserToolConfig, error) {
	var config entity.UserToolConfig
	err := r.db.WithContext(ctx).
		Preload("ToolType").
		Preload("ToolProvider").
		Where("user_id = ? AND tool_type_id = ?", userID, toolTypeID).
		First(&config).Error
	if err != nil {
		return nil, err
	}
	return &config, nil
}

func (r *userToolConfigRepository) GetByUserAndProvider(ctx context.Context, userID, providerID string) (*entity.UserToolConfig, error) {
	var config entity.UserToolConfig
	err := r.db.WithContext(ctx).
		Preload("ToolType").
		Preload("ToolProvider").
		Where("user_id = ? AND provider_id = ?", userID, providerID).
		First(&config).Error
	if err != nil {
		return nil, err
	}
	return &config, nil
}

func (r *userToolConfigRepository) DisableOthersByToolType(ctx context.Context, userID, toolTypeID, exceptID string) error {
	query := r.db.WithContext(ctx).
		Model(&entity.UserToolConfig{}).
		Where("user_id = ? AND tool_type_id = ?", userID, toolTypeID).
		Where("is_enabled = ?", true)
	if exceptID != "" {
		query = query.Where("id <> ?", exceptID)
	}
	return query.Update("is_enabled", false).Error
}

func (r *userToolConfigRepository) ListByUserID(ctx context.Context, userID string) ([]entity.UserToolConfig, error) {
	var configs []entity.UserToolConfig
	err := r.db.WithContext(ctx).
		Preload("ToolType").
		Preload("ToolProvider").
		Where("user_id = ?", userID).
		Find(&configs).Error
	return configs, err
}

func (r *userToolConfigRepository) ListEnabledByUserID(ctx context.Context, userID string) ([]entity.UserToolConfig, error) {
	var configs []entity.UserToolConfig
	err := r.db.WithContext(ctx).
		Preload("ToolType").
		Preload("ToolProvider").
		Where("user_id = ? AND is_enabled = ?", userID, true).
		Find(&configs).Error
	return configs, err
}
