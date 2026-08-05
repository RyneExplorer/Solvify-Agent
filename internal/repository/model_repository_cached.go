package repository

import (
	"context"
	"fmt"

	"solvify-agent/internal/model/entity"
	"solvify-agent/pkg/cache"
	"solvify-agent/pkg/logger"
)

// cachedModelRepository 为 ModelRepo 添加 Redis 缓存层
type cachedModelRepository struct {
	inner ModelRepo
	cache *cache.RedisCache
}

// NewCachedModelRepository 创建带缓存的模型仓库
func NewCachedModelRepository(inner ModelRepo, c *cache.RedisCache) ModelRepo {
	return &cachedModelRepository{inner: inner, cache: c}
}

// Create 创建模型配置
func (r *cachedModelRepository) Create(ctx context.Context, model *entity.Model) error {
	return r.inner.Create(ctx, model)
}

// Update 更新模型配置并清除对应缓存
func (r *cachedModelRepository) Update(ctx context.Context, model *entity.Model) error {
	if err := r.inner.Update(ctx, model); err != nil {
		return err
	}
	if err := r.cache.Delete(ctx, "id:"+model.ID); err != nil {
		logger.Warnf("模型缓存清除失败: %v", err)
	}
	return nil
}

// Delete 删除模型配置并清除对应缓存
func (r *cachedModelRepository) Delete(ctx context.Context, id string) error {
	if err := r.inner.Delete(ctx, id); err != nil {
		return err
	}
	if err := r.cache.Delete(ctx, "id:"+id); err != nil {
		logger.Warnf("模型缓存清除失败: %v", err)
	}
	return nil
}

// List 返回全部模型列表
func (r *cachedModelRepository) List(ctx context.Context) ([]entity.Model, error) {
	return r.inner.List(ctx)
}

// GetByID 根据 ID 获取模型，优先读取缓存
func (r *cachedModelRepository) GetByID(ctx context.Context, id string) (*entity.Model, error) {
	key := fmt.Sprintf("id:%s", id)
	var model entity.Model
	if found, _ := r.cache.Get(ctx, key, &model); found {
		return &model, nil
	}
	result, err := r.inner.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	_ = r.cache.Set(ctx, key, result, 0)
	return result, nil
}

// ExistsByModelID 检查指定 model_id 是否已存在
func (r *cachedModelRepository) ExistsByModelID(ctx context.Context, modelID string, excludeID string) (bool, error) {
	return r.inner.ExistsByModelID(ctx, modelID, excludeID)
}
