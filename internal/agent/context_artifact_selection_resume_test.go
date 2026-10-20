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

	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

func TestArtifactSelectionRecoveryAcrossProcesses(t *testing.T) {
	if phase := os.Getenv("SOLVIFY_SELECTION_PHASE"); phase != "" {
		runArtifactSelectionProcess(t, phase, os.Getenv("SOLVIFY_SELECTION_DIR"))
		return
	}
	dir := t.TempDir()
	for _, phase := range []string{"save", "restore"} {
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestArtifactSelectionRecoveryAcrossProcesses$", "-test.v")
		cmd.Env = append(os.Environ(), "SOLVIFY_SELECTION_PHASE="+phase, "SOLVIFY_SELECTION_DIR="+dir)
		out, err := cmd.CombinedOutput()
		cancel()
		if err != nil {
			t.Fatalf("定位证据独立进程%s失败: %v\n%s", phase, err, out)
		}
	}
}

func runArtifactSelectionProcess(t *testing.T, phase, dir string) {
	t.Helper()
	repo := &memoryArtifactRepo{}
	if phase == "restore" {
		raw, err := os.ReadFile(filepath.Join(dir, "artifacts.json"))
		if err != nil || json.Unmarshal(raw, &repo.values) != nil {
			t.Fatal("缺少持久原文", err)
		}
	}
	e := deepTestEngine()
	e.cfg.DeepMaxSubagentIterations = 8
	e.WithArtifactRepo(repo)
	e.WithCheckpointRepo(artifactArgumentCheckpointRepo{store: probeFileStore{dir}})
	probe := guardProbeTool{calls: new(atomic.Int32)}
	e.RegisterInternal("guard_probe", 1, false, DomainKnowledge, func(context.Context, string, []string) einoTool.BaseTool { return probe })
	e.RegisterInternal(clarifyToolName, 2, false, DomainKnowledge, func(context.Context, string, []string) einoTool.BaseTool { return guardClarifyTool{} })
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	ctx = withPrebuiltTools(ctx, prebuiltToolsBundle{Buckets: map[ToolDomain][]einoTool.BaseTool{DomainKnowledge: {probe, guardClarifyTool{}}}})
	m := peModel(`{"strategy":"sequential","tasks":[{"id":"a","agent":"knowledge_research","title":"定位原文末尾"},{"id":"s","agent":"synthesis_review","title":"汇总证据","depends_on":["a"]}]}`, new(atomic.Int32))
	base := m.respond
	var restored, synthesized bool
	m.respond = func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
		if !strings.Contains(messages[0].Content, "研究知识库") {
			if strings.Contains(messages[0].Content, "仅使用结构化研究结果") {
				encodedTail, _ := json.Marshal(guardProbeTail)
				if !strings.Contains(messages[1].Content, string(encodedTail[1:len(encodedTail)-1])) || strings.Contains(messages[1].Content, strings.Repeat(guardProbeRaw, 3)) {
					return nil, fmt.Errorf("恢复后汇总丢失实际读取位置或退回首页")
				}
				synthesized = true
			}
			return base(ctx, messages)
		}
		last := messages[len(messages)-1]
		if last.Role != schema.Tool {
			return peCall("guard_probe", map[string]any{}), nil
		}
		if last.ToolCallID == "call-guard_probe" {
			var ref artifactReference
			_ = json.Unmarshal([]byte(last.Content), &ref)
			return peCall(artifactReadToolName, map[string]any{"artifact_id": ref.ArtifactID, "keyword": guardProbeTail}), nil
		}
		if last.ToolCallID == "call-"+artifactReadToolName {
			var page artifactReference
			if json.Unmarshal([]byte(last.Content), &page) != nil || page.Selection == nil || page.Offset <= 0 || page.Content != guardProbeTail {
				return nil, fmt.Errorf("未定位真实尾部")
			}
			return peCall(clarifyToolName, map[string]any{"question": "是否采用此结论"}), nil
		}
		if last.ToolCallID == "call-"+clarifyToolName {
			for _, msg := range messages {
				if msg.Role != schema.Tool || msg.ToolCallID != "call-"+artifactReadToolName {
					continue
				}
				var page artifactReference
				if json.Unmarshal([]byte(msg.Content), &page) == nil && page.Selection != nil && page.Content == guardProbeTail && page.Offset > 0 {
					restored = true
				}
			}
			if !restored {
				return nil, fmt.Errorf("跨进程恢复丢失定位信息")
			}
			return schema.AssistantMessage("已核验末尾结论", nil), nil
		}
		return nil, fmt.Errorf("出现非预期工具结果")
	}
	req := Request{Query: "核验原文", UserID: "u", SessionID: "selection-recovery"}
	if phase == "restore" {
		raw, err := os.ReadFile(filepath.Join(dir, "interrupt.json"))
		var interrupt Event
		if err != nil || json.Unmarshal(raw, &interrupt) != nil {
			t.Fatal("缺少中断标识", err)
		}
		req.CheckpointID = interrupt.CheckpointID
		req.ResumeData = map[string]any{interrupt.InterruptID: "采用"}
	}
	var interrupted, done bool
	for _, ev := range collectPEEvents(t, ctx, e, req, m) {
		if ev.Type == EventError || ev.TaskStatus == TaskError {
			t.Fatalf("定位证据恢复失败: %+v", ev)
		}
		if ev.Type == EventInterrupt {
			raw, _ := json.Marshal(ev)
			if err := os.WriteFile(filepath.Join(dir, "interrupt.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			interrupted = true
		}
		done = done || ev.Type == EventDone
	}
	if phase == "save" {
		if !interrupted || done || probe.calls.Load() != 1 {
			t.Fatal("定位后未正确暂停")
		}
		raw, _ := json.Marshal(repo.values)
		if err := os.WriteFile(filepath.Join(dir, "artifacts.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	} else if !restored || !synthesized || !done || interrupted || probe.calls.Load() != 0 {
		t.Fatalf("恢复未沿用证据或重复执行原工具: restored=%t synthesis=%t done=%t source=%d", restored, synthesized, done, probe.calls.Load())
	}
}
