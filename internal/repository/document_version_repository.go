package repository

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"solvify-agent/internal/model/entity"
	apperrors "solvify-agent/pkg/errors"
)

// documentVersionRepository 封装文档版本 GORM 数据访问
type documentVersionRepository struct {
	db *gorm.DB
}

// NewDocumentVersionRepository 创建文档版本数据仓储
func NewDocumentVersionRepository(db *gorm.DB) DocumentVersionRepository {
	return &documentVersionRepository{db: db}
}

// ListByDocument 查询文档版本列表
func (r *documentVersionRepository) ListByDocument(ctx context.Context, userID, documentID string) ([]entity.DocumentVersion, error) {
	var items []entity.DocumentVersion
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND document_id = ?", userID, documentID).
		Order("version_no DESC").
		Find(&items).Error
	return items, err
}

// FindByID 查询文档版本详情
func (r *documentVersionRepository) FindByID(ctx context.Context, userID, documentID, versionID string) (entity.DocumentVersion, bool, error) {
	var version entity.DocumentVersion
	err := r.db.WithContext(ctx).
		Where("id = ? AND user_id = ? AND document_id = ?", versionID, userID, documentID).
		First(&version).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return entity.DocumentVersion{}, false, nil
	}
	return version, err == nil, err
}

// FindLatestByDocument 查询文档最新版本
func (r *documentVersionRepository) FindLatestByDocument(ctx context.Context, userID, documentID string) (entity.DocumentVersion, bool, error) {
	var version entity.DocumentVersion
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND document_id = ?", userID, documentID).
		Order("version_no DESC").
		First(&version).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return entity.DocumentVersion{}, false, nil
	}
	return version, err == nil, err
}

// SaveVersionAndReindex 保存新版本并重建文档分块
func (r *documentVersionRepository) SaveVersionAndReindex(ctx context.Context, doc entity.Document, job *entity.DocumentProcessingJob, version *entity.DocumentVersion, chunks []entity.DocumentChunk, readyStatus, successJobStatus int, finishedAt time.Time) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		nextVersionNo, err := r.nextVersionNo(tx, doc.UserID, doc.ID)
		if err != nil {
			return err
		}
		version.VersionNo = nextVersionNo
		if err := tx.Create(version).Error; err != nil {
			return err
		}
		return r.replaceChunksAndFinishJob(tx, doc, job, version.ID, chunks, readyStatus, successJobStatus, finishedAt)
	})
	if isUniqueConflict(err) {
		return apperrors.New(apperrors.CodeBadRequest, "版本保存冲突，请重试")
	}
	return err
}

// ReindexVersion 基于指定版本重建文档分块
func (r *documentVersionRepository) ReindexVersion(ctx context.Context, doc entity.Document, job *entity.DocumentProcessingJob, version entity.DocumentVersion, chunks []entity.DocumentChunk, readyStatus, successJobStatus int, finishedAt time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		return r.replaceChunksAndFinishJob(tx, doc, job, version.ID, chunks, readyStatus, successJobStatus, finishedAt)
	})
}

// nextVersionNo 获取下一个版本号
func (r *documentVersionRepository) nextVersionNo(tx *gorm.DB, userID, documentID string) (int, error) {
	var latest entity.DocumentVersion
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("user_id = ? AND document_id = ?", userID, documentID).
		Order("version_no DESC").
		First(&latest).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 1, nil
	}
	if err != nil {
		return 0, err
	}
	return latest.VersionNo + 1, nil
}

// replaceChunksAndFinishJob 替换分块并完成 reindex 任务
func (r *documentVersionRepository) replaceChunksAndFinishJob(tx *gorm.DB, doc entity.Document, job *entity.DocumentProcessingJob, versionID string, chunks []entity.DocumentChunk, readyStatus, successJobStatus int, finishedAt time.Time) error {
	for i := range chunks {
		chunks[i].VersionID = versionID
	}

	// 1. 先写任务，再替换 chunks，确保 reindex 历史可追踪
	if err := tx.Create(job).Error; err != nil {
		return err
	}
	if err := tx.Where("user_id = ? AND document_id = ?", doc.UserID, doc.ID).
		Delete(&entity.DocumentChunk{}).Error; err != nil {
		return err
	}
	if len(chunks) > 0 {
		if err := tx.Create(&chunks).Error; err != nil {
			return err
		}
	}

	// 2. 文档状态和任务状态必须和 chunks 替换结果一起提交
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
		Where("id = ? AND user_id = ?", job.ID, doc.UserID).
		Updates(map[string]any{
			"status":        successJobStatus,
			"error_message": "",
			"started_at":    finishedAt,
			"finished_at":   finishedAt,
		}).Error
}

// isUniqueConflict 判断是否为唯一约束冲突
func isUniqueConflict(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate key") || strings.Contains(msg, "unique constraint")
}
