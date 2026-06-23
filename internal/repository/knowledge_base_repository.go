package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"solvify-agent/internal/model/entity"
)

// knowledgeBaseRepository 封装知识库 GORM 数据访问
type knowledgeBaseRepository struct {
	db *gorm.DB
}

// NewKnowledgeBaseRepository 创建知识库数据仓储
func NewKnowledgeBaseRepository(db *gorm.DB) KnowledgeBaseRepository {
	return &knowledgeBaseRepository{db: db}
}

// Create 创建知识库记录
func (r *knowledgeBaseRepository) Create(ctx context.Context, kb *entity.KnowledgeBase) error {
	return r.db.WithContext(ctx).Create(kb).Error
}

// ExistsName 判断当前用户是否存在同名正常知识库
func (r *knowledgeBaseRepository) ExistsName(ctx context.Context, userID, name string, normalStatus int) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&entity.KnowledgeBase{}).
		Where("user_id = ? AND name = ? AND status = ?", userID, name, normalStatus).
		Count(&count).Error
	return count > 0, err
}

// ListNormal 查询当前用户正常状态的知识库
func (r *knowledgeBaseRepository) ListNormal(ctx context.Context, userID string, status int) ([]entity.KnowledgeBase, error) {
	var items []entity.KnowledgeBase
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND status = ?", userID, status).
		Order("created_at DESC").
		Find(&items).Error
	return items, err
}

// FindNormal 查询当前用户正常状态的知识库
func (r *knowledgeBaseRepository) FindNormal(ctx context.Context, userID, kbID string, status int) (entity.KnowledgeBase, bool, error) {
	var kb entity.KnowledgeBase
	err := r.db.WithContext(ctx).
		Where("id = ? AND user_id = ? AND status = ?", kbID, userID, status).
		First(&kb).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return entity.KnowledgeBase{}, false, nil
	}
	return kb, err == nil, err
}

// UpdateBasic 更新知识库基础信息
func (r *knowledgeBaseRepository) UpdateBasic(ctx context.Context, userID, kbID string, status int, name, category, description string) (bool, error) {
	result := r.db.WithContext(ctx).
		Model(&entity.KnowledgeBase{}).
		Where("id = ? AND user_id = ? AND status = ?", kbID, userID, status).
		Updates(map[string]any{
			"name":        name,
			"category":    category,
			"description": description,
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// SoftDelete 软删除知识库
func (r *knowledgeBaseRepository) SoftDelete(ctx context.Context, userID, kbID string, normalStatus, deletedStatus int, deletedAt, expiredAt time.Time) (bool, error) {
	result := r.db.WithContext(ctx).
		Model(&entity.KnowledgeBase{}).
		Where("id = ? AND user_id = ? AND status = ?", kbID, userID, normalStatus).
		Updates(map[string]any{
			"status":            deletedStatus,
			"deleted_at":        deletedAt,
			"delete_expired_at": expiredAt,
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// CountDocuments 统计知识库文档数
func (r *knowledgeBaseRepository) CountDocuments(ctx context.Context, userID, kbID string, deletedStatus int) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Table("documents").
		Where("user_id = ? AND knowledge_base_id = ? AND status <> ?", userID, kbID, deletedStatus).
		Count(&count).Error
	return count, err
}

// SumDocumentStorage 统计知识库文档存储大小
func (r *knowledgeBaseRepository) SumDocumentStorage(ctx context.Context, userID, kbID string, deletedStatus int) (int64, error) {
	var storageBytes int64
	err := r.db.WithContext(ctx).
		Table("documents").
		Select("COALESCE(SUM(file_size), 0)").
		Where("user_id = ? AND knowledge_base_id = ? AND status <> ?", userID, kbID, deletedStatus).
		Scan(&storageBytes).Error
	return storageBytes, err
}

// CountRetrievableChunks 统计知识库可检索分块数
func (r *knowledgeBaseRepository) CountRetrievableChunks(ctx context.Context, userID, kbID string) (int64, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Table("document_chunks").
		Where("user_id = ? AND knowledge_base_id = ?", userID, kbID).
		Count(&count).Error
	return count, err
}
