package repository

import (
	"context"
	"time"

	"gorm.io/gorm"

	"solvify-agent/internal/model/entity"
)

// SaveProcessResult 保存文档处理成功结果
func (r *documentRepository) SaveProcessResult(ctx context.Context, doc entity.Document, jobID string, version *entity.DocumentVersion, chunks []entity.DocumentChunk, readyStatus, successJobStatus int16, finishedAt time.Time) error {
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
		return tx.Model(&entity.DocumentProcessingJob{}).
			Where("id = ? AND user_id = ?", jobID, doc.UserID).
			Updates(map[string]any{
				"status":        successJobStatus,
				"error_message": "",
				"started_at":    finishedAt,
				"finished_at":   finishedAt,
			}).Error
	})
}

// MarkProcessFailed 标记文档处理失败
func (r *documentRepository) MarkProcessFailed(ctx context.Context, userID, documentID, jobID string, failedDocumentStatus, failedJobStatus int16, errorMessage string, finishedAt time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&entity.Document{}).
			Where("id = ? AND user_id = ?", documentID, userID).
			Updates(map[string]any{
				"status":        failedDocumentStatus,
				"error_message": errorMessage,
			}).Error; err != nil {
			return err
		}
		return tx.Model(&entity.DocumentProcessingJob{}).
			Where("id = ? AND user_id = ?", jobID, userID).
			Updates(map[string]any{
				"status":        failedJobStatus,
				"error_message": errorMessage,
				"started_at":    finishedAt,
				"finished_at":   finishedAt,
			}).Error
	})
}
