package repository

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

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
