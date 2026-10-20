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

	"solvify-agent/internal/model/entity"
	"solvify-agent/pkg/config"
)

func TestDependencyArtifactRecoveryAcrossProcesses(t *testing.T) {
	if phase := os.Getenv("SOLVIFY_DEPENDENCY_PHASE"); phase != "" {
		dir := os.Getenv("SOLVIFY_DEPENDENCY_DIR")
		path := filepath.Join(dir, "dependency.json")
		type stored struct {
			State     peState
			Artifacts map[string]entity.ContextArtifact
		}
		if phase == "save" {
			reader, ref, _ := artifactReadFixture(t, config.DefaultRuntimeContextPolicy(), strings.Repeat("来源资料", 1500)+"\nTAIL=928")
			original := reader.scope
			state := peState{Request: Request{UserID: original.UserID, SessionID: original.SessionID, RunID: original.RunID}, Plan: ExecutionPlan{Tasks: []TaskSpec{{ID: original.TaskID, Agent: original.AgentID, Status: TaskSuccess}, {ID: "next", Agent: "knowledge_research", DependsOn: []string{original.TaskID}}}}, Results: map[string]TaskResult{original.TaskID: {TaskID: original.TaskID, Agent: original.AgentID, Status: TaskSuccess, Artifacts: []artifactReference{ref}}}}
			raw, err := json.Marshal(stored{state, reader.repo.(*memoryArtifactRepo).values})
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			return
		}
		var data stored
		raw, err := os.ReadFile(path)
		if err != nil || json.Unmarshal(raw, &data) != nil {
			t.Fatal("无法恢复依赖状态")
		}
		req := data.State.Request
		req.TaskID, req.AgentName = "next", "knowledge_research"
		req.artifactReads = dependencyArtifactGrants(data.State.Plan, req.TaskID, data.State.Results)
		reader := artifactReadTool{repo: &memoryArtifactRepo{values: data.Artifacts}, scope: artifactScope(req), policy: config.DefaultRuntimeContextPolicy(), grants: req.artifactReads}
		for id := range req.artifactReads {
			page, err := reader.InvokableRun(context.Background(), fmt.Sprintf(`{"artifact_id":%q,"keyword":"TAIL="}`, id))
			if err != nil || !strings.Contains(page, "TAIL=928") {
				t.Fatalf("新进程无法续读原任务：%s %v", page, err)
			}
			return
		}
		t.Fatal("恢复后没有重建可信读取授权")
	}
	dir := t.TempDir()
	for _, phase := range []string{"save", "restore"} {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDependencyArtifactRecoveryAcrossProcesses$", "-test.timeout=45s")
		cmd.Env = append(os.Environ(), "SOLVIFY_DEPENDENCY_PHASE="+phase, "SOLVIFY_DEPENDENCY_DIR="+dir)
		output, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("独立进程%s失败：%v\n%s", phase, err, output)
		}
	}
}
