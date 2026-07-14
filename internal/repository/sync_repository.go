package repository

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"solvify-agent/internal/model/entity"
)

// syncSourceRepository 封装同步源 GORM 数据访问
type syncSourceRepository struct {
	db *gorm.DB
}

// NewSyncSourceRepository 创建同步源仓储
func NewSyncSourceRepository(db *gorm.DB) SyncSourceRepository {
	return &syncSourceRepository{db: db}
}

// Create 创建同步源并标记知识库来源
func (r *syncSourceRepository) Create(ctx context.Context, source *entity.SyncSource, kbSourceType, kbSourcePlatform string) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&entity.KnowledgeBase{}).
			Where("id = ? AND user_id = ?", source.KnowledgeBaseID, source.UserID).
			Updates(map[string]any{
				"source_type":     kbSourceType,
				"source_platform": kbSourcePlatform,
			}).Error; err != nil {
			return err
		}
		return tx.Create(source).Error
	})
}

// List 查询当前用户未删除同步源
func (r *syncSourceRepository) List(ctx context.Context, userID string, deletedStatus int) ([]entity.SyncSource, error) {
	var items []entity.SyncSource
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND status <> ?", userID, deletedStatus).
		Order("created_at DESC").
		Find(&items).Error
	return items, err
}

// FindByID 查询同步源
func (r *syncSourceRepository) FindByID(ctx context.Context, userID, sourceID string, deletedStatus int) (entity.SyncSource, bool, error) {
	var source entity.SyncSource
	err := r.db.WithContext(ctx).
		Where("id = ? AND user_id = ? AND status <> ?", sourceID, userID, deletedStatus).
		First(&source).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return entity.SyncSource{}, false, nil
	}
	return source, err == nil, err
}

// Update 更新同步源基础配置
func (r *syncSourceRepository) Update(ctx context.Context, source entity.SyncSource, deletedStatus int) (bool, error) {
	result := r.db.WithContext(ctx).
		Model(&entity.SyncSource{}).
		Where("id = ? AND user_id = ? AND status <> ?", source.ID, source.UserID, deletedStatus).
		Updates(map[string]any{
			"name":          source.Name,
			"source_config": source.SourceConfig,
			"status":        source.Status,
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// SoftDelete 软删除同步源
func (r *syncSourceRepository) SoftDelete(ctx context.Context, userID, sourceID string, normalStatus, deletedStatus int, deletedAt time.Time) (bool, error) {
	result := r.db.WithContext(ctx).
		Model(&entity.SyncSource{}).
		Where("id = ? AND user_id = ? AND status <> ?", sourceID, userID, deletedStatus).
		Updates(map[string]any{
			"status":     deletedStatus,
			"deleted_at": deletedAt,
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// MarkSyncResult 更新同步源最近同步结果
func (r *syncSourceRepository) MarkSyncResult(ctx context.Context, userID, sourceID string, lastSyncAt *time.Time, errorMessage string) error {
	return r.db.WithContext(ctx).
		Model(&entity.SyncSource{}).
		Where("id = ? AND user_id = ?", sourceID, userID).
		Updates(map[string]any{
			"last_sync_at":       lastSyncAt,
			"last_error_message": errorMessage,
		}).Error
}

// syncJobRepository 封装同步任务 GORM 数据访问
type syncJobRepository struct {
	db *gorm.DB
}

// NewSyncJobRepository 创建同步任务仓储
func NewSyncJobRepository(db *gorm.DB) SyncJobRepository {
	return &syncJobRepository{db: db}
}

// Create 创建同步任务
func (r *syncJobRepository) Create(ctx context.Context, job *entity.SyncJob) error {
	return r.db.WithContext(ctx).Create(job).Error
}

// MarkRunning 标记同步任务运行中
func (r *syncJobRepository) MarkRunning(ctx context.Context, userID, jobID string, pendingStatus, runningStatus int, startedAt time.Time) (bool, error) {
	result := r.db.WithContext(ctx).
		Model(&entity.SyncJob{}).
		Where("id = ? AND user_id = ? AND status = ?", jobID, userID, pendingStatus).
		Updates(map[string]any{
			"status":     runningStatus,
			"started_at": startedAt,
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// Finish 完成同步任务
func (r *syncJobRepository) Finish(ctx context.Context, userID, jobID string, status, totalCount, successCount, failedCount int, errorMessage string, finishedAt time.Time) error {
	return r.db.WithContext(ctx).
		Model(&entity.SyncJob{}).
		Where("id = ? AND user_id = ?", jobID, userID).
		Updates(map[string]any{
			"status":        status,
			"total_count":   totalCount,
			"success_count": successCount,
			"failed_count":  failedCount,
			"error_message": errorMessage,
			"finished_at":   finishedAt,
		}).Error
}

// ListBySource 查询同步源任务列表
func (r *syncJobRepository) ListBySource(ctx context.Context, userID, sourceID string) ([]entity.SyncJob, error) {
	var items []entity.SyncJob
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND sync_source_id = ?", userID, sourceID).
		Order("created_at DESC").
		Find(&items).Error
	return items, err
}

// FindByID 查询同步任务详情
func (r *syncJobRepository) FindByID(ctx context.Context, userID, jobID string) (entity.SyncJob, bool, error) {
	var job entity.SyncJob
	err := r.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", jobID, userID).
		First(&job).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return entity.SyncJob{}, false, nil
	}
	return job, err == nil, err
}

// syncItemRepository 封装外部同步文件目录项 GORM 数据访问
type syncItemRepository struct {
	db *gorm.DB
}

// NewSyncItemRepository 创建外部同步文件目录项仓储
func NewSyncItemRepository(db *gorm.DB) SyncItemRepository {
	return &syncItemRepository{db: db}
}

// Upsert 创建或更新外部同步文件目录项
func (r *syncItemRepository) Upsert(ctx context.Context, item entity.SyncItem) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "user_id"},
			{Name: "sync_source_id"},
			{Name: "external_id"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"knowledge_base_id",
			"parent_external_id",
			"name",
			"item_type",
			"category",
			"extension",
			"external_url",
			"file_size",
			"has_children",
			"source_updated_at",
			"updated_at",
		}),
	}).Create(&item).Error
}

// ResetDeletedDocumentLinks 清理已删除本地文档的同步关联
func (r *syncItemRepository) ResetDeletedDocumentLinks(ctx context.Context, userID, sourceID string, pendingStatus, deletedDocumentStatus int) error {
	return r.db.WithContext(ctx).
		Model(&entity.SyncItem{}).
		Where("user_id = ? AND sync_source_id = ? AND local_document_id IS NOT NULL", userID, sourceID).
		Where(`NOT EXISTS (
			SELECT 1 FROM documents
			WHERE documents.id = sync_items.local_document_id
				AND documents.user_id = sync_items.user_id
				AND documents.status <> ?
		)`, deletedDocumentStatus).
		Updates(map[string]any{
			"local_document_id": nil,
			"import_status":     pendingStatus,
			"error_message":     "",
		}).Error
}

// ListBySource 查询同步源下外部文件目录项
func (r *syncItemRepository) ListBySource(ctx context.Context, userID, sourceID string) ([]entity.SyncItem, error) {
	var items []entity.SyncItem
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND sync_source_id = ?", userID, sourceID).
		Order("item_type ASC, name ASC").
		Find(&items).Error
	return items, err
}

// FindByID 查询外部文件目录项
func (r *syncItemRepository) FindByID(ctx context.Context, userID, itemID string) (entity.SyncItem, bool, error) {
	var item entity.SyncItem
	err := r.db.WithContext(ctx).
		Where("id = ? AND user_id = ?", itemID, userID).
		First(&item).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return entity.SyncItem{}, false, nil
	}
	return item, err == nil, err
}

// MarkImporting 标记外部文件目录项导入中
func (r *syncItemRepository) MarkImporting(ctx context.Context, userID, itemID string, importingStatus int) (bool, error) {
	result := r.db.WithContext(ctx).
		Model(&entity.SyncItem{}).
		Where("id = ? AND user_id = ?", itemID, userID).
		Updates(map[string]any{
			"import_status": importingStatus,
			"error_message": "",
		})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

// MarkImported 标记外部文件目录项已导入
func (r *syncItemRepository) MarkImported(ctx context.Context, userID, itemID, documentID string, importedStatus int) error {
	return r.db.WithContext(ctx).
		Model(&entity.SyncItem{}).
		Where("id = ? AND user_id = ?", itemID, userID).
		Updates(map[string]any{
			"local_document_id": documentID,
			"import_status":     importedStatus,
			"error_message":     "",
		}).Error
}

// MarkImportFailed 标记外部文件目录项导入失败
func (r *syncItemRepository) MarkImportFailed(ctx context.Context, userID, itemID string, failedStatus int, errorMessage string) error {
	return r.db.WithContext(ctx).
		Model(&entity.SyncItem{}).
		Where("id = ? AND user_id = ?", itemID, userID).
		Updates(map[string]any{
			"import_status": failedStatus,
			"error_message": errorMessage,
		}).Error
}

// syncedDocumentRepository 封装同步文档入库事务
type syncedDocumentRepository struct {
	db *gorm.DB
}

// NewSyncedDocumentRepository 创建同步文档仓储
func NewSyncedDocumentRepository(db *gorm.DB) SyncedDocumentRepository {
	return &syncedDocumentRepository{db: db}
}

// FindByExternalID 查询外部同步文档
func (r *syncedDocumentRepository) FindByExternalID(ctx context.Context, userID, sourceType, externalID string, deletedStatus int) (entity.Document, bool, error) {
	var doc entity.Document
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND source_type = ? AND external_id = ? AND status <> ?", userID, sourceType, externalID, deletedStatus).
		First(&doc).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return entity.Document{}, false, nil
	}
	return doc, err == nil, err
}

// SaveSyncedDocument 保存同步文档版本并替换分块
func (r *syncedDocumentRepository) SaveSyncedDocument(ctx context.Context, doc entity.Document, version *entity.DocumentVersion, chunks []entity.DocumentChunk, readyStatus int, finishedAt time.Time) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current entity.Document
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id = ? AND source_type = ? AND external_id = ? AND status <> ?", doc.UserID, doc.SourceType, doc.ExternalID, 5).
			First(&current).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if err := tx.Create(&doc).Error; err != nil {
				return err
			}
			current = doc
			if err := tx.Model(&entity.KnowledgeBase{}).
				Where("id = ? AND user_id = ?", doc.KnowledgeBaseID, doc.UserID).
				Updates(map[string]any{
					"document_count": gorm.Expr("document_count + ?", 1),
					"storage_bytes":  gorm.Expr("storage_bytes + ?", doc.FileSize),
				}).Error; err != nil {
				return err
			}
		} else if err != nil {
			return err
		} else {
			diff := doc.FileSize - current.FileSize
			doc.ID = current.ID
			if err := tx.Model(&entity.Document{}).
				Where("id = ? AND user_id = ?", current.ID, doc.UserID).
				Updates(map[string]any{
					"title":             doc.Title,
					"file_name":         doc.FileName,
					"file_type":         doc.FileType,
					"file_size":         doc.FileSize,
					"storage_path":      doc.StoragePath,
					"file_hash":         doc.FileHash,
					"external_url":      doc.ExternalURL,
					"source_updated_at": doc.SourceUpdatedAt,
					"status":            readyStatus,
					"error_message":     "",
					"ready_at":          finishedAt,
				}).Error; err != nil {
				return err
			}
			if diff != 0 {
				if err := tx.Model(&entity.KnowledgeBase{}).
					Where("id = ? AND user_id = ?", doc.KnowledgeBaseID, doc.UserID).
					Update("storage_bytes", gorm.Expr("CASE WHEN storage_bytes + ? > 0 THEN storage_bytes + ? ELSE 0 END", diff, diff)).Error; err != nil {
					return err
				}
			}
		}

		nextVersionNo, err := r.nextVersionNo(tx, doc.UserID, doc.ID)
		if err != nil {
			return err
		}
		version.DocumentID = doc.ID
		version.VersionNo = nextVersionNo
		if err := tx.Create(version).Error; err != nil {
			return err
		}
		for i := range chunks {
			chunks[i].DocumentID = doc.ID
			chunks[i].VersionID = version.ID
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
		return tx.Model(&entity.Document{}).
			Where("id = ? AND user_id = ?", doc.ID, doc.UserID).
			Updates(map[string]any{
				"status":        readyStatus,
				"error_message": "",
				"ready_at":      finishedAt,
			}).Error
	})
}

// SaveSyncedPlaceholder 保存暂不支持解析的同步文档记录
func (r *syncedDocumentRepository) SaveSyncedPlaceholder(ctx context.Context, doc entity.Document, deletedStatus int) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var current entity.Document
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("user_id = ? AND source_type = ? AND external_id = ? AND status <> ?", doc.UserID, doc.SourceType, doc.ExternalID, deletedStatus).
			First(&current).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if err := tx.Create(&doc).Error; err != nil {
				return err
			}
			return tx.Model(&entity.KnowledgeBase{}).
				Where("id = ? AND user_id = ?", doc.KnowledgeBaseID, doc.UserID).
				Updates(map[string]any{
					"document_count": gorm.Expr("document_count + ?", 1),
					"storage_bytes":  gorm.Expr("storage_bytes + ?", doc.FileSize),
				}).Error
		}
		if err != nil {
			return err
		}

		diff := doc.FileSize - current.FileSize
		if err := tx.Model(&entity.Document{}).
			Where("id = ? AND user_id = ?", current.ID, doc.UserID).
			Updates(map[string]any{
				"title":             doc.Title,
				"file_name":         doc.FileName,
				"file_type":         doc.FileType,
				"file_size":         doc.FileSize,
				"storage_path":      "",
				"file_hash":         doc.FileHash,
				"external_url":      doc.ExternalURL,
				"source_updated_at": doc.SourceUpdatedAt,
				"status":            doc.Status,
				"error_message":     doc.ErrorMessage,
				"ready_at":          nil,
			}).Error; err != nil {
			return err
		}
		if diff == 0 {
			return nil
		}
		return tx.Model(&entity.KnowledgeBase{}).
			Where("id = ? AND user_id = ?", doc.KnowledgeBaseID, doc.UserID).
			Update("storage_bytes", gorm.Expr("CASE WHEN storage_bytes + ? > 0 THEN storage_bytes + ? ELSE 0 END", diff, diff)).Error
	})
}

// nextVersionNo 获取同步文档下一个版本号
func (r *syncedDocumentRepository) nextVersionNo(tx *gorm.DB, userID, documentID string) (int, error) {
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
