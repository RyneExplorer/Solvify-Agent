package repository

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"solvify-agent/internal/model/entity"
)

// ─── P0-5 回归测试：缓存装饰器的「写时失效」必须同时覆盖 key 索引 ──────────────
//
// 背景：cachedToolTypeRepository 给 tool_key 建了**独立**的缓存索引
// （tool:type:key:<toolKey>，见文件头注释），所以任何写操作都必须同时失效
// ID 索引和 key 索引。此前 Update 只删「新」ToolKey 那一份：
//
//	_ = r.cache.Delete(ctx, "key:"+toolType.ToolKey)   // toolType 是改过之后的实体
//
// 于是 tool_key 一旦被改名，tool:type:key:<旧key> 会一直命中脏值直到 TTL（10 分钟）
// 过期，GetByKey(旧 key) 返回的是已经改名的实体（审查报告 P0-5）。
//
// 这组用例故意做成**行为断言**（「按旧 key 查必须查不到」），而不是「Delete 被调了几次」
// —— 后者会把实现细节钉死，前者才是用户能看到的结果。
//
// 另外注意 TestMemCache_HitsCachedValue 这条**对照组**：它证明这套内存假实现真的会
// 命中缓存并返回旧值。缺了它，下面所有「缓存已失效」的断言都可能是空转（缓存压根没被写过）。

// ── 内存版缓存：用 JSON 往返模拟 *cache.RedisCache 的序列化语义 ──

type memCache struct {
	mu       sync.Mutex
	data     map[string][]byte
	getHits  int      // 命中次数，用于确认缓存真的被读到
	delCalls []string // 记录删除过的 key，便于失败时给出可读线索
}

func newMemCache() *memCache { return &memCache{data: make(map[string][]byte)} }

func (c *memCache) Get(_ context.Context, key string, dest any) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	raw, ok := c.data[key]
	if !ok {
		return false, nil
	}
	c.getHits++
	if err := json.Unmarshal(raw, dest); err != nil {
		return false, err
	}
	return true, nil
}

func (c *memCache) Set(_ context.Context, key string, value any, _ time.Duration) error {
	raw, err := json.Marshal(value) // 与 RedisCache 一致：存序列化后的字节
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.data[key] = raw
	return nil
}

func (c *memCache) Delete(_ context.Context, key string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.data, key)
	c.delCalls = append(c.delCalls, key)
	return nil
}

func (c *memCache) has(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.data[key]
	return ok
}

func (c *memCache) hits() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.getHits
}

func (c *memCache) deleted() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.delCalls...)
}

// ── 内存版仓储：只实现本组用例会走到的方法 ──

var errToolTypeNotFound = errors.New("tool type not found")

type memToolTypeRepo struct {
	mu          sync.Mutex
	byID        map[string]*entity.ToolType
	getByIDErr  error // 注入预读失败
	updateCalls int
}

func newMemToolTypeRepo(types ...*entity.ToolType) *memToolTypeRepo {
	m := &memToolTypeRepo{byID: make(map[string]*entity.ToolType)}
	for _, tt := range types {
		m.byID[tt.ID] = cloneToolType(tt)
	}
	return m
}

func cloneToolType(t *entity.ToolType) *entity.ToolType {
	c := *t
	return &c
}

func (r *memToolTypeRepo) GetByID(_ context.Context, id string) (*entity.ToolType, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.getByIDErr != nil {
		return nil, r.getByIDErr
	}
	tt, ok := r.byID[id]
	if !ok {
		return nil, errToolTypeNotFound
	}
	return cloneToolType(tt), nil
}

func (r *memToolTypeRepo) GetByKey(_ context.Context, toolKey string) (*entity.ToolType, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, tt := range r.byID {
		if tt.ToolKey == toolKey {
			return cloneToolType(tt), nil
		}
	}
	return nil, errToolTypeNotFound
}

func (r *memToolTypeRepo) Update(_ context.Context, toolType *entity.ToolType) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.updateCalls++
	r.byID[toolType.ID] = cloneToolType(toolType)
	return nil
}

func (r *memToolTypeRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byID, id)
	return nil
}

func (r *memToolTypeRepo) Create(_ context.Context, toolType *entity.ToolType) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[toolType.ID] = cloneToolType(toolType)
	return nil
}

func (r *memToolTypeRepo) List(context.Context) ([]entity.ToolType, error) { return nil, nil }
func (r *memToolTypeRepo) ListEnabled(context.Context) ([]entity.ToolType, error) {
	return nil, nil
}
func (r *memToolTypeRepo) ExistsByKey(context.Context, string) (bool, error)         { return false, nil }
func (r *memToolTypeRepo) GetProviderCounts(context.Context) (map[string]int, error) { return nil, nil }

// mutateName 绕过仓储直接改内存里的实体，用于对照组构造「缓存里是旧值」的场景。
func (r *memToolTypeRepo) mutateName(id, name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if tt, ok := r.byID[id]; ok {
		tt.Name = name
	}
}

func (r *memToolTypeRepo) updates() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.updateCalls
}

// ────────────────────────────────────────────────────────────────────────────

// 对照组：证明这套假缓存真会命中并返回旧值。
// 若这条不成立，下面所有「已失效」断言都是空转。
func TestMemCache_HitsCachedValue(t *testing.T) {
	inner := newMemToolTypeRepo(&entity.ToolType{ID: "t1", ToolKey: "k1", Name: "原名"})
	c := newMemCache()
	repo := NewCachedToolTypeRepository(inner, c)
	ctx := context.Background()

	if _, err := repo.GetByKey(ctx, "k1"); err != nil {
		t.Fatalf("首次 GetByKey 失败: %v", err)
	}
	inner.mutateName("t1", "被绕过仓储改掉的名字") // 不动缓存

	got, err := repo.GetByKey(ctx, "k1")
	if err != nil {
		t.Fatalf("二次 GetByKey 失败: %v", err)
	}
	if got.Name != "原名" {
		t.Fatalf("缓存未被命中（拿到 %q）——本组用例的断言将失去意义", got.Name)
	}
	if c.hits() == 0 {
		t.Fatal("缓存 Get 从未命中过")
	}
}

// P0-5 核心回归：改名后旧 key 的缓存必须失效。
func TestCachedToolTypeRepository_UpdateInvalidatesOldKeyOnRename(t *testing.T) {
	inner := newMemToolTypeRepo(&entity.ToolType{ID: "t1", ToolKey: "old-key", Name: "原名"})
	c := newMemCache()
	repo := NewCachedToolTypeRepository(inner, c)
	ctx := context.Background()

	// 先按 key 读一次，把 key:old-key 灌进缓存
	if _, err := repo.GetByKey(ctx, "old-key"); err != nil {
		t.Fatalf("首次 GetByKey 失败: %v", err)
	}
	if !c.has("key:old-key") {
		t.Fatal("用例前提不成立：按 key 读之后缓存里没有 key:old-key")
	}

	// 改名
	if err := repo.Update(ctx, &entity.ToolType{ID: "t1", ToolKey: "new-key", Name: "原名"}); err != nil {
		t.Fatalf("Update 失败: %v", err)
	}

	if c.has("key:old-key") {
		t.Fatalf("改名后 tool:type:key:old-key 仍在缓存里，会返回脏值到 TTL 过期；实际删除过的 key = %v", c.deleted())
	}
	// 行为断言：按旧 key 必须查不到（而不是拿到改名后的实体）
	if got, err := repo.GetByKey(ctx, "old-key"); err == nil {
		t.Fatalf("改名后按旧 key 仍能查到工具类型: %+v", got)
	}
	// 新 key 必须可查
	got, err := repo.GetByKey(ctx, "new-key")
	if err != nil {
		t.Fatalf("改名后按新 key 查询失败: %v", err)
	}
	if got.ToolKey != "new-key" || got.Name != "原名" {
		t.Fatalf("按新 key 查到 %+v，期望 ToolKey=new-key、Name=原名", got)
	}
}

// key 未变时也必须失效，否则改 Name 之后仍会读到旧名字。
func TestCachedToolTypeRepository_UpdateInvalidatesWhenKeyUnchanged(t *testing.T) {
	inner := newMemToolTypeRepo(&entity.ToolType{ID: "t1", ToolKey: "k1", Name: "旧名字"})
	c := newMemCache()
	repo := NewCachedToolTypeRepository(inner, c)
	ctx := context.Background()

	if _, err := repo.GetByKey(ctx, "k1"); err != nil {
		t.Fatalf("首次 GetByKey 失败: %v", err)
	}

	if err := repo.Update(ctx, &entity.ToolType{ID: "t1", ToolKey: "k1", Name: "新名字"}); err != nil {
		t.Fatalf("Update 失败: %v", err)
	}

	got, err := repo.GetByKey(ctx, "k1")
	if err != nil {
		t.Fatalf("Update 后 GetByKey 失败: %v", err)
	}
	if got.Name != "新名字" {
		t.Fatalf("Update 后仍读到旧值 %q，缓存 key 未失效", got.Name)
	}
}

// Delete 的既有行为（也先查实体拿旧 key）不应被回归。
func TestCachedToolTypeRepository_DeleteInvalidatesKey(t *testing.T) {
	inner := newMemToolTypeRepo(&entity.ToolType{ID: "t1", ToolKey: "k1", Name: "待删"})
	c := newMemCache()
	repo := NewCachedToolTypeRepository(inner, c)
	ctx := context.Background()

	if _, err := repo.GetByKey(ctx, "k1"); err != nil {
		t.Fatalf("首次 GetByKey 失败: %v", err)
	}
	if _, err := repo.GetByID(ctx, "t1"); err != nil {
		t.Fatalf("首次 GetByID 失败: %v", err)
	}

	if err := repo.Delete(ctx, "t1"); err != nil {
		t.Fatalf("Delete 失败: %v", err)
	}

	if c.has("key:k1") || c.has("id:t1") {
		t.Fatalf("删除后缓存未清干净：key:k1=%v, id:t1=%v", c.has("key:k1"), c.has("id:t1"))
	}
	if _, err := repo.GetByKey(ctx, "k1"); err == nil {
		t.Fatal("删除后按 key 仍能查到")
	}
}

// 预读失败时必须「不写库 + 不留下半失效的缓存」，让调用方重试。
// 这条锁住的是修复引入的新语义：拿不到旧 ToolKey 就无法确定该失效哪些 key。
func TestCachedToolTypeRepository_UpdateStopsWhenPreReadFails(t *testing.T) {
	inner := newMemToolTypeRepo(&entity.ToolType{ID: "t1", ToolKey: "k1", Name: "原名"})
	inner.getByIDErr = errors.New("数据库暂时不可用")
	c := newMemCache()
	repo := NewCachedToolTypeRepository(inner, c)
	ctx := context.Background()

	if _, err := repo.GetByKey(ctx, "k1"); err != nil {
		t.Fatalf("首次 GetByKey 失败: %v", err)
	}

	err := repo.Update(ctx, &entity.ToolType{ID: "t1", ToolKey: "k2", Name: "原名"})
	if err == nil {
		t.Fatal("预读失败时 Update 应当返回错误")
	}
	if inner.updates() != 0 {
		t.Fatal("预读失败时不该写库 —— 否则会写成功却留下脏缓存")
	}
	if !c.has("key:k1") {
		t.Fatal("预读失败时不该动缓存：本次操作没有生效，缓存应保持原状")
	}
	if len(c.deleted()) != 0 {
		t.Fatalf("预读失败时不该有任何缓存删除，实际 = %v", c.deleted())
	}
}
