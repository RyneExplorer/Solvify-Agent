package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"solvify-agent/pkg/config"
)

// 仅测试使用的持久存储，证明独立进程而非内存搬运恢复。
type probeFileStore struct{ dir string }

func (s probeFileStore) Get(_ context.Context, id string) ([]byte, bool, error) {
	raw, err := os.ReadFile(filepath.Join(s.dir, id+".checkpoint"))
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	return raw, err == nil, err
}
func (s probeFileStore) Set(_ context.Context, id string, raw []byte) error {
	return os.WriteFile(filepath.Join(s.dir, id+".checkpoint"), raw, 0600)
}

type probeContextMetadata struct {
	Version     int      `json:"version"`
	Summary     string   `json:"summary"`
	ArtifactIDs []string `json:"artifact_ids"`
	SourceHash  string   `json:"source_hash"`
}

const probeSessionKey = "ContextMetadataProbeV1"

var probeMetadata = probeContextMetadata{Version: 3, Summary: "已完成研究，等待澄清", ArtifactIDs: []string{"artifact-sample"}, SourceHash: "sha256:sample"}

type probeContextMiddleware struct {
	adk.BaseChatModelAgentMiddleware
	resuming bool
	checked  bool
}

func (m *probeContextMiddleware) BeforeModelRewriteState(ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext) (context.Context, *adk.ChatModelAgentState, error) {
	value, ok := adk.GetSessionValue(ctx, probeSessionKey)
	raw, valid := value.(string)
	var restored probeContextMetadata
	if !ok || !valid || json.Unmarshal([]byte(raw), &restored) != nil || restored.Version != probeMetadata.Version || restored.Summary != probeMetadata.Summary || len(restored.ArtifactIDs) != 1 || restored.ArtifactIDs[0] != probeMetadata.ArtifactIDs[0] || restored.SourceHash != probeMetadata.SourceHash {
		return ctx, state, fmt.Errorf("checkpoint元数据丢失: %v", value)
	}
	if m.resuming {
		found := false
		for _, msg := range state.Messages {
			if msg.Role == schema.Tool && strings.Contains(msg.Content, "恢复回答") {
				found = true
			}
		}
		if !found {
			return ctx, state, fmt.Errorf("澄清结果未恢复")
		}
		m.checked = true
	}
	return ctx, state, nil
}

func TestContextCheckpointMetadataAcrossProcesses(t *testing.T) {
	if phase := os.Getenv("SOLVIFY_CONTEXT_PROBE_PHASE"); phase != "" {
		runContextProbeChild(t, phase, os.Getenv("SOLVIFY_CONTEXT_PROBE_DIR"))
		return
	}
	dir := t.TempDir()
	for _, phase := range []string{"save", "restore"} {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestContextCheckpointMetadataAcrossProcesses$", "-test.v")
		cmd.Env = append(os.Environ(), "SOLVIFY_CONTEXT_PROBE_PHASE="+phase, "SOLVIFY_CONTEXT_PROBE_DIR="+dir)
		output, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("独立进程%s失败: %v\n%s", phase, err, output)
		}
		t.Logf("独立进程%s成功：摘要/引用/版本/哈希逐字段一致", phase)
	}
}
func runContextProbeChild(t *testing.T, phase, dir string) {
	ctx := context.Background()
	middleware := &probeContextMiddleware{resuming: phase == "restore"}
	m := &deepTestModel{respond: func(_ context.Context, _ []*schema.Message) (*schema.Message, error) {
		if phase == "save" {
			return peCall(clarifyToolName, map[string]any{"question": "请选择口径"}), nil
		}
		return schema.AssistantMessage("恢复成功", nil), nil
	}}
	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{Name: "context_probe", Instruction: "测试上下文恢复", Model: m, MaxIterations: 4, Handlers: []adk.ChatModelAgentMiddleware{middleware}, ToolsConfig: adk.ToolsConfig{ToolsNodeConfig: compose.ToolsNodeConfig{Tools: []einoTool.BaseTool{guardClarifyTool{}}, ToolCallMiddlewares: []compose.ToolMiddleware{{Invokable: buildClarifyMiddleware(map[string]bool{clarifyToolName: true})}}}}})
	if err != nil {
		t.Fatal(err)
	}
	store := contextCheckpointStore{CheckPointStore: probeFileStore{dir: dir}, req: Request{UserID: "probe-user", SessionID: "probe-session", RunID: "probe-run", Query: "冻结问题", ModelName: "gpt-4o"}, policy: config.DefaultContextPolicy(), runtimePolicy: config.DefaultRuntimeContextPolicy()}
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: agent, CheckPointStore: store})
	var iter *adk.AsyncIterator[*adk.AgentEvent]
	if phase == "save" {
		raw, _ := json.Marshal(probeMetadata)
		iter = runner.Run(ctx, []*schema.Message{schema.UserMessage("问题")}, adk.WithCheckPointID("probe"), adk.WithSessionValues(map[string]any{probeSessionKey: string(raw)}))
	} else {
		target, err := os.ReadFile(filepath.Join(dir, "interrupt-id"))
		if err != nil {
			t.Fatal(err)
		}
		iter, err = runner.ResumeWithParams(ctx, "probe", &adk.ResumeParams{Targets: map[string]any{string(target): "恢复回答"}})
		if err != nil {
			t.Fatal(err)
		}
	}
	interrupted := false
	for {
		event, ok := iter.Next()
		if !ok {
			break
		}
		if event.Err != nil {
			t.Fatal(event.Err)
		}
		if event.Action != nil && event.Action.Interrupted != nil {
			for _, interrupt := range event.Action.Interrupted.InterruptContexts {
				if err := os.WriteFile(filepath.Join(dir, "interrupt-id"), []byte(interrupt.ID), 0600); err != nil {
					t.Fatal(err)
				}
				interrupted = true
				break
			}
		}
	}
	if phase == "save" && !interrupted {
		t.Fatal("未生成真实checkpoint")
	}
	if phase == "restore" && !middleware.checked {
		t.Fatal("恢复未经过元数据校验")
	}
}
