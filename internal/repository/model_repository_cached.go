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

func (r *cachedModelRepository) Create(ctx context.Context, model *entity.Model) error {
	return r.inner.Create(ctx, model)
}

func (r *cachedModelRepository) Update(ctx context.Context, model *entity.Model) error {
	if err := r.inner.Update(ctx, model); err != nil {
		return err
	}
	if err := r.cache.Delete(ctx, "id:"+model.ID); err != nil {
		logger.Warnf("模型缓存清除失败: %v", err)
	}
	return nil
}

func (r *cachedModelRepository) Delete(ctx context.Context, id string) error {
	if err := r.inner.Delete(ctx, id); err != nil {
		return err
	}
	if err := r.cache.Delete(ctx, "id:"+id); err != nil {
		logger.Warnf("模型缓存清除失败: %v", err)
	}
	return nil
}

func (r *cachedModelRepository) List(ctx context.Context) ([]entity.Model, error) {
	return r.inner.List(ctx)
}

func (r *cachedModelRepository) GetByID(ctx context.Context, id string) (*entity.Model, error) {
	key := fmt.Sprintf("id:%s", id)
	var model entity.Model
	if found, _ := r.cache.Get(ctx, key, &model); found {
		return &model, nil
	}
	result, err := r.inner.GetByID(ctx, id)
	fmt.Println("result:", result)
	if err != nil {
		return nil, err
	}
	_ = r.cache.Set(ctx, key, result, 0)
	return result, nil
}

func (r *cachedModelRepository) ExistsByModelID(ctx context.Context, modelID string, excludeID string) (bool, error) {
	return r.inner.ExistsByModelID(ctx, modelID, excludeID)
}
