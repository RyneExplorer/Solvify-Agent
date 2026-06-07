package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"solvify-agent/internal/model/entity"
)

// documentProcessingJobRepository 封装文档处理任务 GORM 数据访问
type documentProcessingJobRepository struct {
	db *gorm.DB
}

// NewDocumentProcessingJobRepository 创建文档处理任务数据仓储
func NewDocumentProcessingJobRepository(db *gorm.DB) DocumentProcessingJobRepository {
	return &documentProcessingJobRepository{db: db}
}

// CreateProcessJob 创建处理任务并更新文档状态
func (r *documentProcessingJobRepository) CreateProcessJob(ctx context.Context, job *entity.DocumentProcessingJob, allowedDocumentStatuses []int16, processingDocumentStatus int16) (bool, error) {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// 1. 先按允许状态抢占文档，避免重复触发处理任务
		result := tx.Model(&entity.Document{}).
			Where("id = ? AND user_id = ? AND status IN ?", job.DocumentID, job.UserID, allowedDocumentStatuses).
			Update("status", processingDocumentStatus)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return gorm.ErrRecordNotFound
		}

		// 2. 文档进入处理中后再创建任务记录，保证任务和状态一致
		return tx.Create(job).Error
	})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return err == nil, err
}

// ListByDocument 查询文档处理任务列表
func (r *documentProcessingJobRepository) ListByDocument(ctx context.Context, userID, documentID string) ([]entity.DocumentProcessingJob, error) {
	var items []entity.DocumentProcessingJob
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND document_id = ?", userID, documentID).
		Order("created_at DESC").
		Find(&items).Error
	return items, err
}

// FindByID 查询处理任务详情
func (r *documentProcessingJobRepository) FindByID(ctx context.Context, userID, jobID string) (entity.DocumentProcessingJob, bool, error) {
	var job entity.DocumentProcessingJob
	err := r.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", jobID, userID).
		First(&job).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return entity.DocumentProcessingJob{}, false, nil
	}
	return job, err == nil, err
}
