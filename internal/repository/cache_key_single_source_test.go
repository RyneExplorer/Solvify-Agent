package repository

import (
	"context"
	"errors"
	"sync"
	"testing"

	"solvify-agent/internal/model/entity"
)

// ─── P1-5 回归：读路径写入的 key，必须就是写路径删掉的那个 key ────────────────
//
// 这里断言的是「同一个 key」这条不变量，而不是某个具体字符串：用例里一律用
// cache_keys.go 的构造函数取 key，任何「读用 A、失效删 B」的偏离都会让断言落空。
//
// 每条用例都做成三段式，缺一段就会退化成空转：
//  1. 首读 → 断言缓存里确实出现了这个 key（读路径真的用了它）
//  2. 复读 → 断言**没有**再查库（缓存真的命中，而不是每次都穿透）
//  3. 写   → 断言该 key 消失 + 复读拿到新值（失效真的指向同一个 key）
//
// ⚠️ 覆盖缺口：改动前 model / user_model_config 两个装饰器**一条用例都没有**，
// 它们的失效逻辑坏了也不会有人发现。本文件补上。

// ── 计数的假仓储 ──

var errMemModelNotFound = errors.New("model not found")

type countingModelRepo struct {
	mu    sync.Mutex
	rows  map[string]*entity.Model
	reads int
}

func newCountingModelRepo(models ...*entity.Model) *countingModelRepo {
	r := &countingModelRepo{rows: make(map[string]*entity.Model)}
	for _, m := range models {
		cp := *m
		r.rows[m.ID] = &cp
	}
	return r
}

func (r *countingModelRepo) readCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reads
}

func (r *countingModelRepo) Create(context.Context, *entity.Model) error { return nil }

func (r *countingModelRepo) Update(_ context.Context, model *entity.Model) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *model
	r.rows[model.ID] = &cp
	return nil
}

func (r *countingModelRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.rows, id)
	return nil
}

func (r *countingModelRepo) List(context.Context) ([]entity.Model, error) { return nil, nil }

func (r *countingModelRepo) GetByID(_ context.Context, id string) (*entity.Model, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads++
	m, ok := r.rows[id]
	if !ok {
		return nil, errMemModelNotFound
	}
	cp := *m
	return &cp, nil
}

func (r *countingModelRepo) ExistsByModelID(context.Context, string, string) (bool, error) {
	return false, nil
}

var errMemUserModelConfigNotFound = errors.New("user model config not found")

type countingUserModelConfigRepo struct {
	mu    sync.Mutex
	rows  map[string]*entity.UserModelConfig
	reads int
}

func newCountingUserModelConfigRepo(cfgs ...*entity.UserModelConfig) *countingUserModelConfigRepo {
	r := &countingUserModelConfigRepo{rows: make(map[string]*entity.UserModelConfig)}
	for _, c := range cfgs {
		cp := *c
		r.rows[c.ID] = &cp
	}
	return r
}

func (r *countingUserModelConfigRepo) readCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reads
}

func (r *countingUserModelConfigRepo) Create(context.Context, *entity.UserModelConfig) error {
	return nil
}

func (r *countingUserModelConfigRepo) Update(_ context.Context, cfg *entity.UserModelConfig) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := *cfg
	r.rows[cfg.ID] = &cp
	return nil
}

func (r *countingUserModelConfigRepo) Delete(_ context.Context, id string, _ string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.rows, id)
	return nil
}

func (r *countingUserModelConfigRepo) GetByID(_ context.Context, id string, _ string) (*entity.UserModelConfig, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads++
	c, ok := r.rows[id]
	if !ok {
		return nil, errMemUserModelConfigNotFound
	}
	cp := *c
	return &cp, nil
}

func (r *countingUserModelConfigRepo) ListByUserID(context.Context, string) ([]entity.UserModelConfig, error) {
	return nil, nil
}

func (r *countingUserModelConfigRepo) ExistsByModelID(context.Context, string, string, string) (bool, error) {
	return false, nil
}

var _ ModelRepo = (*countingModelRepo)(nil)
var _ UserModelConfigRepo = (*countingUserModelConfigRepo)(nil)

// ── 系统模型 ──

func TestCachedModelRepository_ReadKeyIsTheOneInvalidated(t *testing.T) {
	ctx := context.Background()
	cache := newMemCache()
	inner := newCountingModelRepo(&entity.Model{ID: "m1", ModelID: "gpt-x", Name: "old"})
	repo := NewCachedModelRepository(inner, cache)
	key := modelCacheKeyByID("m1")

	// 1) 首读回填
	if _, err := repo.GetByID(ctx, "m1"); err != nil {
		t.Fatalf("首读失败: %v", err)
	}
	if !cache.has(key) {
		t.Fatalf("读路径没有按 %q 回填缓存（已删除过的 key=%v）", key, cache.deleted())
	}

	// 2) 复读必须命中缓存
	before := inner.readCount()
	if _, err := repo.GetByID(ctx, "m1"); err != nil {
		t.Fatalf("复读失败: %v", err)
	}
	if inner.readCount() != before {
		t.Fatalf("复读仍在查库（%d→%d），缓存没有真正命中，后面的「已失效」断言会变成空转",
			before, inner.readCount())
	}

	// 3) Update 必须删掉读路径用的那个 key
	if err := repo.Update(ctx, &entity.Model{ID: "m1", ModelID: "gpt-x", Name: "new"}); err != nil {
		t.Fatalf("Update 失败: %v", err)
	}
	if cache.has(key) {
		t.Fatalf("Update 之后 %q 仍在缓存里 —— 读 key 与失效 key 不是同一个来源", key)
	}
	got, err := repo.GetByID(ctx, "m1")
	if err != nil {
		t.Fatalf("Update 后复读失败: %v", err)
	}
	if got.Name != "new" {
		t.Fatalf("拿到脏值：name=%q，期望 \"new\"", got.Name)
	}

	// 4) Delete 同样要删这个 key
	if !cache.has(key) {
		t.Fatalf("复读未回填 %q，无法继续验证 Delete 的失效", key)
	}
	if err := repo.Delete(ctx, "m1"); err != nil {
		t.Fatalf("Delete 失败: %v", err)
	}
	if cache.has(key) {
		t.Fatalf("Delete 之后 %q 仍在缓存里 —— 已删除的模型会继续被读到", key)
	}
}

// ── 用户模型配置 ──

func TestCachedUserModelConfigRepository_ReadKeyIsTheOneInvalidated(t *testing.T) {
	ctx := context.Background()
	cache := newMemCache()
	inner := newCountingUserModelConfigRepo(&entity.UserModelConfig{
		ID: "c1", UserID: "u1", ModelID: "m1", APIFormat: "openai",
	})
	repo := NewCachedUserModelConfigRepository(inner, cache)
	key := userModelConfigCacheKeyByID("c1", "u1")

	if _, err := repo.GetByID(ctx, "c1", "u1"); err != nil {
		t.Fatalf("首读失败: %v", err)
	}
	if !cache.has(key) {
		t.Fatalf("读路径没有按 %q 回填缓存（已删除过的 key=%v）", key, cache.deleted())
	}

	before := inner.readCount()
	if _, err := repo.GetByID(ctx, "c1", "u1"); err != nil {
		t.Fatalf("复读失败: %v", err)
	}
	if inner.readCount() != before {
		t.Fatalf("复读仍在查库（%d→%d），缓存没有真正命中", before, inner.readCount())
	}

	if err := repo.Update(ctx, &entity.UserModelConfig{
		ID: "c1", UserID: "u1", ModelID: "m2", APIFormat: "openai",
	}); err != nil {
		t.Fatalf("Update 失败: %v", err)
	}
	if cache.has(key) {
		t.Fatalf("Update 之后 %q 仍在缓存里 —— 用户会继续用旧模型配置（含旧 BaseURL/APIKey）", key)
	}
	got, err := repo.GetByID(ctx, "c1", "u1")
	if err != nil {
		t.Fatalf("Update 后复读失败: %v", err)
	}
	if got.ModelID != "m2" {
		t.Fatalf("拿到脏值：model_id=%q，期望 \"m2\"", got.ModelID)
	}

	if !cache.has(key) {
		t.Fatalf("复读未回填 %q，无法继续验证 Delete 的失效", key)
	}
	if err := repo.Delete(ctx, "c1", "u1"); err != nil {
		t.Fatalf("Delete 失败: %v", err)
	}
	if cache.has(key) {
		t.Fatalf("Delete 之后 %q 仍在缓存里 —— 已删除的配置会继续被读到", key)
	}
}
