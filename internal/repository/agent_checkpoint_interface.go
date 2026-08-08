package repository

import (
	"context"
	"time"
)

// AgentCheckpointRepo 定义 Agent checkpoint 数据访问接口。
// 实现 eino CheckPointStore 接口的底层存储。
type AgentCheckpointRepo interface {
	// Save 存或更新 checkpoint（checkpointID 为主键）
	Save(ctx context.Context, checkpointID string, sessionID string, data []byte, expiredAt time.Time) error
	// Find 按 checkpointID 查找
	Find(ctx context.Context, checkpointID string) ([]byte, bool, error)
	// Delete 按 checkpointID 删除
	Delete(ctx context.Context, checkpointID string) error
	// DeleteBySessionID 按 sessionID 删除所有 checkpoint
	DeleteBySessionID(ctx context.Context, sessionID string) error
	// DeleteExpired 清理过期 checkpoint（返回删除数）
	DeleteExpired(ctx context.Context, now time.Time) (int64, error)
}
