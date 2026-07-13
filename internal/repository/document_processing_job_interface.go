package repository

import (
	"context"
	"time"

	"solvify-agent/internal/model/entity"
)

// DocumentProcessingJobRepository 定义文档处理任务数据访问能力
type DocumentProcessingJobRepository interface {
	CreateProcessJob(ctx context.Context, job *entity.DocumentProcessingJob, allowedDocumentStatuses []int, processingDocumentStatus int) (bool, error)
	MarkRunning(ctx context.Context, userID, jobID string, pendingStatus, runningStatus int, startedAt time.Time) (bool, error)
	ListByDocument(ctx context.Context, userID, documentID string) ([]entity.DocumentProcessingJob, error)
	FindByID(ctx context.Context, userID, jobID string) (entity.DocumentProcessingJob, bool, error)
}
