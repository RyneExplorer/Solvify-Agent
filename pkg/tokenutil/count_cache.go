package tokenutil

import (
	"container/list"
	"crypto/sha256"
	"fmt"
	"sync"

	"github.com/pkoukk/tiktoken-go"
	"golang.org/x/sync/singleflight"
)

// 只保存正文摘要和精确计数，不保留正文或可还原正文的token序列。
// 编码器身份隔离不同分词规则；规则重新加载后不会复用旧计数。
type tokenCountKey struct {
	encoding *tiktoken.Tiktoken
	content  [sha256.Size]byte
}

type tokenCountEntry struct {
	key   tokenCountKey
	count int
}

type tokenCountCache struct {
	mu       sync.Mutex
	capacity int
	entries  map[tokenCountKey]*list.Element
	order    list.List
	pending  singleflight.Group
}

var exactTokenCounts = newTokenCountCache(1024)

func newTokenCountCache(capacity int) *tokenCountCache {
	return &tokenCountCache{capacity: capacity, entries: make(map[tokenCountKey]*list.Element)}
}

func (c *tokenCountCache) get(key tokenCountKey) (int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if entry := c.entries[key]; entry != nil {
		c.order.MoveToFront(entry)
		return entry.Value.(tokenCountEntry).count, true
	}
	return 0, false
}

func (c *tokenCountCache) count(key tokenCountKey, calculate func() int) int {
	if count, ok := c.get(key); ok {
		return count
	}
	// 纯计算不绑定请求context，单个请求取消不会中断其他等待者的计数。
	value, _, _ := c.pending.Do(fmt.Sprintf("%p:%x", key.encoding, key.content), func() (any, error) {
		if count, ok := c.get(key); ok {
			return count, nil
		}
		count := calculate()
		c.mu.Lock()
		defer c.mu.Unlock()
		entry := c.order.PushFront(tokenCountEntry{key: key, count: count})
		c.entries[key] = entry
		if c.order.Len() > c.capacity {
			old := c.order.Back()
			delete(c.entries, old.Value.(tokenCountEntry).key)
			c.order.Remove(old)
		}
		return count, nil
	})
	return value.(int)
}
