package cache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"solvify-agent/pkg/logger"
)

// RedisCache 通用 Redis 缓存封装
type RedisCache struct {
	client     *redis.Client
	keyPrefix  string
	defaultTTL time.Duration
}

// New 创建 RedisCache
func New(client *redis.Client, keyPrefix string, defaultTTL time.Duration) *RedisCache {
	return &RedisCache{
		client:     client,
		keyPrefix:  keyPrefix,
		defaultTTL: defaultTTL,
	}
}

// Get 从缓存获取，反序列化到 dest。miss 时返回 false。
func (c *RedisCache) Get(ctx context.Context, key string, dest any) (bool, error) {
	fullKey := c.keyPrefix + key
	data, err := c.client.Get(ctx, fullKey).Bytes()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("缓存读取失败: %w", err)
	}
	if err := json.Unmarshal(data, dest); err != nil {
		return false, fmt.Errorf("缓存反序列化失败: %w", err)
	}
	return true, nil
}

// GetDelete 从缓存原子读取并删除数据
func (c *RedisCache) GetDelete(ctx context.Context, key string, dest any) (bool, error) {
	fullKey := c.keyPrefix + key
	data, err := c.client.GetDel(ctx, fullKey).Bytes()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("缓存读取并删除失败: %w", err)
	}
	if err := json.Unmarshal(data, dest); err != nil {
		return false, fmt.Errorf("缓存反序列化失败: %w", err)
	}
	return true, nil
}

// Set 写入缓存，ttl 为 0 时使用默认 TTL
func (c *RedisCache) Set(ctx context.Context, key string, value any, ttl time.Duration) error {
	fullKey := c.keyPrefix + key
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("缓存序列化失败: %w", err)
	}
	if ttl <= 0 {
		ttl = c.defaultTTL
	}
	if err := c.client.Set(ctx, fullKey, data, ttl).Err(); err != nil {
		logger.Warnf("缓存写入失败: key=%s, err=%v", fullKey, err)
	}
	return nil
}

// Delete 删除缓存
func (c *RedisCache) Delete(ctx context.Context, key string) error {
	return c.client.Del(ctx, c.keyPrefix+key).Err()
}
