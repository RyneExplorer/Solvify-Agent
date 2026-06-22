package repository

import (
	"context"
	"time"

	"solvify-agent/internal/model/entity"
)

// KnowledgeBaseRepository 定义知识库数据访问能力
type KnowledgeBaseRepository interface {
	Create(ctx context.Context, kb *entity.KnowledgeBase) error
	ExistsName(ctx context.Context, userID, name string, normalStatus int) (bool, error)
	ListNormal(ctx context.Context, userID string, status int) ([]entity.KnowledgeBase, error)
	FindNormal(ctx context.Context, userID, kbID string, status int) (entity.KnowledgeBase, bool, error)
	UpdateBasic(ctx context.Context, userID, kbID string, status int, name, category, description string) (bool, error)
	SoftDelete(ctx context.Context, userID, kbID string, normalStatus, deletedStatus int, deletedAt, expiredAt time.Time) (bool, error)
	CountDocuments(ctx context.Context, userID, kbID string, deletedStatus int) (int64, error)
	SumDocumentStorage(ctx context.Context, userID, kbID string, deletedStatus int) (int64, error)
	CountRetrievableChunks(ctx context.Context, userID, kbID string) (int64, error)
}
