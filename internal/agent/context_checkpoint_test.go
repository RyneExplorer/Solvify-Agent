package agent

import (
	"context"
	"encoding/json"
	"solvify-agent/internal/model/entity"
	"solvify-agent/pkg/config"
	"testing"
)

func TestContextCheckpointIntegrityOwnershipAndLegacy(t *testing.T) {
	ctx := context.Background()
	rawStore := probeFileStore{t.TempDir()}
	req := Request{UserID: "u", SessionID: "s", RunID: "r", Query: "冻结素材", History: []entity.ChatMessage{{Role: "user", Content: "旧事实"}}}
	store := contextCheckpointStore{rawStore, req, config.DefaultContextPolicy(), config.DefaultRuntimeContextPolicy()}
	if err := store.Set(ctx, "cp", []byte("framework-data")); err != nil {
		t.Fatal(err)
	}
	raw, _, _ := rawStore.Get(ctx, "cp")
	cp, wrapped, err := decodeContextCheckpoint(raw)
	if err != nil || !wrapped || cp.Request.Query != req.Query {
		t.Fatal("冻结请求未保存")
	}
	for _, change := range []func(*contextCheckpoint){func(c *contextCheckpoint) { c.Request.Query = "篡改" }, func(c *contextCheckpoint) { c.RuntimePolicy.ReadTokens++ }, func(c *contextCheckpoint) { c.Data = []byte("篡改") }, func(c *contextCheckpoint) { c.Version++ }} {
		copy := cp
		change(&copy)
		encoded, _ := json.Marshal(copy)
		if _, _, err := decodeContextCheckpoint(append(append([]byte(nil), checkpointEnvelopePrefix...), encoded...)); err == nil {
			t.Fatal("元数据或正文篡改未拒绝")
		}
	}
	other := store
	other.req.UserID = "other"
	if _, _, err = other.Get(ctx, "cp"); err == nil {
		t.Fatal("跨用户恢复")
	}
	if err = rawStore.Set(ctx, "old", []byte("legacy-eino")); err != nil {
		t.Fatal(err)
	}
	data, found, err := store.Get(ctx, "old")
	if err != nil || !found || string(data) != "legacy-eino" {
		t.Fatal("旧字节兼容损坏")
	}
}

func TestContextRestoreFreezesMaterialsAndRejectsModelChanges(t *testing.T) {
	e := deepTestEngine()
	ctx := context.Background()
	original := Request{UserID: "u", SessionID: "s", RunID: "r", Query: "冻结的问题", SystemPrompt: "冻结的指令", ModelID: "m", ModelName: "gpt-4o", MaxContextLength: 64000, OutputLimit: 4096, InputLimit: 32000, UserToolConfigIDs: []string{"old-tool"}, ContextData: []ContextMaterial{{Kind: "summary", Content: "冻结摘要，预算5120"}}}
	if err := e.contextStore(original).Set(ctx, "cp", []byte("eino-state")); err != nil {
		t.Fatal(err)
	}
	incoming := original
	incoming.Query = "新素材"
	incoming.ContextData = []ContextMaterial{{Kind: "summary", Content: "无权替换的摘要"}}
	incoming.SystemPrompt = "新指令"
	incoming.UserToolConfigIDs = []string{"new-tool"}
	incoming.CheckpointID = "cp"
	incoming.ResumeData = map[string]any{"interrupt": "回答"}
	restored, err := e.restoreContextRequest(ctx, incoming)
	if err != nil || restored.Query != original.Query || restored.SystemPrompt != original.SystemPrompt || restored.UserToolConfigIDs[0] != "old-tool" || restored.ResumeData["interrupt"] != "回答" {
		t.Fatal("恢复混入新素材", err)
	}
	if len(restored.ContextData) != 1 || restored.ContextData[0].Content != original.ContextData[0].Content {
		t.Fatal("恢复时摘要数据被新请求替换")
	}
	for _, change := range []func(*Request){func(r *Request) { r.ModelName = "other" }, func(r *Request) { r.MaxContextLength = 8192 }, func(r *Request) { r.OutputLimit = 8000 }, func(r *Request) { r.InputLimit = 16000 }, func(r *Request) { r.UserID = "other" }} {
		copy := incoming
		change(&copy)
		if _, err := e.restoreContextRequest(ctx, copy); err == nil {
			t.Fatal("恢复未拒绝模型或归属变化")
		}
	}
	e.cfg.RuntimeContext = config.DefaultRuntimeContextPolicy()
	e.cfg.RuntimeContext.ReadTokens++
	if _, err := e.restoreContextRequest(ctx, incoming); err == nil {
		t.Fatal("恢复未拒绝策略变化")
	}
}
