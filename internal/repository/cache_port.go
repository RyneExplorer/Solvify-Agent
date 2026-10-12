package repository

import (
	"context"
	"time"
)

// cachePort 是缓存装饰器所需的最小能力集（单键 get / set / delete）。
//
// 抽成接口只为一个目的：让「写时失效」可测。*cache.RedisCache 天然满足它，
// 所以 app.go 的装配一行都不用改；测试换成内存实现，就能在不起 Redis 的前提下
// 断言「删了行 / 改了名之后缓存不再命中」——这是缓存装饰器唯一真正值得回归的行为。
//
// ⚠️ 所有缓存装饰器都必须通过本接口拿缓存，不要再直接依赖 *cache.RedisCache。
// 直接依赖会让失效逻辑不可测，而**不可测的失效逻辑正是 P0-5 那类缺陷的温床**：
// tool_type 的日志曾因「删了新 key、没删旧 key」而长时间返回脏值，当时就是因为
// 无法在单测里驱动，才拖到审查才发现。
type cachePort interface {
	Get(ctx context.Context, key string, dest any) (bool, error)
	Set(ctx context.Context, key string, value any, ttl time.Duration) error
	Delete(ctx context.Context, key string) error
}
