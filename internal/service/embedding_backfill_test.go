package service

import (
	"context"
	"errors"
	"testing"
)

type missingEmbeddingQueue struct {
	calls, limit int
	retry        bool
	err          error
	ctx          context.Context
}

func (r *missingEmbeddingQueue) QueueMissing(ctx context.Context, limit int, retry bool) (int64, error) {
	r.calls++
	r.ctx = ctx
	r.limit = limit
	r.retry = retry
	return 7, r.err
}
func TestExplicitEmbeddingBackfill(t *testing.T) {
	repo := &missingEmbeddingQueue{}
	backfill := NewMessageEmbeddingBackfiller(repo)
	for _, limit := range []int{-1, 0, 10001} {
		if _, err := backfill.Enqueue(context.Background(), limit, false); err == nil {
			t.Fatal("接受了无效数量")
		}
	}
	if repo.calls != 0 {
		t.Fatal("无效参数操作了数据库")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n, err := backfill.Enqueue(ctx, 100, true)
	if err != nil || n != 7 || repo.limit != 100 || !repo.retry || repo.ctx != ctx {
		t.Fatal("未按参数排队")
	}
	repo.err = errors.New("数据库失败")
	if _, err = backfill.Enqueue(ctx, 1, false); !errors.Is(err, repo.err) {
		t.Fatal("数据库错误未透传")
	}
}
