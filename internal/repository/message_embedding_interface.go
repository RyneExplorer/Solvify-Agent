package repository

import (
	"context"
	"time"

	"solvify-agent/internal/model/entity"
)

type ClaimedMessageEmbedding struct {
	ID                  string
	Content             string
	EmbeddingAttempts   int
	EmbeddingLeaseToken string
}

// MessageEmbeddingRepository 只操作消息行上的任务状态，结果写入与终态原子提交。
type MessageEmbeddingRepository interface {
	Claim(ctx context.Context, maxAttempts int, lease time.Duration) (*ClaimedMessageEmbedding, error)
	Complete(ctx context.Context, job ClaimedMessageEmbedding, vector entity.FloatVector) (bool, error)
	Fail(ctx context.Context, job ClaimedMessageEmbedding, retry bool, delay time.Duration, code string) (bool, error)
	Release(ctx context.Context, job ClaimedMessageEmbedding) (bool, error)
	QueueMissing(ctx context.Context, limit int, retryFailed bool) (int64, error)
}
