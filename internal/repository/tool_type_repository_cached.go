package repository

import (
	"context"

	"solvify-agent/internal/model/entity"
	"solvify-agent/pkg/logger"
)

// cachedToolTypeRepository 为 ToolTypeRepository 添加 Redis 缓存层
//
//	缓存策略：写时失效
//	- 按 toolKey 查：key = "tool:type:key:{toolKey}"
//	- 按 ID 查：key = "tool:type:id:{id}"
//	- Create/Update/Delete → 清除对应缓存
//
// ⚠️ tool_key 建了独立索引，所以**任何写操作都必须同时失效 ID 索引和 key 索引**；
// 只要漏掉一个，被漏掉的那条就会一直返回脏值直到 TTL 过期（10 分钟）。
type cachedToolTypeRepository struct {
	inner ToolTypeRepository
	cache cachePort
}

// NewCachedToolTypeRepository 创建带缓存的工具类型仓库
func NewCachedToolTypeRepository(inner ToolTypeRepository, c cachePort) ToolTypeRepository {
	return &cachedToolTypeRepository{inner: inner, cache: c}
}

// ========== 写操作 ==========

func (r *cachedToolTypeRepository) Create(ctx context.Context, toolType *entity.ToolType) error {
	return r.inner.Create(ctx, toolType)
}

func (r *cachedToolTypeRepository) Update(ctx context.Context, toolType *entity.ToolType) error {
	// 先取旧实体，为的是拿到**旧** ToolKey。
	//
	// 缓存给 tool_key 建了独立索引（key:"+ToolKey），而 Update 拿到的是改过之后的新实体；
	// 只失效新 key 的话，tool_key 一旦被改名，tool:type:key:<旧key> 会一直命中脏值，
	// GetByKey(旧 key) 返回的是已经改名的实体（审查报告 P0-5）。
	//
	// 读失败就直接失败、不写库：此时无法确定该失效哪些 key，与其写成功却留下脏缓存，
	// 不如让调用方重试。口径与下面的 Delete 一致（Delete 也先 GetByID 再删）。
	//
	// 注意：当前 UpdateToolTypeRequest 没有 tool_key 字段、全仓也没有第二处给
	// ToolKey 赋值，所以「改名」今天还走不到这里 —— 但管理台的工具类型编辑弹窗
	// 把 tool_key 做成了可编辑输入框（AdminPage.vue），看上去像是能改，
	// 于是这行防御迟早会被需要。不能等那天再补。
	old, err := r.inner.GetByID(ctx, toolType.ID)
	if err != nil {
		return err
	}

	if err := r.inner.Update(ctx, toolType); err != nil {
		return err
	}

	_ = r.cache.Delete(ctx, toolTypeCacheKeyByID(toolType.ID))
	_ = r.cache.Delete(ctx, toolTypeCacheKeyByToolKey(toolType.ToolKey))
	if old.ToolKey != toolType.ToolKey {
		_ = r.cache.Delete(ctx, toolTypeCacheKeyByToolKey(old.ToolKey))
	}
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
	_ = r.cache.Delete(ctx, toolTypeCacheKeyByID(id))
	_ = r.cache.Delete(ctx, toolTypeCacheKeyByToolKey(toolType.ToolKey))
	return nil
}

// ========== 读操作：cache-aside ==========

func (r *cachedToolTypeRepository) GetByKey(ctx context.Context, toolKey string) (*entity.ToolType, error) {
	key := toolTypeCacheKeyByToolKey(toolKey)
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
	key := toolTypeCacheKeyByID(id)
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

func (r *cachedToolTypeRepository) GetProviderCounts(ctx context.Context) (map[string]int, error) {
	return r.inner.GetProviderCounts(ctx)
}
