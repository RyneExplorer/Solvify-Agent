package service

import (
	"context"
	"fmt"
)

type missingMessageEmbeddingQueue interface {
	QueueMissing(context.Context, int, bool) (int64, error)
}

// MessageEmbeddingBackfiller 仅由维护命令显式登记历史任务，不在启动时执行。
type MessageEmbeddingBackfiller struct{ queue missingMessageEmbeddingQueue }

func NewMessageEmbeddingBackfiller(queue missingMessageEmbeddingQueue) *MessageEmbeddingBackfiller {
	return &MessageEmbeddingBackfiller{queue: queue}
}

func (b *MessageEmbeddingBackfiller) Enqueue(ctx context.Context, limit int, retryFailed bool) (int64, error) {
	if limit < 1 || limit > 10000 {
		return 0, fmt.Errorf("消息补齐数量必须为1–10000")
	}
	return b.queue.QueueMissing(ctx, limit, retryFailed)
}
