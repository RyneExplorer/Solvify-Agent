package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/compose"

	"solvify-agent/pkg/config"
)

var checkpointEnvelopePrefix = []byte("SOLVIFY-CONTEXT-1\n")

type contextCheckpoint struct {
	Version       int
	Request       Request
	Policy        config.ContextPolicy
	RuntimePolicy config.RuntimeContextPolicy
	Data          []byte
	Hash          string
	Citations     []citationSource `json:",omitempty"`
}

type contextCheckpointStore struct {
	compose.CheckPointStore
	req           Request
	policy        config.ContextPolicy
	runtimePolicy config.RuntimeContextPolicy
}

func (s contextCheckpointStore) Set(ctx context.Context, id string, data []byte) error {
	req := s.req
	req.ResumeData = nil
	req.CheckpointID = ""
	req.History = append(req.History[:0:0], req.History...)
	envelope := contextCheckpoint{Version: 1, Request: req, Policy: s.policy, RuntimePolicy: s.runtimePolicy, Data: data}
	envelope.Citations = req.citations.snapshot()
	unsigned, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	envelope.Hash = contentHash(string(unsigned))
	raw, err := json.Marshal(envelope)
	if err != nil {
		return err
	}
	return s.CheckPointStore.Set(ctx, id, append(append([]byte(nil), checkpointEnvelopePrefix...), raw...))
}

func (s contextCheckpointStore) Get(ctx context.Context, id string) ([]byte, bool, error) {
	raw, found, err := s.CheckPointStore.Get(ctx, id)
	if err != nil || !found {
		return raw, found, err
	}
	envelope, wrapped, err := decodeContextCheckpoint(raw)
	if err != nil {
		return nil, false, err
	}
	if !wrapped {
		return raw, true, nil
	}
	if envelope.Request.UserID != s.req.UserID || envelope.Request.SessionID != s.req.SessionID || envelope.Request.RunID != s.req.RunID {
		return nil, false, fmt.Errorf("上下文中断点归属不匹配")
	}
	return envelope.Data, true, nil
}

func (s contextCheckpointStore) Delete(ctx context.Context, id string) error {
	if d, ok := s.CheckPointStore.(interface {
		Delete(context.Context, string) error
	}); ok {
		return d.Delete(ctx, id)
	}
	return nil
}

func decodeContextCheckpoint(raw []byte) (contextCheckpoint, bool, error) {
	var cp contextCheckpoint
	if !bytes.HasPrefix(raw, checkpointEnvelopePrefix) {
		return cp, false, nil
	}
	if json.Unmarshal(raw[len(checkpointEnvelopePrefix):], &cp) != nil || cp.Version != 1 {
		return cp, true, fmt.Errorf("上下文中断点版本或哈希无效")
	}
	hash := cp.Hash
	cp.Hash = ""
	unsigned, err := json.Marshal(cp)
	cp.Hash = hash
	if err != nil || hash != contentHash(string(unsigned)) {
		return cp, true, fmt.Errorf("上下文中断点版本或哈希无效")
	}
	return cp, true, nil
}

func (e *Engine) contextStore(req Request) compose.CheckPointStore {
	return contextCheckpointStore{e.buildCheckpointStore(req.SessionID), req, config.ContextDefaults(), e.runtimePolicy()}
}

func (e *Engine) restoreContextRequest(ctx context.Context, req Request) (Request, error) {
	if req.CheckpointID == "" || len(req.ResumeData) == 0 {
		return req, nil
	}
	raw, found, err := e.buildCheckpointStore(req.SessionID).Get(ctx, req.CheckpointID)
	if err != nil {
		return req, err
	}
	if !found {
		return req, fmt.Errorf("上下文中断点不存在或已过期")
	}
	cp, wrapped, err := decodeContextCheckpoint(raw)
	if err != nil || !wrapped {
		return req, err
	}
	old := cp.Request
	if old.UserID != req.UserID || old.SessionID != req.SessionID {
		return req, fmt.Errorf("上下文恢复归属不匹配")
	}
	if old.ModelID != req.ModelID || old.ModelName != req.ModelName || old.ModelType != req.ModelType || old.MaxContextLength != req.MaxContextLength || old.OutputLimit != req.OutputLimit || (old.InputLimit != 0 && old.InputLimit != req.InputLimit) {
		return req, fmt.Errorf("恢复模型或窗口已变化，请使用原模型继续或重新发起任务")
	}
	if cp.Policy != config.ContextDefaults() || cp.RuntimePolicy != e.runtimePolicy() {
		return req, fmt.Errorf("上下文策略已变化，无法安全恢复")
	}
	// 旧快照未记录独立输入限制；沿用本次已确认限制，不猜测模型能力。
	old.InputLimit = req.InputLimit
	old.ResumeData = req.ResumeData
	old.CheckpointID = req.CheckpointID
	old.citations = &citationRegistry{}
	for _, source := range cp.Citations {
		old.citations.add(source)
	}
	return old, nil
}
