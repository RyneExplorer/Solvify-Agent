package repository

import (
	"context"

	"solvify-agent/internal/model/entity"
)

// UserMemoryRepo 定义用户长期记忆数据访问接口
type UserMemoryRepo interface {
	// ListActive 获取用户有效记忆，按更新时间倒序
	ListActive(ctx context.Context, userID string, limit int) ([]entity.UserMemory, error)
	// Create 创建记忆
	Create(ctx context.Context, memory *entity.UserMemory) error
	// Upsert 按内容去重更新（如果不存在则创建）
	Upsert(ctx context.Context, memory *entity.UserMemory) error
	// DeactivateByContent 停用某条记忆
	DeactivateByContent(ctx context.Context, userID string, content string) error
}
