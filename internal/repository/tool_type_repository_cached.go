package repository

import (
	"context"

	"solvify-agent/internal/model/entity"
	"solvify-agent/pkg/cache"
	"solvify-agent/pkg/logger"
)

// cachedToolTypeRepository 为 ToolTypeRepository 添加 Redis 缓存层
//
//	缓存策略：写时失效
//	- 按 toolKey 查：key = "tool:type:key:{toolKey}"
//	- 按 ID 查：key = "tool:type:id:{id}"
//	- Create/Update/Delete → 清除对应缓存
type cachedToolTypeRepository struct {
	inner ToolTypeRepository
	cache *cache.RedisCache
}

// NewCachedToolTypeRepository 创建带缓存的工具类型仓库
func NewCachedToolTypeRepository(inner ToolTypeRepository, c *cache.RedisCache) ToolTypeRepository {
	return &cachedToolTypeRepository{inner: inner, cache: c}
}

// ========== 写操作 ==========

func (r *cachedToolTypeRepository) Create(ctx context.Context, toolType *entity.ToolType) error {
	return r.inner.Create(ctx, toolType)
}

func (r *cachedToolTypeRepository) Update(ctx context.Context, toolType *entity.ToolType) error {
	if err := r.inner.Update(ctx, toolType); err != nil {
		return err
	}
	_ = r.cache.Delete(ctx, "id:"+toolType.ID)
	_ = r.cache.Delete(ctx, "key:"+toolType.ToolKey)
	return nil
}

func (r *cachedToolTypeRepository) Delete(ctx context.Context, id string) error {
	toolType, err := r.inner.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := r.inner.Delete(ctx, id); err != nil {
		return err
	}
	_ = r.cache.Delete(ctx, "id:"+id)
	_ = r.cache.Delete(ctx, "key:"+toolType.ToolKey)
	return nil
}

// ========== 读操作：cache-aside ==========

func (r *cachedToolTypeRepository) GetByKey(ctx context.Context, toolKey string) (*entity.ToolType, error) {
	key := "key:" + toolKey
	var tt entity.ToolType
	if found, _ := r.cache.Get(ctx, key, &tt); found {
		return &tt, nil
	}
	result, err := r.inner.GetByKey(ctx, toolKey)
	if err != nil {
		return nil, err
	}
	if err := r.cache.Set(ctx, key, result, 0); err != nil {
		logger.Warnf("工具类型缓存写入失败: key=%s, err=%v", key, err)
	}
	return result, nil
}

func (r *cachedToolTypeRepository) GetByID(ctx context.Context, id string) (*entity.ToolType, error) {
	key := "id:" + id
	var tt entity.ToolType
	if found, _ := r.cache.Get(ctx, key, &tt); found {
		return &tt, nil
	}
	result, err := r.inner.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	_ = r.cache.Set(ctx, key, result, 0)
	return result, nil
}

// ========== 透传（不缓存）==========

func (r *cachedToolTypeRepository) List(ctx context.Context) ([]entity.ToolType, error) {
	return r.inner.List(ctx)
}

func (r *cachedToolTypeRepository) ListEnabled(ctx context.Context) ([]entity.ToolType, error) {
	return r.inner.ListEnabled(ctx)
}

func (r *cachedToolTypeRepository) ExistsByKey(ctx context.Context, toolKey string) (bool, error) {
	return r.inner.ExistsByKey(ctx, toolKey)
}
