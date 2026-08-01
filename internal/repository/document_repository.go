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
func (r *documentRepository) SoftDelete(ctx context.Context, userID, documentID string, deletedStatus, pendingImportStatus int, deletedAt, expiredAt time.Time) (bool, error) {
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
		if err := tx.Model(&entity.SyncItem{}).
			Where("user_id = ? AND local_document_id = ?", userID, documentID).
			Updates(map[string]any{
				"local_document_id": nil,
				"import_status":     pendingImportStatus,
				"error_message":     "",
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

// SaveProcessResult 保存文档处理成功结果
func (r *documentRepository) SaveProcessResult(ctx context.Context, doc entity.Document, jobID string, version *entity.DocumentVersion, chunks []entity.DocumentChunk, readyStatus, successJobStatus int, finishedAt time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(version).Error; err != nil {
			return err
		}
		for i := range chunks {
			chunks[i].VersionID = version.ID
		}
		if len(chunks) > 0 {
			if err := tx.Create(&chunks).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&entity.Document{}).
			Where("id = ? AND user_id = ?", doc.ID, doc.UserID).
			Updates(map[string]any{
				"status":        readyStatus,
				"error_message": "",
				"ready_at":      finishedAt,
			}).Error; err != nil {
			return err
		}
		if jobID != "" {
			return tx.Model(&entity.DocumentProcessingJob{}).
				Where("id = ? AND user_id = ?", jobID, doc.UserID).
				Updates(map[string]any{
					"status":        successJobStatus,
					"error_message": "",
					"finished_at":   finishedAt,
				}).Error
		}
		return nil
	})
}

// MarkProcessFailed 标记文档处理失败
func (r *documentRepository) MarkProcessFailed(ctx context.Context, userID, documentID, jobID string, failedDocumentStatus, failedJobStatus int, errorMessage string, finishedAt time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&entity.Document{}).
			Where("id = ? AND user_id = ?", documentID, userID).
			Updates(map[string]any{
				"status":        failedDocumentStatus,
				"error_message": errorMessage,
			}).Error; err != nil {
			return err
		}
		if jobID != "" {
			return tx.Model(&entity.DocumentProcessingJob{}).
				Where("id = ? AND user_id = ?", jobID, userID).
				Updates(map[string]any{
					"status":        failedJobStatus,
					"error_message": errorMessage,
					"finished_at":   finishedAt,
				}).Error
		}
		return nil
	})
}
