package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"solvify-agent/internal/llm"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/rag"
	"solvify-agent/internal/repository"
	"solvify-agent/pkg/config"
	"solvify-agent/pkg/logger"
)

// MessageEmbeddingWorker 生命周期由App管理；并发槽内领取，避免预领取任务等待过期。
type MessageEmbeddingWorker struct {
	repo      repository.MessageEmbeddingRepository
	embed     rag.EmbeddingFunc
	policy    config.MessageEmbeddingConfig
	dimension int
}

func NewMessageEmbeddingWorker(repo repository.MessageEmbeddingRepository, embed rag.EmbeddingFunc, policy config.MessageEmbeddingConfig, dimension int) (*MessageEmbeddingWorker, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	if repo == nil || embed == nil || dimension < 1 {
		return nil, fmt.Errorf("消息向量Worker依赖或维度无效")
	}
	return &MessageEmbeddingWorker{repo: repo, embed: embed, policy: policy, dimension: dimension}, nil
}

// Run 在ctx取消后等待所有在途任务结束/归还，返回后才可关闭DB和Embedding缓存。
func (w *MessageEmbeddingWorker) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for i := 0; i < w.policy.Concurrency; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); w.loop(ctx) }()
	}
	wg.Wait()
}

func (w *MessageEmbeddingWorker) loop(ctx context.Context) {
	for ctx.Err() == nil {
		dbCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		job, err := w.repo.Claim(dbCtx, w.policy.MaxAttempts, time.Duration(w.policy.TimeoutSeconds+30)*time.Second)
		cancel()
		if err != nil && ctx.Err() == nil {
			logger.Warn("领取消息向量任务失败，稍后再试")
		}
		if job != nil && err == nil {
			w.process(ctx, *job)
			continue
		}
		timer := time.NewTimer(time.Duration(w.policy.PollSeconds) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (w *MessageEmbeddingWorker) process(ctx context.Context, job repository.ClaimedMessageEmbedding) {
	started := time.Now()
	callCtx, cancel := context.WithTimeout(ctx, time.Duration(w.policy.TimeoutSeconds)*time.Second)
	vec, err := w.embedSafely(callCtx, job.Content)
	// 请求超时即使适配器返回成功也不能提交迟到结果。
	if callCtx.Err() != nil {
		err = callCtx.Err()
	}
	cancel()
	// 允许在正常停机取消后用有界上下文归还任务；Run等待此步骤结束。
	settleCtx, settleCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer settleCancel()
	if ctx.Err() != nil {
		if _, e := w.repo.Release(settleCtx, job); e != nil {
			logger.Warnf("归还消息向量任务失败，将由租约恢复: message_id=%s", job.ID)
		}
		return
	}
	var converted entity.FloatVector
	if err == nil {
		converted = make(entity.FloatVector, len(vec))
		if len(vec) != w.dimension {
			err = &llm.EmbeddingError{Code: "dimension", Message: "向量维度不匹配"}
		}
		for i, v := range vec {
			converted[i] = float32(v)
			if math.IsNaN(v) || math.IsInf(v, 0) || math.IsInf(float64(converted[i]), 0) {
				err = &llm.EmbeddingError{Code: "invalid_vector", Message: "向量包含无效值"}
				break
			}
		}
	}
	code := ""
	retry := false
	if err != nil {
		code = "embedding_failed"
		var classified *llm.EmbeddingError
		if errors.As(err, &classified) {
			code = classified.Code
			retry = classified.Retryable()
		}
		if errors.Is(err, context.DeadlineExceeded) {
			code = "timeout"
			retry = true
		}
	} else {
		ok, e := w.repo.Complete(settleCtx, job, converted)
		if e == nil {
			if ok {
				logger.Infof("消息向量任务完成: message_id=%s attempt=%d elapsed_ms=%d", job.ID, job.EmbeddingAttempts, time.Since(started).Milliseconds())
			}
			return
		}
		// 写入错误可能是持久化或schema故障，保留租约由数据库回收；不误报成功。
		logger.Warnf("消息向量结果提交失败，将由租约恢复: message_id=%s", job.ID)
		return
	}
	retry = retry && job.EmbeddingAttempts < w.policy.MaxAttempts
	delay := time.Duration(w.policy.RetrySeconds) * time.Second * time.Duration(1<<uint(job.EmbeddingAttempts-1))
	ok, e := w.repo.Fail(settleCtx, job, retry, delay, code)
	if e != nil {
		logger.Warnf("消息向量失败状态保存失败，将由租约恢复: message_id=%s", job.ID)
		return
	}
	if ok {
		logger.Warnf("消息向量任务失败: message_id=%s attempt=%d retry=%t reason=%s", job.ID, job.EmbeddingAttempts, retry, code)
	}
}

func (w *MessageEmbeddingWorker) embedSafely(ctx context.Context, content string) (vector []float64, err error) {
	defer func() {
		if recover() != nil {
			vector = nil
			err = &llm.EmbeddingError{Code: "adapter_panic", Message: "向量适配器异常"}
		}
	}()
	return w.embed(ctx, content)
}
