package llm

import (
	"container/list"
	"sync"
)

// defaultClientCacheCapacity 是 LLM 客户端缓存的默认容量上限。
//
// 每个条目持有一个 eino ChatModel（含 HTTP 客户端与配置），原先用 sync.Map
// 无上限、无淘汰，key 里又含明文 APIKey —— 用户轮换密钥、或有人反复「测试连接」
// 试不同模型时，旧 client 会永久常驻，缓存只增不减。给一个硬上限即可收敛。
const defaultClientCacheCapacity = 64

// clientCacheKey 是「创建 OpenAI 客户端所需入参」的指纹。
//
// ⚠️ 不变式：字段必须与 OpenAIClientConfig 一一对应（同名同序）。
// 给 OpenAIClientConfig 加了字段却忘了加进 key，会让两份不同配置共用同一个客户端 ——
// 这个坑已经真实发生过：原先的 key 漏了 Config 与 MaxContextLength，
// 前者导致预热条目永远命中不到，后者让上下文窗口被别的配置覆盖。
// TestClientCacheKeyCoversAllClientConfigFields 会把这个不变式钉死。
type clientCacheKey struct {
	APIKey           string
	BaseURL          string
	Model            string
	Config           string
	MaxContextLength int
}

// cacheKey 返回该客户端配置的缓存指纹。
//
// 它是 key 的唯一来源：请求路径与预热路径都必须经过它，
// 不允许再有第二处手拼 clientCacheKey 的代码（P1-8 的根因就是手拼了两份）。
func (cfg OpenAIClientConfig) cacheKey() clientCacheKey {
	return clientCacheKey{
		APIKey:           cfg.APIKey,
		BaseURL:          cfg.BaseURL,
		Model:            cfg.Model,
		Config:           string(cfg.Config),
		MaxContextLength: cfg.MaxContextLength,
	}
}

// clientCache 是带容量上限的 LRU 缓存，专用于缓存 LLM 客户端。
type clientCache struct {
	mu       sync.Mutex
	capacity int
	items    map[clientCacheKey]*list.Element
	order    *list.List // 元素为 *clientCacheEntry，队首 = 最近使用
}

// clientCacheEntry 是 clientCache 的链表元素。
type clientCacheEntry struct {
	key    clientCacheKey
	client *OpenAIClient
}

// newClientCache 创建一个容量为 capacity 的 LRU 缓存；capacity <= 0 时用默认值。
func newClientCache(capacity int) *clientCache {
	if capacity <= 0 {
		capacity = defaultClientCacheCapacity
	}
	return &clientCache{
		capacity: capacity,
		items:    make(map[clientCacheKey]*list.Element, capacity),
		order:    list.New(),
	}
}

// Get 返回缓存中的客户端，并把条目移到队首（标记为最近使用）。
func (c *clientCache) Get(key clientCacheKey) (*OpenAIClient, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	c.order.MoveToFront(el)
	return el.Value.(*clientCacheEntry).client, true
}

// Put 写入客户端；同 key 覆盖，超出容量时淘汰最久未使用的条目。
func (c *clientCache) Put(key clientCacheKey, client *OpenAIClient) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if el, ok := c.items[key]; ok {
		el.Value.(*clientCacheEntry).client = client
		c.order.MoveToFront(el)
		return
	}

	c.items[key] = c.order.PushFront(&clientCacheEntry{key: key, client: client})
	for c.order.Len() > c.capacity {
		oldest := c.order.Back()
		if oldest == nil {
			break
		}
		c.order.Remove(oldest)
		delete(c.items, oldest.Value.(*clientCacheEntry).key)
	}
}

// Len 返回当前条目数。
func (c *clientCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}

// llmClientCache 是全局的 LLM 客户端缓存。
var llmClientCache = newClientCache(defaultClientCacheCapacity)
