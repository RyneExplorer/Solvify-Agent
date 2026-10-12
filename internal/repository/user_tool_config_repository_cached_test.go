package repository

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"

	"solvify-agent/internal/model/entity"
)

// ─── P0-5 回归：级联删除必须连带失效缓存 ──────────────────────────────────────
//
// 缺陷复现路径（修好之前）：
//
//	管理员删供应商 p1
//	  → tool_provider_repository.Delete 里一句 tx.Delete(&entity.UserToolConfig{}, "provider_id = ?", p1)
//	  → 数据库里用户 u1/u2 的配置没了
//	  → 但 tool:config:user:u1 还是旧的
//	  → u1 打开设置页，仍然看到那个已经不存在的工具（幽灵工具），直到 10 分钟 TTL 过期
//
// 所以本组的断言必须落在**用户看得到的结果**上：删完之后再查一次，拿到的必须是新状态，
// 而不是「Delete 被调了几次」这种实现细节。
//
// 复用同包 tool_type_repository_cached_test.go 里的 memCache（JSON 往返模拟 RedisCache）。
// 它是「会真的命中并返回旧值」的假实现，否则本组断言会变成空转。

// ── 内存版仓储：把行存成一个切片当表 ──

type memUserToolConfigRepo struct {
	mu sync.Mutex
	// rows 是「表」的全部内容；顺序不重要。
	rows []entity.UserToolConfig
	// cascadeErr 非 nil 时，两个级联删除方法直接返回它（用于测失败路径）。
	cascadeErr error
	// cascadeCalls 记录级联删除被调用的次数，用于失败路径断言「没写库」。
	cascadeCalls int
}

func newMemUserToolConfigRepo(rows ...entity.UserToolConfig) *memUserToolConfigRepo {
	m := &memUserToolConfigRepo{}
	for _, r := range rows {
		m.rows = append(m.rows, r)
	}
	return m
}

func (r *memUserToolConfigRepo) Create(_ context.Context, config *entity.UserToolConfig) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.rows = append(r.rows, *config)
	return nil
}

func (r *memUserToolConfigRepo) Update(_ context.Context, config *entity.UserToolConfig) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.rows {
		if r.rows[i].ID == config.ID {
			r.rows[i] = *config
		}
	}
	return nil
}

func (r *memUserToolConfigRepo) Delete(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removeLocked(func(c entity.UserToolConfig) bool { return c.ID == id })
	return nil
}

func (r *memUserToolConfigRepo) GetByID(_ context.Context, id string) (*entity.UserToolConfig, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.rows {
		if r.rows[i].ID == id {
			c := r.rows[i]
			return &c, nil
		}
	}
	return nil, errUserToolConfigNotFound
}

func (r *memUserToolConfigRepo) GetByUserAndToolType(_ context.Context, userID, toolTypeID string) (*entity.UserToolConfig, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.rows {
		if r.rows[i].UserID == userID && r.rows[i].ToolTypeID == toolTypeID {
			c := r.rows[i]
			return &c, nil
		}
	}
	return nil, errUserToolConfigNotFound
}

func (r *memUserToolConfigRepo) GetByUserAndProvider(_ context.Context, userID, providerID string) (*entity.UserToolConfig, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.rows {
		if r.rows[i].UserID == userID && r.rows[i].ProviderID == providerID {
			c := r.rows[i]
			return &c, nil
		}
	}
	return nil, errUserToolConfigNotFound
}

func (r *memUserToolConfigRepo) DisableOthersByToolType(_ context.Context, userID, toolTypeID, exceptID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.rows {
		c := &r.rows[i]
		if c.UserID == userID && c.ToolTypeID == toolTypeID && c.ID != exceptID {
			c.IsEnabled = false
		}
	}
	return nil
}

func (r *memUserToolConfigRepo) ListByUserID(_ context.Context, userID string) ([]entity.UserToolConfig, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []entity.UserToolConfig
	for _, c := range r.rows {
		if c.UserID == userID {
			out = append(out, c)
		}
	}
	return out, nil
}

func (r *memUserToolConfigRepo) ListEnabledByUserID(_ context.Context, userID string) ([]entity.UserToolConfig, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []entity.UserToolConfig
	for _, c := range r.rows {
		if c.UserID == userID && c.IsEnabled {
			out = append(out, c)
		}
	}
	return out, nil
}

func (r *memUserToolConfigRepo) DeleteByProviderID(_ context.Context, providerID string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cascadeCalls++
	if r.cascadeErr != nil {
		return nil, r.cascadeErr
	}
	userIDs := r.affectedLocked(func(c entity.UserToolConfig) bool { return c.ProviderID == providerID })
	r.removeLocked(func(c entity.UserToolConfig) bool { return c.ProviderID == providerID })
	return userIDs, nil
}

func (r *memUserToolConfigRepo) DeleteByToolTypeID(_ context.Context, toolTypeID string) ([]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cascadeCalls++
	if r.cascadeErr != nil {
		return nil, r.cascadeErr
	}
	userIDs := r.affectedLocked(func(c entity.UserToolConfig) bool { return c.ToolTypeID == toolTypeID })
	r.removeLocked(func(c entity.UserToolConfig) bool { return c.ToolTypeID == toolTypeID })
	return userIDs, nil
}

// ── 以下两个方法只给测试用：绕过缓存装饰器，模拟「修好之前」的行为 ──

// bypassDeleteByProvider 直接删行、**不动缓存** —— 精确复刻 tool_provider_repository
// 里那句 tx.Delete(&entity.UserToolConfig{}, ...) 的语义，用于对照组。
func (r *memUserToolConfigRepo) bypassDeleteByProvider(providerID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.removeLocked(func(c entity.UserToolConfig) bool { return c.ProviderID == providerID })
}

func (r *memUserToolConfigRepo) removeLocked(pred func(entity.UserToolConfig) bool) {
	kept := r.rows[:0]
	for _, c := range r.rows {
		if !pred(c) {
			kept = append(kept, c)
		}
	}
	r.rows = kept
}

func (r *memUserToolConfigRepo) affectedLocked(pred func(entity.UserToolConfig) bool) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range r.rows {
		if pred(c) && !seen[c.UserID] {
			seen[c.UserID] = true
			out = append(out, c.UserID)
		}
	}
	sort.Strings(out) // 去重后还要稳定，才能直接比对
	return out
}

func (r *memUserToolConfigRepo) rowCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.rows)
}

func (r *memUserToolConfigRepo) cascades() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.cascadeCalls
}

var errUserToolConfigNotFound = errors.New("user tool config not found")

// 编译期确认假实现真的满足接口（接口加方法时这里会立刻红，而不是悄悄漏测）。
var _ UserToolConfigRepository = (*memUserToolConfigRepo)(nil)

// ────────────────────────────────────────────────────────────────────────────

// 场景：u1、u2 用了供应商 p1；u3 用的是 p2。三方缓存都已灌好。
func cascadeFixture() (*memUserToolConfigRepo, *memCache, UserToolConfigRepository) {
	inner := newMemUserToolConfigRepo(
		entity.UserToolConfig{ID: "c1", UserID: "u1", ToolTypeID: "tt1", ProviderID: "p1", IsEnabled: true},
		entity.UserToolConfig{ID: "c2", UserID: "u2", ToolTypeID: "tt1", ProviderID: "p1", IsEnabled: true},
		entity.UserToolConfig{ID: "c3", UserID: "u3", ToolTypeID: "tt1", ProviderID: "p2", IsEnabled: true},
	)
	c := newMemCache()
	return inner, c, NewCachedUserToolConfigRepository(inner, c)
}

func primeUserCache(t *testing.T, repo UserToolConfigRepository, c *memCache, userID string) {
	t.Helper()
	if _, err := repo.ListEnabledByUserID(context.Background(), userID); err != nil {
		t.Fatalf("预热 %s 缓存失败: %v", userID, err)
	}
	if !c.has("user:" + userID) {
		t.Fatalf("用例前提不成立：预热之后缓存里没有 user:%s", userID)
	}
}

// 对照组：证明「缓存返回旧值」这件事真的会发生。
//
// 它同时是缺陷的复现 —— 直接删行（绕过装饰器）之后，缓存仍然返回那条已经不存在的配置。
// 缺了这条，下面「缓存已失效」的断言可能只是因为缓存压根没生效。
func TestUserToolConfig_StaleCacheSurvivesBypassDelete(t *testing.T) {
	inner, c, repo := cascadeFixture()
	ctx := context.Background()

	primeUserCache(t, repo, c, "u1")

	inner.bypassDeleteByProvider("p1") // 旧行为：删了行，没动缓存

	got, err := repo.ListEnabledByUserID(ctx, "u1")
	if err != nil {
		t.Fatalf("ListEnabledByUserID 失败: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("对照组前提不成立：期望从缓存里读回 1 条陈旧配置，实际 %d 条", len(got))
	}
	// 夹具 3 行里，p1 名下是 u1、u2 两条，删掉后应只剩 u3 那一行
	if inner.rowCount() != 1 {
		t.Fatalf("对照组前提不成立：期望数据库里只剩 1 行，实际 %d 行", inner.rowCount())
	}
}

// P0-5 核心回归：删除供应商后，受影响用户的缓存必须失效，且**看得到**新状态。
func TestCachedUserToolConfigRepository_DeleteByProviderIDInvalidatesAffectedUsers(t *testing.T) {
	inner, c, repo := cascadeFixture()
	ctx := context.Background()

	for _, uid := range []string{"u1", "u2", "u3"} {
		primeUserCache(t, repo, c, uid)
	}

	affected, err := repo.DeleteByProviderID(ctx, "p1")
	if err != nil {
		t.Fatalf("DeleteByProviderID 失败: %v", err)
	}

	if want := []string{"u1", "u2"}; !equalStrings(affected, want) {
		t.Fatalf("返回的受影响 userID = %v，期望 %v", affected, want)
	}
	if inner.rowCount() != 1 {
		t.Fatalf("数据库里应只剩 u3 那一行，实际 %d 行", inner.rowCount())
	}

	// 受影响的两个用户：缓存必须已清掉
	if c.has("user:u1") || c.has("user:u2") {
		t.Fatalf("删除后 u1/u2 的缓存仍在：u1=%v, u2=%v；实际删除过的 key=%v",
			c.has("user:u1"), c.has("user:u2"), c.deleted())
	}
	// 没被波及的用户：不做无谓失效
	if !c.has("user:u3") {
		t.Fatalf("u3 与本供应商无关，不该被清缓存；实际删除过的 key=%v", c.deleted())
	}

	// 用户看得到的结果：u1 再查一次，那条幽灵配置必须消失
	got, err := repo.ListEnabledByUserID(ctx, "u1")
	if err != nil {
		t.Fatalf("删除后查询失败: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("删除供应商后用户仍看到 %d 条已启用配置（幽灵工具）: %+v", len(got), got)
	}

	// u3 的配置不受影响，且读了缓存就应该是那条
	got3, err := repo.ListEnabledByUserID(ctx, "u3")
	if err != nil {
		t.Fatalf("查询 u3 失败: %v", err)
	}
	if len(got3) != 1 || got3[0].ID != "c3" {
		t.Fatalf("u3 的配置不该被删，实际 %+v", got3)
	}
}

func TestCachedUserToolConfigRepository_DeleteByToolTypeIDInvalidatesAffectedUsers(t *testing.T) {
	inner := newMemUserToolConfigRepo(
		entity.UserToolConfig{ID: "c1", UserID: "u1", ToolTypeID: "tt1", ProviderID: "p1", IsEnabled: true},
		entity.UserToolConfig{ID: "c2", UserID: "u2", ToolTypeID: "tt1", ProviderID: "p2", IsEnabled: true},
		entity.UserToolConfig{ID: "c3", UserID: "u3", ToolTypeID: "tt2", ProviderID: "p3", IsEnabled: true},
	)
	c := newMemCache()
	repo := NewCachedUserToolConfigRepository(inner, c)
	ctx := context.Background()

	for _, uid := range []string{"u1", "u2", "u3"} {
		primeUserCache(t, repo, c, uid)
	}

	affected, err := repo.DeleteByToolTypeID(ctx, "tt1")
	if err != nil {
		t.Fatalf("DeleteByToolTypeID 失败: %v", err)
	}
	if want := []string{"u1", "u2"}; !equalStrings(affected, want) {
		t.Fatalf("返回的受影响 userID = %v，期望 %v", affected, want)
	}
	if c.has("user:u1") || c.has("user:u2") {
		t.Fatalf("删除后 u1/u2 的缓存仍在；实际删除过的 key=%v", c.deleted())
	}
	if !c.has("user:u3") {
		t.Fatalf("u3 与本工具类型无关，不该被清缓存；实际删除过的 key=%v", c.deleted())
	}

	got, err := repo.ListEnabledByUserID(ctx, "u1")
	if err != nil {
		t.Fatalf("删除后查询失败: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("删除工具类型后用户仍看到 %d 条已启用配置: %+v", len(got), got)
	}
}

// 失败路径：删行失败必须「原样上抛 + 不动缓存」。
//
// 为什么这条重要：如果删行失败却先把缓存清了，用户会看到「工具没了」（缓存空）
// 而数据库里其实还在 —— 那是比脏缓存更难查的假象。
func TestCachedUserToolConfigRepository_CascadeFailureLeavesCacheUntouched(t *testing.T) {
	inner, c, repo := cascadeFixture()
	inner.cascadeErr = errors.New("数据库暂时不可用")
	ctx := context.Background()

	primeUserCache(t, repo, c, "u1")

	if _, err := repo.DeleteByProviderID(ctx, "p1"); err == nil {
		t.Fatal("删行失败时 DeleteByProviderID 应当返回错误")
	} else if !errors.Is(err, inner.cascadeErr) {
		t.Fatalf("原始错误必须留在链上，实际 %v", err)
	}
	if !c.has("user:u1") {
		t.Fatalf("删行失败时不该动缓存；实际删除过的 key=%v", c.deleted())
	}
	if len(c.deleted()) != 0 {
		t.Fatalf("删行失败时不该有任何缓存删除，实际 = %v", c.deleted())
	}
	if inner.rowCount() != 3 {
		t.Fatalf("删行失败时不该有行被删，实际剩 %d 行", inner.rowCount())
	}
	// 反向：错误必须能从装饰器穿透出来（别被吞掉）
	if inner.cascades() != 1 {
		t.Fatalf("级联删除应被调用恰好 1 次，实际 %d 次", inner.cascades())
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
