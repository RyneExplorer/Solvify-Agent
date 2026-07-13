package database

import (
	"context"
	"fmt"
	"net"
	"strconv"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"solvify-agent/pkg/config"
	"solvify-agent/pkg/logger"
)

// OpenRedis 初始化 Redis 客户端
func OpenRedis(cfg *config.RedisConfig) (*redis.Client, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Password: cfg.Password,
		DB:       cfg.DB,
		PoolSize: cfg.PoolSize,
	})

	if err := client.Ping(context.Background()).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("检查 Redis 连接失败: %w", err)
	}

	logger.Info("Redis 连接成功",
		zap.String("host", cfg.Host),
		zap.Int("port", cfg.Port),
		zap.Int("db", cfg.DB),
	)
	return client, nil
}

// CloseRedis 关闭 Redis 客户端
func CloseRedis(client *redis.Client) error {
	if err := client.Close(); err != nil {
		return fmt.Errorf("关闭 Redis 连接失败: %w", err)
	}
	return nil
}
