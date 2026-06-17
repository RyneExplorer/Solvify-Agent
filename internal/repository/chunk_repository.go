package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"solvify-agent/internal/model/entity"
)

// ChunkRepository 定义 chunk 数据访问能力
type ChunkRepository interface {
	FindByID(ctx context.Context, chunkID string) (entity.DocumentChunk, bool, error)
}

// chunkRepository 封装 chunk GORM 数据访问
type chunkRepository struct {
	db *gorm.DB
}

// NewChunkRepository 创建 chunk 数据仓储
func NewChunkRepository(db *gorm.DB) ChunkRepository {
	return &chunkRepository{db: db}
}

// FindByID 根据 ID 查询 chunk
func (r *chunkRepository) FindByID(ctx context.Context, chunkID string) (entity.DocumentChunk, bool, error) {
	var chunk entity.DocumentChunk
	err := r.db.WithContext(ctx).
		Select("id", "document_id", "content", "section_title").
		Where("id = ?", chunkID).
		First(&chunk).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return entity.DocumentChunk{}, false, nil
	}
	return chunk, err == nil, err
}
