package llm

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// resetClientCache 把全局缓存换成全新实例，避免用例之间互相污染。
// 注意：全局缓存是进程级单例，所以本包测试不要用 t.Parallel()。
func resetClientCache(t *testing.T) {
	t.Helper()
	llmClientCache = newClientCache(defaultClientCacheCapacity)
	t.Cleanup(func() {
		llmClientCache = newClientCache(defaultClientCacheCapacity)
	})
}

// testModelConfig 造一份可用的模型配置，mutate 用于按用例改字段。
func testModelConfig(mutate func(*ModelConfig)) ModelConfig {
	cfg := ModelConfig{
		Provider:         "deepseek",
		ModelID:          "test-model",
		BaseURL:          "https://example.invalid/v1",
		APIKey:           "sk-test-key",
		MaxContextLength: 8192,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	return cfg
}

// newTestClient 构造一个真实客户端用于缓存用例（只构造，不发起网络请求）。
func newTestClient(t *testing.T, model string) *OpenAIClient {
	t.Helper()
	client, err := NewOpenAIClient(context.Background(), OpenAIClientConfig{
		APIKey:  "sk-test-key",
		BaseURL: "https://example.invalid/v1",
		Model:   model,
	})
	if err != nil {
		t.Fatalf("构造测试客户端失败(model=%s): %v", model, err)
	}
	return client
}

// TestPrewarmThenRequestHitsSameClient 是 P1-8 的真回归。
//
// 原先预热用 SystemModelInfo（ModelConfig 的子集，漏了 Config）并自己手拼了一份
// 缓存 key，而查询路径的 key 含 Config → Config 非空的模型，预热写入的条目
// 永远命中不到（Config 为空的模型碰巧能命中，所以长期表现成「有时候有效」）。
func TestPrewarmThenRequestHitsSameClient(t *testing.T) {
	resetClientCache(t)
	cfg := testModelConfig(func(c *ModelConfig) {
		c.Config = []byte(`{"temperature":0.3}`)
	})

	PrewarmClients(context.Background(), []ModelConfig{cfg})

	// ① 预热必须写进「请求路径会去查的那个 key」
	warmed, ok := llmClientCache.Get(cfg.openAIConfig().cacheKey())
	if !ok {
		t.Fatal("预热未写入请求路径查询的 key：预热 key 与查询 key 口径不一致（P1-8 回归）")
	}

	// ② 请求路径必须复用预热好的那个实例，而不是新建一个
	before := llmClientCache.Len()
	got, err := NewClientFromModelConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewClientFromModelConfig 失败: %v", err)
	}
	if got != warmed {
		t.Error("请求路径返回的不是预热好的客户端实例，预热形同虚设")
	}
	if after := llmClientCache.Len(); after != before {
		t.Errorf("请求路径新建了客户端（缓存条目 %d → %d），预热未生效", before, after)
	}
}

// TestPrewarmCoversEveryConfiguredModel 保证预热不会静默漏掉某个模型。
func TestPrewarmCoversEveryConfiguredModel(t *testing.T) {
	resetClientCache(t)
	models := []ModelConfig{
		testModelConfig(func(c *ModelConfig) { c.ModelID = "m-empty-config" }),
		testModelConfig(func(c *ModelConfig) {
			c.ModelID = "m-with-config"
			c.Config = []byte(`{"timeout":30}`)
		}),
	}
	PrewarmClients(context.Background(), models)

	if got := llmClientCache.Len(); got != len(models) {
		t.Errorf("预热后缓存条目数=%d，期望 %d（Config 非空的模型此前会另占一个 key）", got, len(models))
	}
}

// TestClientCacheKeyCoversAllClientConfigFields 是结构守卫：
// 给 OpenAIClientConfig 加字段却忘了加进缓存 key，会让两份不同配置共用同一个客户端 ——
// 这个坑真实发生过（漏了 Config 与 MaxContextLength），所以在这里钉死。
func TestClientCacheKeyCoversAllClientConfigFields(t *testing.T) {
	cfgType := reflect.TypeOf(OpenAIClientConfig{})
	keyType := reflect.TypeOf(clientCacheKey{})

	if cfgType.NumField() != keyType.NumField() {
		t.Fatalf("clientCacheKey 有 %d 个字段、OpenAIClientConfig 有 %d 个：给 OpenAIClientConfig 加字段时必须同步加进缓存 key",
			keyType.NumField(), cfgType.NumField())
	}
	for i := 0; i < cfgType.NumField(); i++ {
		if got, want := keyType.Field(i).Name, cfgType.Field(i).Name; got != want {
			t.Errorf("第 %d 个字段不一致：clientCacheKey.%s vs OpenAIClientConfig.%s（需同名同序）",
				i, got, want)
		}
	}
}

// TestClientCacheKeySeparatesConstructionInputs 保证「每一个影响客户端构造的字段」都进了 key。
// 与上一条互补：上一条防「新增字段忘了加」，这条防「字段加了但没被正确派生」。
func TestClientCacheKeySeparatesConstructionInputs(t *testing.T) {
	base := OpenAIClientConfig{
		APIKey:           "sk-a",
		BaseURL:          "https://example.invalid/v1",
		Model:            "m",
		Config:           []byte(`{"temperature":0.1}`),
		MaxContextLength: 8192,
	}
	baseKey := base.cacheKey()

	cases := []struct {
		name string
		cfg  OpenAIClientConfig
	}{
		{"APIKey", func() OpenAIClientConfig { c := base; c.APIKey = "sk-b"; return c }()},
		{"BaseURL", func() OpenAIClientConfig { c := base; c.BaseURL = "https://other.invalid/v1"; return c }()},
		{"Model", func() OpenAIClientConfig { c := base; c.Model = "m2"; return c }()},
		{"Config", func() OpenAIClientConfig { c := base; c.Config = []byte(`{"temperature":0.9}`); return c }()},
		{"MaxContextLength", func() OpenAIClientConfig { c := base; c.MaxContextLength = 32768; return c }()},
	}
	for _, tc := range cases {
		if tc.cfg.cacheKey() == baseKey {
			t.Errorf("%s 不同时缓存 key 未变化 → 两份不同配置会共用同一个客户端", tc.name)
		}
	}
}

// TestClientCacheEvictsLeastRecentlyUsed 覆盖「缓存无上限」的修复。
func TestClientCacheEvictsLeastRecentlyUsed(t *testing.T) {
	c := newClientCache(2)
	k1 := OpenAIClientConfig{Model: "m1"}.cacheKey()
	k2 := OpenAIClientConfig{Model: "m2"}.cacheKey()
	k3 := OpenAIClientConfig{Model: "m3"}.cacheKey()

	c.Put(k1, newTestClient(t, "m1"))
	c.Put(k2, newTestClient(t, "m2"))
	if _, ok := c.Get(k1); !ok {
		t.Fatal("k1 应命中")
	}
	// 上一步触碰了 k1 → k2 变成最久未使用
	c.Put(k3, newTestClient(t, "m3"))

	if got := c.Len(); got != 2 {
		t.Fatalf("容量上限未生效：Len=%d，期望 2", got)
	}
	if _, ok := c.Get(k1); !ok {
		t.Error("最近使用过的 k1 被误淘汰")
	}
	if _, ok := c.Get(k2); ok {
		t.Error("最久未使用的 k2 应被淘汰")
	}
	if _, ok := c.Get(k3); !ok {
		t.Error("新写入的 k3 应存在")
	}
}

// TestClientCacheOverwritesSameKey 保证同 key 重写不会堆积条目。
func TestClientCacheOverwritesSameKey(t *testing.T) {
	c := newClientCache(2)
	key := OpenAIClientConfig{Model: "m"}.cacheKey()
	first := newTestClient(t, "m")
	second := newTestClient(t, "m")

	c.Put(key, first)
	c.Put(key, second)

	if got := c.Len(); got != 1 {
		t.Fatalf("同 key 覆盖不应新增条目：Len=%d", got)
	}
	got, ok := c.Get(key)
	if !ok || got != second {
		t.Error("同 key 写入应覆盖为最新实例")
	}
}

// TestNewClientCacheFallsBackToDefaultCapacity 保证非法容量不会退化成「无上限」。
func TestNewClientCacheFallsBackToDefaultCapacity(t *testing.T) {
	for _, in := range []int{0, -5} {
		if got := newClientCache(in).capacity; got != defaultClientCacheCapacity {
			t.Errorf("newClientCache(%d).capacity = %d，期望 %d", in, got, defaultClientCacheCapacity)
		}
	}
}

// TestClientCacheBoundedWhenAPIKeyRotates 直接对应缺陷描述：
// 「key 里含明文 APIKey，用户轮换密钥后旧 client 常驻内存，选得越多长得越大」。
// 旧实现是 sync.Map，下面这 500 次轮换会留下 500 个条目，永不释放。
func TestClientCacheBoundedWhenAPIKeyRotates(t *testing.T) {
	resetClientCache(t)

	const rotations = 500
	for i := 0; i < rotations; i++ {
		cfg := testModelConfig(func(c *ModelConfig) {
			c.APIKey = fmt.Sprintf("sk-rotated-%d", i)
		})
		if _, err := NewClientFromModelConfig(context.Background(), cfg); err != nil {
			t.Fatalf("第 %d 次轮换密钥创建客户端失败: %v", i, err)
		}
	}

	// 每次都是全新 key、不会有命中，所以最终条目数应恰好等于容量上限。
	if got := llmClientCache.Len(); got != defaultClientCacheCapacity {
		t.Errorf("轮换 %d 次密钥后缓存条目=%d，期望恰好 %d（旧实现无上限，会留 %d 个）",
			rotations, got, defaultClientCacheCapacity, rotations)
	}
}

// TestClientCacheConcurrentResolveIsRaceFree 在 -race 下验证 LRU 的并发安全，
// 并保证同一配置并发解析只留一个条目。
func TestClientCacheConcurrentResolveIsRaceFree(t *testing.T) {
	resetClientCache(t)
	cfg := testModelConfig(nil)

	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := NewClientFromModelConfig(context.Background(), cfg); err != nil {
				t.Errorf("并发解析客户端失败: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := llmClientCache.Len(); got != 1 {
		t.Errorf("同一配置并发解析产生了 %d 个缓存条目，期望 1", got)
	}
}

// TestUnsupportedProviderIsRejectedWithoutCaching 保证拒绝的服务商不污染缓存。
func TestUnsupportedProviderIsRejectedWithoutCaching(t *testing.T) {
	resetClientCache(t)

	if _, err := NewClientFromModelConfig(context.Background(), ModelConfig{
		Provider: "not-a-provider",
		ModelID:  "x",
	}); err == nil {
		t.Fatal("不支持的服务商应返回错误")
	}
	if got := llmClientCache.Len(); got != 0 {
		t.Errorf("不支持的服务商不应写入缓存，实际条目数=%d", got)
	}
}

// TestPrewarmReportsUnsupportedProviderAsFailure 固化行为变化：
// 预热不再对「请求路径根本不支持的服务商」报成功。
func TestPrewarmReportsUnsupportedProviderAsFailure(t *testing.T) {
	resetClientCache(t)

	PrewarmClients(context.Background(), []ModelConfig{
		{Provider: "not-a-provider", ModelID: "bad"},
	})

	if got := llmClientCache.Len(); got != 0 {
		t.Errorf("预热不应为不支持的服务商缓存客户端，实际条目数=%d", got)
	}
}
