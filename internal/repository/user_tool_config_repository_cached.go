package repository

import (
	"context"

	"solvify-agent/internal/model/entity"
	"solvify-agent/pkg/logger"
)

// cachedUserToolConfigRepository 为 UserToolConfigRepository 添加 Redis 缓存层
//
//	缓存策略：写时失效
//	- 按 userID 查已启用配置：key = "tool:config:user:{userID}"
//	- 按 ID 查单条：key = "tool:config:id:{id}"
//	- Create/Update/Delete → 清除对应用户缓存
//
// ⚠️ 本表还会被「级联删除」波及（删供应商 / 删工具类型时连带删用户配置），
// 那些入口同样收在本层（DeleteByProviderID / DeleteByToolTypeID）。
// 这是 P0-5 的收口：此前级联写在 tool_provider_repository / tool_type_repository 里，
// 直删本表却不失效缓存，用户已启用的工具在被管理员删掉供应商后，仍会以「幽灵工具」
// 形式出现，直到 10 分钟 TTL 过期。
type cachedUserToolConfigRepository struct {
	inner UserToolConfigRepository
	cache cachePort
}

// NewCachedUserToolConfigRepository 创建带缓存的用户工具配置仓库
func NewCachedUserToolConfigRepository(inner UserToolConfigRepository, c cachePort) UserToolConfigRepository {
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
	_ = r.cache.Delete(ctx, userToolConfigCacheKeyByID(config.ID))
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
	_ = r.cache.Delete(ctx, userToolConfigCacheKeyByID(id))
	return nil
}

// ========== 读操作：cache-aside ==========

func (r *cachedUserToolConfigRepository) GetByID(ctx context.Context, id string) (*entity.UserToolConfig, error) {
	key := userToolConfigCacheKeyByID(id)
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
	key := userToolConfigCacheKeyByUser(userID)
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

func (r *cachedUserToolConfigRepository) GetByUserAndProvider(ctx context.Context, userID, providerID string) (*entity.UserToolConfig, error) {
	return r.inner.GetByUserAndProvider(ctx, userID, providerID)
}

func (r *cachedUserToolConfigRepository) DisableOthersByToolType(ctx context.Context, userID, toolTypeID, exceptID string) error {
	if err := r.inner.DisableOthersByToolType(ctx, userID, toolTypeID, exceptID); err != nil {
		return err
	}
	r.invalidateUser(ctx, userID)
	return nil
}

// ========== 级联删除：删行与失效必须成对 ==========

func (r *cachedUserToolConfigRepository) DeleteByProviderID(ctx context.Context, providerID string) ([]string, error) {
	userIDs, err := r.inner.DeleteByProviderID(ctx, providerID)
	if err != nil {
		return nil, err
	}
	r.invalidateUsers(ctx, userIDs)
	return userIDs, nil
}

func (r *cachedUserToolConfigRepository) DeleteByToolTypeID(ctx context.Context, toolTypeID string) ([]string, error) {
	userIDs, err := r.inner.DeleteByToolTypeID(ctx, toolTypeID)
	if err != nil {
		return nil, err
	}
	r.invalidateUsers(ctx, userIDs)
	return userIDs, nil
}

// ========== 缓存失效 ==========

func (r *cachedUserToolConfigRepository) invalidateUser(ctx context.Context, userID string) {
	key := userToolConfigCacheKeyByUser(userID)
	if err := r.cache.Delete(ctx, key); err != nil {
		logger.Warnf("工具配置缓存清除失败: userID=%s, err=%v", userID, err)
	}
}

// invalidateUsers 批量失效：级联删除一次会波及多个用户。
//
// 失效紧跟删行之后、不等事务提交。方向上是安全的：若事务最终回滚，只是白删了几个 key
// （下次读会回填）；真正危险的是反方向——漏失效，那会留下脏值到 TTL 过期。
func (r *cachedUserToolConfigRepository) invalidateUsers(ctx context.Context, userIDs []string) {
	for _, userID := range userIDs {
		r.invalidateUser(ctx, userID)
	}
}
