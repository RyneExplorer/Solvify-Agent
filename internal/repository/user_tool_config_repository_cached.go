package repository

import (
	"context"
	"fmt"

	"solvify-agent/internal/model/entity"
	"solvify-agent/pkg/cache"
	"solvify-agent/pkg/logger"
)

// cachedUserToolConfigRepository 为 UserToolConfigRepository 添加 Redis 缓存层
//
//	缓存策略：写时失效
//	- 按 userID 查已启用配置：key = "tool:config:{userID}"
//	- 按 ID 查单条：key = "tool:config:id:{id}"
//	- Create/Update/Delete → 清除对应用户缓存
type cachedUserToolConfigRepository struct {
	inner UserToolConfigRepository
	cache *cache.RedisCache
}

// NewCachedUserToolConfigRepository 创建带缓存的用户工具配置仓库
func NewCachedUserToolConfigRepository(inner UserToolConfigRepository, c *cache.RedisCache) UserToolConfigRepository {
	return &cachedUserToolConfigRepository{inner: inner, cache: c}
}

// ========== 写操作：穿透写 DB，然后删缓存 ==========

func (r *cachedUserToolConfigRepository) Create(ctx context.Context, config *entity.UserToolConfig) error {
	if err := r.inner.Create(ctx, config); err != nil {
		return err
	}
	r.invalidateUser(ctx, config.UserID)
	return nil
}

func (r *cachedUserToolConfigRepository) Update(ctx context.Context, config *entity.UserToolConfig) error {
	if err := r.inner.Update(ctx, config); err != nil {
		return err
	}
	r.invalidateUser(ctx, config.UserID)
	_ = r.cache.Delete(ctx, "id:"+config.ID)
	return nil
}

func (r *cachedUserToolConfigRepository) Delete(ctx context.Context, id string) error {
	// 先查出 userID 用于缓存失效
	config, err := r.inner.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := r.inner.Delete(ctx, id); err != nil {
		return err
	}
	r.invalidateUser(ctx, config.UserID)
	_ = r.cache.Delete(ctx, "id:"+id)
	return nil
}

// ========== 读操作：cache-aside ==========

func (r *cachedUserToolConfigRepository) GetByID(ctx context.Context, id string) (*entity.UserToolConfig, error) {
	key := "id:" + id
	var config entity.UserToolConfig
	if found, _ := r.cache.Get(ctx, key, &config); found {
		return &config, nil
	}
	result, err := r.inner.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	_ = r.cache.Set(ctx, key, result, 0)
	return result, nil
}

func (r *cachedUserToolConfigRepository) ListEnabledByUserID(ctx context.Context, userID string) ([]entity.UserToolConfig, error) {
	key := fmt.Sprintf("user:%s", userID)
	var configs []entity.UserToolConfig
	if found, _ := r.cache.Get(ctx, key, &configs); found {
		return configs, nil
	}
	result, err := r.inner.ListEnabledByUserID(ctx, userID)
	if err != nil {
		return nil, err
	}
	_ = r.cache.Set(ctx, key, result, 0)
	return result, nil
}

// ========== 透传（不缓存）==========

func (r *cachedUserToolConfigRepository) ListByUserID(ctx context.Context, userID string) ([]entity.UserToolConfig, error) {
	return r.inner.ListByUserID(ctx, userID)
}

func (r *cachedUserToolConfigRepository) GetByUserAndToolType(ctx context.Context, userID, toolTypeID string) (*entity.UserToolConfig, error) {
	return r.inner.GetByUserAndToolType(ctx, userID, toolTypeID)
}

// ========== 缓存失效 ==========

func (r *cachedUserToolConfigRepository) invalidateUser(ctx context.Context, userID string) {
	key := fmt.Sprintf("user:%s", userID)
	if err := r.cache.Delete(ctx, key); err != nil {
		logger.Warnf("工具配置缓存清除失败: userID=%s, err=%v", userID, err)
	}
}
