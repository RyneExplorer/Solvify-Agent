package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	apperrors "solvify-agent/pkg/errors"
	"solvify-agent/internal/model/entity"
)

// userModelConfigRepository 提供用户模型配置数据访问实现
type userModelConfigRepository struct {
	db *gorm.DB
}

// NewUserModelConfigRepository 创建用户模型配置仓库
func NewUserModelConfigRepository(db *gorm.DB) UserModelConfigRepo {
	return &userModelConfigRepository{db: db}
}

// Create 创建用户模型配置
func (r *userModelConfigRepository) Create(ctx context.Context, config *entity.UserModelConfig) error {
	return r.db.WithContext(ctx).Create(config).Error
}

// Update 更新用户模型配置
func (r *userModelConfigRepository) Update(ctx context.Context, config *entity.UserModelConfig) error {
	return r.db.WithContext(ctx).Save(config).Error
}

// Delete 删除用户模型配置（软删除或硬删除）
func (r *userModelConfigRepository) Delete(ctx context.Context, id string, userID string) error {
	return r.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", id, userID).
		Delete(&entity.UserModelConfig{}).Error
}

// GetByID 根据 ID 和用户 ID 获取配置；找不到返回业务错误 CodeModelConfigNotFound
func (r *userModelConfigRepository) GetByID(ctx context.Context, id string, userID string) (*entity.UserModelConfig, error) {
	var config entity.UserModelConfig
	err := r.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", id, userID).
		First(&config).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.NewDefault(apperrors.CodeModelConfigNotFound)
		}
		return nil, err
	}
	return &config, nil
}

// ListByUserID 获取用户所有模型配置
func (r *userModelConfigRepository) ListByUserID(ctx context.Context, userID string) ([]entity.UserModelConfig, error) {
	var configs []entity.UserModelConfig
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("display_name ASC").
		Find(&configs).Error
	return configs, err
}

// ExistsByModelID 检查用户的 model_id 是否已存在
func (r *userModelConfigRepository) ExistsByModelID(ctx context.Context, userID string, modelID string, excludeID string) (bool, error) {
	var count int64
	query := r.db.WithContext(ctx).Model(&entity.UserModelConfig{}).
		Where("user_id = ? AND model_id = ?", userID, modelID)
	if excludeID != "" {
		query = query.Where("id != ?", excludeID)
	}
	err := query.Count(&count).Error
	return count > 0, err
}
