package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/internal/repository"
)

type artifactArgumentCheckpointRepo struct {
	repository.AgentCheckpointRepo
	store probeFileStore
}

func (r artifactArgumentCheckpointRepo) Save(ctx context.Context, id, _ string, raw []byte, _ time.Time) error {
	return r.store.Set(ctx, id, raw)
}
func (r artifactArgumentCheckpointRepo) Find(ctx context.Context, id string) ([]byte, bool, error) {
	return r.store.Get(ctx, id)
}
func (r artifactArgumentCheckpointRepo) Delete(_ context.Context, id string) error {
	err := os.Remove(filepath.Join(r.store.dir, id+".checkpoint"))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func TestPlanExecuteArtifactArgumentRecoveryAcrossProcesses(t *testing.T) {
	if phase := os.Getenv("SOLVIFY_ARTIFACT_ARGUMENT_PHASE"); phase != "" {
		runArtifactArgumentRecoveryProcess(t, phase, os.Getenv("SOLVIFY_ARTIFACT_ARGUMENT_DIR"))
		return
	}
	dir := t.TempDir()
	for _, phase := range []string{"save", "restore"} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPlanExecuteArtifactArgumentRecoveryAcrossProcesses$", "-test.v")
		cmd.Env = append(os.Environ(), "SOLVIFY_ARTIFACT_ARGUMENT_PHASE="+phase, "SOLVIFY_ARTIFACT_ARGUMENT_DIR="+dir)
		output, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("独立进程%s失败: %v\n%s", phase, err, output)
		}
	}
}

func runArtifactArgumentRecoveryProcess(t *testing.T, phase, dir string) {
	t.Helper()
	repo := &memoryArtifactRepo{}
	if phase == "restore" {
		raw, err := os.ReadFile(filepath.Join(dir, "artifacts.json"))
		if err != nil || json.Unmarshal(raw, &repo.values) != nil {
			t.Fatal("产物原文未持久保存", err)
		}
	}
	e := deepTestEngine()
	e.cfg.DeepMaxSubagentIterations = 8
	e.WithArtifactRepo(repo)
	e.WithCheckpointRepo(artifactArgumentCheckpointRepo{store: probeFileStore{dir}})
	probe := guardProbeTool{calls: new(atomic.Int32)}
	e.RegisterInternal("guard_probe", 1, false, DomainKnowledge, func(context.Context, string, []string) einoTool.BaseTool { return probe })
	// 测试工具集显式提供澄清入口，仅用于验证现有checkpoint机制，不修改生产角色权限。
	e.RegisterInternal(clarifyToolName, 2, false, DomainKnowledge, func(context.Context, string, []string) einoTool.BaseTool { return guardClarifyTool{} })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ctx = withPrebuiltTools(ctx, prebuiltToolsBundle{Buckets: map[ToolDomain][]einoTool.BaseTool{DomainKnowledge: {probe, guardClarifyTool{}}}})
	plan := `{"strategy":"sequential","tasks":[{"id":"a","agent":"knowledge_research","title":"抽样核验清单"},{"id":"s","agent":"synthesis_review","title":"汇总","depends_on":["a"]}]}`
	m := peModel(plan, new(atomic.Int32))
	base := m.respond
	var restored bool
	m.respond = func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
		if !strings.Contains(messages[0].Content, "研究知识库") {
			return base(ctx, messages)
		}
		var ref artifactReference
		for _, message := range messages {
			if message.Role == schema.Tool && message.ToolCallID == "call-guard_probe" {
				_ = json.Unmarshal([]byte(message.Content), &ref)
			}
		}
		last := messages[len(messages)-1]
		if last.Role != schema.Tool {
			return peCall("guard_probe", map[string]any{}), nil
		}
		if last.ToolCallID == "call-guard_probe" {
			return peCall(artifactReadToolName, map[string]any{"artifact_id": ref.ArtifactID, "offset": -1}), nil
		}
		if _, recoverable := artifactArgumentFailure(artifactReadToolName, last.Content); recoverable {
			return peCall(clarifyToolName, map[string]any{"question": "是否继续核验"}), nil
		}
		if last.ToolCallID == "call-"+clarifyToolName {
			value, _ := adk.GetSessionValue(ctx, peSessionKey)
			raw, _ := value.(string)
			var state peState
			if json.Unmarshal([]byte(raw), &state) != nil || state.ArtifactArgumentFailures["a"] == "" {
				return nil, fmt.Errorf("独立进程恢复丢失待纠正参数错误")
			}
			restored = true
			return peCall(artifactReadToolName, map[string]any{"artifact_id": ref.ArtifactID, "offset": 0, "limit": 100}), nil
		}
		var page artifactReadPage
		if json.Unmarshal([]byte(last.Content), &page) != nil || page.NextOffset != 100 || page.Content == "" {
			return nil, fmt.Errorf("恢复纠正后未读取真实原文")
		}
		return schema.AssistantMessage("已完成原文抽样核验", nil), nil
	}
	req := Request{Query: "核验文档", UserID: "u", SessionID: "argument-recovery"}
	if phase == "restore" {
		raw, err := os.ReadFile(filepath.Join(dir, "interrupt.json"))
		var interrupt Event
		if err != nil || json.Unmarshal(raw, &interrupt) != nil {
			t.Fatal("缺少恢复标识", err)
		}
		req.CheckpointID = interrupt.CheckpointID
		req.ResumeData = map[string]any{interrupt.InterruptID: "继续"}
	}
	events := collectPEEvents(t, ctx, e, req, m)
	var interrupted, succeeded, done bool
	for _, ev := range events {
		if ev.Type == EventError || ev.TaskStatus == TaskError {
			t.Fatalf("恢复流程失败: %+v", ev)
		}
		if ev.Type == EventInterrupt {
			raw, _ := json.Marshal(ev)
			if err := os.WriteFile(filepath.Join(dir, "interrupt.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			interrupted = true
		}
		succeeded = succeeded || ev.Type == EventTaskResult && ev.TaskID == "a" && ev.TaskStatus == TaskSuccess
		done = done || ev.Type == EventDone
	}
	if phase == "save" {
		if !interrupted || probe.calls.Load() != 1 || succeeded || done {
			t.Fatal("参数错误后的澄清未正确暂停")
		}
		raw, _ := json.Marshal(repo.values)
		if err := os.WriteFile(filepath.Join(dir, "artifacts.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	} else if !restored || !succeeded || !done || interrupted || probe.calls.Load() != 0 {
		t.Fatalf("恢复丢失错误、未清理错误或重执行原工具: restored=%t success=%t done=%t source=%d", restored, succeeded, done, probe.calls.Load())
	}
}
