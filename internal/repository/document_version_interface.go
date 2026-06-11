package repository

import (
	"context"
	"time"

	"solvify-agent/internal/model/entity"
)

// DocumentVersionRepository 定义文档版本数据访问能力
type DocumentVersionRepository interface {
	ListByDocument(ctx context.Context, userID, documentID string) ([]entity.DocumentVersion, error)
	FindByID(ctx context.Context, userID, documentID, versionID string) (entity.DocumentVersion, bool, error)
	FindLatestByDocument(ctx context.Context, userID, documentID string) (entity.DocumentVersion, bool, error)
	SaveVersionAndReindex(ctx context.Context, doc entity.Document, job *entity.DocumentProcessingJob, version *entity.DocumentVersion, chunks []entity.DocumentChunk, readyStatus, successJobStatus int16, finishedAt time.Time) error
	ReindexVersion(ctx context.Context, doc entity.Document, job *entity.DocumentProcessingJob, version entity.DocumentVersion, chunks []entity.DocumentChunk, readyStatus, successJobStatus int16, finishedAt time.Time) error
}
