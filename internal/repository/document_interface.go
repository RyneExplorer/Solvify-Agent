package repository

import (
	"context"
	"time"

	"solvify-agent/internal/model/entity"
)

// DocumentRepository 定义文档数据访问能力
type DocumentRepository interface {
	Create(ctx context.Context, doc *entity.Document) error
	ListByKnowledgeBase(ctx context.Context, userID, kbID string, deletedStatus int16) ([]entity.Document, error)
	FindByID(ctx context.Context, userID, documentID string, deletedStatus int16) (entity.Document, bool, error)
	ExistsFileName(ctx context.Context, userID, kbID, fileName string, deletedStatus int16) (bool, error)
	SoftDelete(ctx context.Context, userID, documentID string, deletedStatus int16, deletedAt, expiredAt time.Time) (bool, error)
}
