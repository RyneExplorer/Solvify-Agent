package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"solvify-agent/internal/repository"
	"solvify-agent/pkg/config"
)

func TestArtifactDependencyReadAfterCheckpointRebuild(t *testing.T) {
	reader, ref, _ := artifactReadFixture(t, config.DefaultRuntimeContextPolicy(), strings.Repeat("前文资料。", 1500)+"尾部验收AZURE-7319")
	ref.OwnerTaskID, ref.OwnerAgentID = reader.scope.TaskID, reader.scope.AgentID
	owner := ref.OwnerTaskID
	state := peState{Plan: ExecutionPlan{Tasks: []TaskSpec{{ID: owner}, {ID: "next", DependsOn: []string{owner}}, {ID: "unrelated"}}}, Results: map[string]TaskResult{owner: {TaskID: owner, Agent: reader.scope.AgentID, Status: TaskSuccess, Artifacts: []artifactReference{ref}}}}
	raw, _ := json.Marshal(state)
	var restored peState
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	reader.scope.TaskID = "next"
	args := fmt.Sprintf(`{"artifact_id":%q,"keyword":"尾部验收"}`, ref.ArtifactID)
	if _, err := reader.InvokableRun(context.Background(), args); !errors.Is(err, repository.ErrArtifactUnavailable) {
		t.Fatalf("无依赖授权仍可跨任务读取：%v", err)
	}
	reader.grants = dependencyArtifactGrants(restored.Plan, "next", restored.Results)
	page, err := reader.InvokableRun(context.Background(), args)
	var result artifactReference
	if err != nil || json.Unmarshal([]byte(page), &result) != nil || !strings.Contains(result.Content, "AZURE-7319") || result.OwnerTaskID != owner {
		t.Fatalf("恢复后未能定位原任务产物：%s %v", page, err)
	}
	for _, dimension := range []string{"user", "session", "run"} {
		t.Run(dimension, func(t *testing.T) {
			other := reader
			switch dimension {
			case "user":
				other.scope.UserID = "other"
			case "session":
				other.scope.SessionID = "other"
			case "run":
				other.scope.RunID = "other"
			}
			if _, err := other.InvokableRun(context.Background(), args); !errors.Is(err, repository.ErrArtifactUnavailable) {
				t.Fatalf("依赖授权绕过%s隔离：%v", dimension, err)
			}
		})
	}
	for _, dimension := range []string{"hash", "version"} {
		t.Run(dimension, func(t *testing.T) {
			other := reader
			grant := reader.grants[ref.ArtifactID]
			if dimension == "hash" {
				grant.Hash = "changed"
			} else {
				grant.Version++
			}
			other.grants = map[string]artifactReadGrant{ref.ArtifactID: grant}
			if _, err := other.InvokableRun(context.Background(), args); err == nil {
				t.Fatalf("依赖授权未校验%s", dimension)
			}
		})
	}
	reader.grants = dependencyArtifactGrants(restored.Plan, "unrelated", restored.Results)
	if len(reader.grants) != 0 {
		t.Fatal("无关任务取得读取授权")
	}
}

func TestPlanExecuteDependencyArtifactReadCompletes(t *testing.T) {
	e := deepTestEngine()
	e.cfg.RuntimeContext = config.DefaultRuntimeContextPolicy()
	e.cfg.RuntimeContext.ArtifactThreshold = 100
	repo := &memoryArtifactRepo{}
	e.WithArtifactRepo(repo)
	var calls atomic.Int32
	probe := peResultTestTool{name: "read_fixture", result: strings.Repeat("保真的研究原文。", 300) + "\nTAIL=AZURE-7319", calls: &calls}
	e.RegisterInternal(probe.name, 1, false, DomainKnowledge, func(context.Context, string, []string) einoTool.BaseTool { return probe })
	ctx := withPrebuiltTools(deepTestContext(t), prebuiltToolsBundle{Buckets: map[ToolDomain][]einoTool.BaseTool{DomainKnowledge: {probe}}})
	m := peModel(`{"strategy":"sequential","tasks":[{"id":"a","agent":"knowledge_research","title":"取得资料"},{"id":"b","agent":"knowledge_research","title":"续读原文","depends_on":["a"]},{"id":"s","agent":"synthesis_review","title":"汇总","depends_on":["b"]}]}`, new(atomic.Int32))
	base := m.respond
	var mu sync.Mutex
	var ref artifactReference
	m.respond = func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
		if !strings.Contains(messages[0].Content, "研究知识库") {
			return base(ctx, messages)
		}
		mu.Lock()
		defer mu.Unlock()
		last := messages[len(messages)-1]
		if strings.Contains(messages[1].Content, "当前任务：取得资料") {
			if last.Role != schema.Tool {
				return peCall(probe.name, map[string]any{}), nil
			}
			if err := json.Unmarshal([]byte(last.Content), &ref); err != nil || ref.ArtifactID == "" {
				return nil, fmt.Errorf("产物未外置")
			}
			return schema.AssistantMessage("资料已取得，末尾需核验", nil), nil
		}
		if last.Role != schema.Tool {
			return peCall(artifactReadToolName, map[string]any{"artifact_id": ref.ArtifactID, "keyword": "TAIL="}), nil
		}
		var page artifactReference
		if json.Unmarshal([]byte(last.Content), &page) != nil || page.Content != "TAIL=AZURE-7319" || page.OwnerTaskID != "a" {
			return nil, fmt.Errorf("依赖任务没有读到原始末尾：%s", last.Content)
		}
		return schema.AssistantMessage("已核验TAIL=AZURE-7319", nil), nil
	}
	events := collectPEEvents(t, ctx, e, Request{UserID: "u", SessionID: "dependency", Query: "取得资料后续读末尾"}, m)
	done := 0
	for _, event := range events {
		if event.Type == EventError {
			t.Fatal(event.Error)
		}
		if event.Type == EventDone {
			done++
		}
	}
	if done != 1 || calls.Load() != 1 {
		t.Fatalf("未完成或重复执行源工具：done=%d calls=%d", done, calls.Load())
	}
}
