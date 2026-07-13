package repository

import (
	"context"
	"fmt"

	"solvify-agent/internal/model/entity"
	"solvify-agent/pkg/cache"
	"solvify-agent/pkg/logger"
)

// cachedUserModelConfigRepository 为 UserModelConfigRepo 添加 Redis 缓存层
type cachedUserModelConfigRepository struct {
	inner UserModelConfigRepo
	cache *cache.RedisCache
}

// NewCachedUserModelConfigRepository 创建带缓存的用户模型配置仓库
func NewCachedUserModelConfigRepository(inner UserModelConfigRepo, c *cache.RedisCache) UserModelConfigRepo {
	return &cachedUserModelConfigRepository{inner: inner, cache: c}
}

func (r *cachedUserModelConfigRepository) Create(ctx context.Context, config *entity.UserModelConfig) error {
	return r.inner.Create(ctx, config)
}

func (r *cachedUserModelConfigRepository) Update(ctx context.Context, config *entity.UserModelConfig) error {
	if err := r.inner.Update(ctx, config); err != nil {
		return err
	}
	if err := r.cache.Delete(ctx, fmt.Sprintf("id:%s:%s", config.ID, config.UserID)); err != nil {
		logger.Warnf("用户模型配置缓存清除失败: %v", err)
	}
	return nil
}

func (r *cachedUserModelConfigRepository) Delete(ctx context.Context, id string, userID string) error {
	if err := r.inner.Delete(ctx, id, userID); err != nil {
		return err
	}
	if err := r.cache.Delete(ctx, fmt.Sprintf("id:%s:%s", id, userID)); err != nil {
		logger.Warnf("用户模型配置缓存清除失败: %v", err)
	}
	return nil
}

func (r *cachedUserModelConfigRepository) GetByID(ctx context.Context, id string, userID string) (*entity.UserModelConfig, error) {
	key := fmt.Sprintf("id:%s:%s", id, userID)
	var config entity.UserModelConfig
	if found, _ := r.cache.Get(ctx, key, &config); found {
		return &config, nil
	}
	result, err := r.inner.GetByID(ctx, id, userID)
	if err != nil {
		return nil, err
	}
	_ = r.cache.Set(ctx, key, result, 0)
	return result, nil
}

func (r *cachedUserModelConfigRepository) ListByUserID(ctx context.Context, userID string) ([]entity.UserModelConfig, error) {
	return r.inner.ListByUserID(ctx, userID)
}

func (r *cachedUserModelConfigRepository) ExistsByModelID(ctx context.Context, userID string, modelID string, excludeID string) (bool, error) {
	return r.inner.ExistsByModelID(ctx, userID, modelID, excludeID)
}
