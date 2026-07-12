package repository

import (
	"context"
	"time"

	"solvify-agent/internal/model/entity"
)

// DocumentRepository 定义文档数据访问能力
type DocumentRepository interface {
	Create(ctx context.Context, doc *entity.Document) error
	ListByKnowledgeBase(ctx context.Context, userID, kbID string, deletedStatus int) ([]entity.Document, error)
	ListWithChunkCount(ctx context.Context, userID, kbID string) ([]DocumentWithChunkCount, error)
	FindByID(ctx context.Context, userID, documentID string, deletedStatus int) (entity.Document, bool, error)
	ExistsFileName(ctx context.Context, userID, kbID, fileName string, deletedStatus int) (bool, error)
	SoftDelete(ctx context.Context, userID, documentID string, deletedStatus int, deletedAt, expiredAt time.Time) (bool, error)
	SaveProcessResult(ctx context.Context, doc entity.Document, jobID string, version *entity.DocumentVersion, chunks []entity.DocumentChunk, readyStatus, successJobStatus int, finishedAt time.Time) error
	MarkProcessFailed(ctx context.Context, userID, documentID, jobID string, failedDocumentStatus, failedJobStatus int, errorMessage string, finishedAt time.Time) error
}

// DocumentWithChunkCount 文档信息+分块数
type DocumentWithChunkCount struct {
	entity.Document
	ChunkCount int    `json:"chunk_count"`
	StatusText string `json:"status_text"`
}
