package repository

import (
	"context"

	"gorm.io/gorm"

	"solvify-agent/internal/model/entity"
)

// memoryRepository 提供用户长期记忆数据访问实现
type memoryRepository struct {
	db *gorm.DB
}

// NewMemoryRepository 创建记忆仓库
func NewMemoryRepository(db *gorm.DB) MemoryRepo {
	return &memoryRepository{db: db}
}

// ListActive 获取用户有效记忆
func (r *memoryRepository) ListActive(ctx context.Context, userID string, limit int) ([]entity.UserMemory, error) {
	if limit <= 0 {
		limit = 10
	}
	var memories []entity.UserMemory
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND is_active = ?", userID, true).
		Order("updated_at DESC").
		Limit(limit).
		Find(&memories).Error
	return memories, err
}

// Create 创建记忆
func (r *memoryRepository) Create(ctx context.Context, memory *entity.UserMemory) error {
	return r.db.WithContext(ctx).Create(memory).Error
}

// Upsert 按内容去重更新
func (r *memoryRepository) Upsert(ctx context.Context, memory *entity.UserMemory) error {
	return r.db.WithContext(ctx).
		Where("user_id = ? AND content = ?", memory.UserID, memory.Content).
		Assign(map[string]interface{}{
			"memory_type":    memory.MemoryType,
			"source_session": memory.SourceSession,
			"confidence":     memory.Confidence,
			"is_active":      memory.IsActive,
		}).
		FirstOrCreate(memory).Error
}

// DeactivateByContent 停用某条记忆
func (r *memoryRepository) DeactivateByContent(ctx context.Context, userID string, content string) error {
	return r.db.WithContext(ctx).
		Model(&entity.UserMemory{}).
		Where("user_id = ? AND content = ?", userID, content).
		Update("is_active", false).Error
}
