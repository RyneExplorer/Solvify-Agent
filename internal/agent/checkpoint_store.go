package agent

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"

	"solvify-agent/internal/repository"
	"solvify-agent/pkg/logger"
)

// CheckpointTTL 是 agent_checkpoints 表中 checkpoint 的默认存活时长
const CheckpointTTL = 24 * time.Hour

// InMemoryCheckPointStore 是 core.CheckPointStore 的内存实现。
// 用于本地开发和单元测试。线程安全。
type InMemoryCheckPointStore struct {
	mu   sync.RWMutex
	data map[string][]byte
}

func NewInMemoryCheckPointStore() *InMemoryCheckPointStore {
	return &InMemoryCheckPointStore{data: make(map[string][]byte)}
}

func (s *InMemoryCheckPointStore) Get(_ context.Context, checkPointID string) ([]byte, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[checkPointID]
	return bytes.Clone(v), ok, nil
}

func (s *InMemoryCheckPointStore) Set(_ context.Context, checkPointID string, checkPoint []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[checkPointID] = bytes.Clone(checkPoint)
	return nil
}

func (s *InMemoryCheckPointStore) Delete(_ context.Context, checkPointID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, checkPointID)
	return nil
}

// DBCheckPointStore 是 core.CheckPointStore 的数据库实现。
// 通过 AgentCheckpointRepo 持久化 checkpoint 原始字节，支持按 checkpointID 读写删。
type DBCheckPointStore struct {
	repo      repository.AgentCheckpointRepo
	sessionID string
	ttl       time.Duration
}

// NewDBCheckPointStore 从 repo 创建 DB 版 checkpoint store。
// sessionID 用于把 checkpoint 和聊天会话关联起来；ttl 控制 checkpoint 过期时间。
func NewDBCheckPointStore(repo repository.AgentCheckpointRepo, sessionID string, ttl time.Duration) *DBCheckPointStore {
	if ttl <= 0 {
		ttl = CheckpointTTL
	}
	return &DBCheckPointStore{repo: repo, sessionID: sessionID, ttl: ttl}
}

func (s *DBCheckPointStore) Get(ctx context.Context, checkPointID string) ([]byte, bool, error) {
	return s.repo.Find(ctx, checkPointID)
}

func (s *DBCheckPointStore) Set(ctx context.Context, checkPointID string, checkPoint []byte) error {
	expiredAt := time.Now().Add(s.ttl)
	return s.repo.Save(ctx, checkPointID, s.sessionID, checkPoint, expiredAt)
}

func (s *DBCheckPointStore) Delete(ctx context.Context, checkPointID string) error {
	return s.repo.Delete(ctx, checkPointID)
}

func (e *Engine) resolveCheckpointID(req Request) string {
	if req.CheckpointID != "" {
		logger.Infof("[Agent] 恢复执行：复用 checkpointID=%s", req.CheckpointID)
		return req.CheckpointID
	}
	return fmt.Sprintf("agent-%s-%s-%s-%d", req.SessionID, req.UserID, randomStr8(), time.Now().UnixNano())
}

func randomStr(n int) string {
	const alpha = "0123456789abcdef"
	buf := make([]byte, n)
	seed := time.Now().UnixNano()
	for i := 0; i < n; i++ {
		seed = seed*1103515245 + 12345
		buf[i] = alpha[int(seed>>16)&15]
	}
	return string(buf)
}

func randomStr8() string { return randomStr(8) }
