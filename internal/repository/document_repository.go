package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"solvify-agent/internal/model/entity"
)

// documentRepository 封装文档 GORM 数据访问
type documentRepository struct {
	db *gorm.DB
}

// NewDocumentRepository 创建文档数据仓储
func NewDocumentRepository(db *gorm.DB) DocumentRepository {
	return &documentRepository{db: db}
}

// Create 创建文档记录
func (r *documentRepository) Create(ctx context.Context, doc *entity.Document) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(doc).Error; err != nil {
			return err
		}
		return tx.Model(&entity.KnowledgeBase{}).
			Where("id = ? AND user_id = ?", doc.KnowledgeBaseID, doc.UserID).
			Updates(map[string]any{
				"document_count": gorm.Expr("document_count + ?", 1),
				"storage_bytes":  gorm.Expr("storage_bytes + ?", doc.FileSize),
			}).Error
	})
}

// ListByKnowledgeBase 查询知识库下未删除文档
func (r *documentRepository) ListByKnowledgeBase(ctx context.Context, userID, kbID string, deletedStatus int) ([]entity.Document, error) {
	var items []entity.Document
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND knowledge_base_id = ? AND status <> ?", userID, kbID, deletedStatus).
		Order("created_at DESC").
		Find(&items).Error
	return items, err
}

// ListWithChunkCount 查询知识库下文档列表，包含分块数和状态文本
func (r *documentRepository) ListWithChunkCount(ctx context.Context, userID, kbID string) ([]DocumentWithChunkCount, error) {
	var rows []struct {
		entity.Document
		ChunkCount int `gorm:"column:chunk_count"`
	}

	err := r.db.WithContext(ctx).Raw(`
		SELECT d.*, COALESCE(COUNT(dc.id), 0) as chunk_count
		FROM documents d
		LEFT JOIN document_chunks dc ON dc.document_id = d.id
		WHERE d.user_id = ? AND d.knowledge_base_id = ? AND d.status <> 5
		GROUP BY d.id
		ORDER BY d.created_at DESC
	`, userID, kbID).Scan(&rows).Error

	if err != nil {
		return nil, err
	}

	statusMap := map[int]string{
		1: "已上传",
		2: "处理中",
		3: "就绪",
		4: "失败",
		5: "已删除",
	}

	result := make([]DocumentWithChunkCount, 0, len(rows))
	for _, row := range rows {
		result = append(result, DocumentWithChunkCount{
			Document:   row.Document,
			ChunkCount: row.ChunkCount,
			StatusText: statusMap[row.Document.Status],
		})
	}

	return result, nil
}

// FindByID 查询当前用户未删除文档
func (r *documentRepository) FindByID(ctx context.Context, userID, documentID string, deletedStatus int) (entity.Document, bool, error) {
	var doc entity.Document
	err := r.db.WithContext(ctx).
		Where("id = ? AND user_id = ? AND status <> ?", documentID, userID, deletedStatus).
		First(&doc).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return entity.Document{}, false, nil
	}
	return doc, err == nil, err
}

// ExistsFileName 判断知识库下是否存在同名未删除文档
func (r *documentRepository) ExistsFileName(ctx context.Context, userID, kbID, fileName string, deletedStatus int) (bool, error) {
	var count int64
	err := r.db.WithContext(ctx).
		Model(&entity.Document{}).
		Where("user_id = ? AND knowledge_base_id = ? AND file_name = ? AND status <> ?", userID, kbID, fileName, deletedStatus).
		Count(&count).Error
	return count > 0, err
}

// SoftDelete 软删除文档
func (r *documentRepository) SoftDelete(ctx context.Context, userID, documentID string, deletedStatus int, deletedAt, expiredAt time.Time) (bool, error) {
	result := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var doc entity.Document
		if err := tx.Where("id = ? AND user_id = ? AND status <> ?", documentID, userID, deletedStatus).First(&doc).Error; err != nil {
			return err
		}
		if err := tx.Model(&entity.Document{}).
			Where("id = ? AND user_id = ? AND status <> ?", documentID, userID, deletedStatus).
			Updates(map[string]any{
				"status":            deletedStatus,
				"deleted_at":        deletedAt,
				"delete_expired_at": expiredAt,
			}).Error; err != nil {
			return err
		}
		return tx.Model(&entity.KnowledgeBase{}).
			Where("id = ? AND user_id = ?", doc.KnowledgeBaseID, userID).
			Updates(map[string]any{
				"document_count": gorm.Expr("CASE WHEN document_count > 0 THEN document_count - 1 ELSE 0 END"),
				"storage_bytes":  gorm.Expr("CASE WHEN storage_bytes >= ? THEN storage_bytes - ? ELSE 0 END", doc.FileSize, doc.FileSize),
			}).Error
	})
	if errors.Is(result, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return result == nil, result
}
