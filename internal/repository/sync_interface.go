package repository

import (
	"context"
	"time"

	"solvify-agent/internal/model/entity"
)

// SyncSourceRepository 定义同步源数据访问能力
type SyncSourceRepository interface {
	Create(ctx context.Context, source *entity.SyncSource, kbSourceType, kbSourcePlatform string) error
	List(ctx context.Context, userID string, deletedStatus int) ([]entity.SyncSource, error)
	FindByID(ctx context.Context, userID, sourceID string, deletedStatus int) (entity.SyncSource, bool, error)
	Update(ctx context.Context, source entity.SyncSource, deletedStatus int) (bool, error)
	SoftDelete(ctx context.Context, userID, sourceID string, normalStatus, deletedStatus int, deletedAt time.Time) (bool, error)
	MarkSyncResult(ctx context.Context, userID, sourceID string, lastSyncAt *time.Time, errorMessage string) error
}

// SyncJobRepository 定义同步任务数据访问能力
type SyncJobRepository interface {
	Create(ctx context.Context, job *entity.SyncJob) error
	MarkRunning(ctx context.Context, userID, jobID string, pendingStatus, runningStatus int, startedAt time.Time) (bool, error)
	Finish(ctx context.Context, userID, jobID string, status, totalCount, successCount, failedCount int, errorMessage string, finishedAt time.Time) error
	ListBySource(ctx context.Context, userID, sourceID string) ([]entity.SyncJob, error)
	FindByID(ctx context.Context, userID, jobID string) (entity.SyncJob, bool, error)
}

// SyncItemRepository 定义外部同步文件目录项数据访问能力
type SyncItemRepository interface {
	Upsert(ctx context.Context, item entity.SyncItem) error
	ListBySource(ctx context.Context, userID, sourceID string) ([]entity.SyncItem, error)
	FindByID(ctx context.Context, userID, itemID string) (entity.SyncItem, bool, error)
	MarkImporting(ctx context.Context, userID, itemID string, importingStatus int) (bool, error)
	MarkImported(ctx context.Context, userID, itemID, documentID string, importedStatus int) error
	MarkImportFailed(ctx context.Context, userID, itemID string, failedStatus int, errorMessage string) error
}

// SyncedDocumentRepository 定义同步文档入库能力
type SyncedDocumentRepository interface {
	FindByExternalID(ctx context.Context, userID, sourceType, externalID string, deletedStatus int) (entity.Document, bool, error)
	SaveSyncedDocument(ctx context.Context, doc entity.Document, version *entity.DocumentVersion, chunks []entity.DocumentChunk, readyStatus int, finishedAt time.Time) error
	SaveSyncedPlaceholder(ctx context.Context, doc entity.Document, deletedStatus int) error
}
