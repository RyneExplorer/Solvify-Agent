package service

import (
	"context"
	"errors"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"solvify-agent/internal/llm"
	requestdto "solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/rag"
	"solvify-agent/internal/repository"
	"solvify-agent/pkg/config"
)

type embeddingJobStore struct {
	repository.MessageEmbeddingRepository
	mu                          sync.Mutex
	jobs                        []repository.ClaimedMessageEmbedding
	completed, released, failed int
	retry                       bool
	delay                       time.Duration
	code                        string
	vector                      entity.FloatVector
}

func (r *embeddingJobStore) Claim(ctx context.Context, _ int, _ time.Duration) (*repository.ClaimedMessageEmbedding, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if len(r.jobs) == 0 {
		return nil, nil
	}
	j := r.jobs[0]
	r.jobs = r.jobs[1:]
	return &j, nil
}
func (r *embeddingJobStore) Complete(_ context.Context, _ repository.ClaimedMessageEmbedding, v entity.FloatVector) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.completed++
	r.vector = v
	return true, nil
}
func (r *embeddingJobStore) Fail(_ context.Context, _ repository.ClaimedMessageEmbedding, retry bool, delay time.Duration, code string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failed++
	r.retry = retry
	r.delay = delay
	r.code = code
	return true, nil
}
func (r *embeddingJobStore) Release(ctx context.Context, _ repository.ClaimedMessageEmbedding) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	r.released++
	return true, nil
}

func TestMessageEmbeddingFailurePolicy(t *testing.T) {
	cases := []struct {
		name    string
		embed   rag.EmbeddingFunc
		attempt int
		retry   bool
		code    string
	}{
		{"成功", func(context.Context, string) ([]float64, error) { return []float64{.1, .2}, nil }, 1, false, ""},
		{"网络故障重试", func(context.Context, string) ([]float64, error) {
			return nil, &llm.EmbeddingError{Code: "connection", Temporary: true}
		}, 2, true, "connection"},
		{"次数用尽", func(context.Context, string) ([]float64, error) {
			return nil, &llm.EmbeddingError{Code: "connection", Temporary: true}
		}, 3, false, "connection"},
		{"认证错误", func(context.Context, string) ([]float64, error) {
			return nil, &llm.EmbeddingError{Code: "authentication"}
		}, 1, false, "authentication"},
		{"未知错误终止", func(context.Context, string) ([]float64, error) { return nil, errors.New("private upstream body") }, 1, false, "embedding_failed"},
		{"维度错误", func(context.Context, string) ([]float64, error) { return []float64{1}, nil }, 1, false, "dimension"},
		{"无效数值", func(context.Context, string) ([]float64, error) { return []float64{math.NaN(), 0}, nil }, 1, false, "invalid_vector"},
		{"溢出", func(context.Context, string) ([]float64, error) { return []float64{math.MaxFloat64, 0}, nil }, 1, false, "invalid_vector"},
		{"适配器异常", func(context.Context, string) ([]float64, error) { panic("private") }, 1, false, "adapter_panic"},
		{"超时", func(ctx context.Context, _ string) ([]float64, error) { <-ctx.Done(); return []float64{1, 2}, nil }, 1, true, "timeout"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &embeddingJobStore{}
			policy := config.DefaultMessageEmbeddingConfig()
			policy.TimeoutSeconds = 1
			worker, err := NewMessageEmbeddingWorker(repo, tc.embed, policy, 2)
			if err != nil {
				t.Fatal(err)
			}
			worker.process(context.Background(), repository.ClaimedMessageEmbedding{ID: "test", Content: "正文", EmbeddingAttempts: tc.attempt})
			if tc.code == "" {
				if repo.completed != 1 || len(repo.vector) != 2 {
					t.Fatal("结果未提交")
				}
				return
			}
			if repo.completed != 0 || repo.failed != 1 || repo.code != tc.code || repo.retry != tc.retry {
				t.Fatalf("状态错误: %+v", repo)
			}
			if tc.retry && repo.delay != time.Duration(15*(1<<uint(tc.attempt-1)))*time.Second {
				t.Fatal("退避错误")
			}
		})
	}
}

func TestMessageEmbeddingConcurrencyAndShutdown(t *testing.T) {
	repo := &embeddingJobStore{jobs: []repository.ClaimedMessageEmbedding{{ID: "a", EmbeddingAttempts: 1}, {ID: "b", EmbeddingAttempts: 1}, {ID: "c", EmbeddingAttempts: 1}}}
	var active, max atomic.Int32
	entered := make(chan struct{}, 3)
	worker, err := NewMessageEmbeddingWorker(repo, func(ctx context.Context, _ string) ([]float64, error) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := max.Load(); n > old && !max.CompareAndSwap(old, n); old = max.Load() {
		}
		entered <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}, config.DefaultMessageEmbeddingConfig(), 2)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { worker.Run(ctx); close(done) }()
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(3 * time.Second):
			t.Fatal("Worker未开始")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("停机未等待完成")
	}
	if active.Load() != 0 || max.Load() != 2 || repo.released != 2 || len(repo.jobs) != 1 || repo.failed != 0 {
		t.Fatalf("并发或停机归还错误: %+v", repo)
	}
}

type embeddingSaveRepo struct {
	repository.ChatMessageRepo
	messages []entity.ChatMessage
	err      error
	seen     context.Context
}

func (r *embeddingSaveRepo) Create(ctx context.Context, m *entity.ChatMessage) error {
	r.seen = ctx
	if r.err != nil {
		return r.err
	}
	r.messages = append(r.messages, *m)
	return nil
}
func TestChatSaveAtomicallyRegistersEmbedding(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		repo := &embeddingSaveRepo{}
		s := &chatService{messageRepo: repo, queueMessageEmbeddings: enabled, embedFn: func(context.Context, string) ([]float64, error) {
			t.Fatal("保存消息不应调用向量服务")
			return nil, nil
		}}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		req := requestdto.SendMessageRequest{Content: "问题", SearchMode: "quick"}
		if _, err := s.saveUserMessage(ctx, "session", req); err != nil {
			t.Fatal(err)
		}
		if err := s.saveAssistantMessage(ctx, "session", "answer", "回答", req, nil, nil); err != nil {
			t.Fatal(err)
		}
		if err := s.saveAssistantMessage(ctx, "session", "empty", " \n", req, nil, nil); err != nil {
			t.Fatal(err)
		}
		for i, m := range repo.messages {
			want := ""
			if enabled && i < 2 {
				want = "queued"
			}
			if m.EmbeddingStatus != want {
				t.Fatal("正文INSERT未携带正确任务状态")
			}
		}
		if repo.seen != ctx {
			t.Fatal("保存未沿用调用上下文")
		}
		repo.err = errors.New("数据库不可用")
		if _, err := s.saveUserMessage(ctx, "session", req); !errors.Is(err, repo.err) {
			t.Fatal("保存失败被吞掉")
		}
	}
}
