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

	"solvify-agent/internal/model/entity"
)

const citationTestPlan = `{"strategy":"sequential","tasks":[{"id":"a","agent":"knowledge_research","title":"研究select"},{"id":"s","agent":"synthesis_review","title":"审查","depends_on":["a"]}]}`

// 模型只能从程序清单选择ID；研究工具仍经过真实Eino中间件执行。
func citationExecutionModel(t *testing.T, profile Profile, clarify bool) *deepTestModel {
	t.Helper()
	base := peModel(citationTestPlan, new(atomic.Int32)).respond
	return &deepTestModel{respond: func(ctx context.Context, msgs []*schema.Message) (*schema.Message, error) {
		if profile == ProfileQuick || strings.Contains(msgs[0].Content, "研究知识库") {
			var manifest []citationSource
			lastTool := ""
			for _, msg := range msgs {
				if msg.Role != schema.Tool {
					continue
				}
				lastTool = msg.ToolCallID
				var result struct {
					CitationSources []citationSource `json:"citation_sources"`
				}
				if json.Unmarshal([]byte(msg.Content), &result) == nil && len(result.CitationSources) > 0 {
					manifest = result.CitationSources
				}
			}
			if len(manifest) == 0 {
				return peCall("knowledge_search", map[string]string{"query": "select"}), nil
			}
			if len(manifest) != 1 || manifest[0].Title != "Golang并发之select详解" {
				return nil, fmt.Errorf("模型收到错误来源清单")
			}
			if clarify && lastTool != "call-"+clarifyToolName {
				return peCall(clarifyToolName, map[string]string{"question": "是否继续"}), nil
			}
			return schema.AssistantMessage(`结论<cite id="`+manifest[0].ID+`" />`, nil), nil
		}
		answer, err := base(ctx, msgs)
		if err != nil || !strings.Contains(msgs[0].Content, "你是 Replanner") || len(answer.ToolCalls) == 0 || answer.ToolCalls[0].Function.Name != "respond" {
			return answer, err
		}
		content := msgs[len(msgs)-1].Content
		_, raw, ok := strings.Cut(content, "执行状态：")
		var state struct{ CitationSources []citationSource }
		if !ok || json.Unmarshal([]byte(raw), &state) != nil || len(state.CitationSources) != 1 {
			return nil, fmt.Errorf("最终回答未取得已登记来源")
		}
		return peCall("respond", map[string]string{"response": `结论<cite id="` + state.CitationSources[0].ID + `" />`}), nil
	}}
}

func citationEngineContext(t *testing.T, e *Engine, clarify bool, calls *atomic.Int32) context.Context {
	t.Helper()
	lookup := peResultTestTool{name: "knowledge_search", result: citationKBResult, calls: calls}
	e.RegisterInternal(lookup.name, 1, false, DomainKnowledge, func(context.Context, string, []string) einoTool.BaseTool { return lookup })
	tools := []einoTool.BaseTool{lookup}
	if clarify {
		e.RegisterInternal(clarifyToolName, 2, false, DomainKnowledge, func(context.Context, string, []string) einoTool.BaseTool { return guardClarifyTool{} })
		tools = append(tools, guardClarifyTool{})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	return withPrebuiltTools(ctx, prebuiltToolsBundle{Buckets: map[ToolDomain][]einoTool.BaseTool{DomainKnowledge: tools}})
}

func assertCitationExecution(t *testing.T, events []Event) string {
	t.Helper()
	var answer strings.Builder
	var done string
	for _, ev := range events {
		if ev.Type == EventError {
			t.Fatalf("引用执行失败: %+v", ev)
		}
		if ev.Type == EventAnswer {
			answer.WriteString(ev.Content)
		}
		if ev.Type == EventDone {
			done = ev.Content
		}
	}
	if !strings.Contains(done, `<kb doc="Golang并发之select详解" chunk_id="chunk-select" />`) || answer.String() != done || strings.Contains(done, "<cite") {
		t.Fatalf("流式与最终正文未统一校验: answer=%q done=%q", answer.String(), done)
	}
	return done
}

func TestCitationEngineQuickAndPlanExecuteIsolation(t *testing.T) {
	for _, profile := range []Profile{ProfileQuick, ProfileDeep} {
		t.Run(string(profile), func(t *testing.T) {
			e := deepTestEngine()
			var calls atomic.Int32
			ctx := citationEngineContext(t, e, false, &calls)
			req := Request{Profile: profile, UserID: "u", SessionID: "citation-engine", Query: "介绍select"}
			answer := assertCitationExecution(t, collectPEEvents(t, ctx, e, req, citationExecutionModel(t, profile, false)))
			if calls.Load() != 1 {
				t.Fatalf("工具被重复调用%d次", calls.Load())
			}
			// 新用户和同用户新运行均不能通过历史正文继承上一轮的引用权限。
			for _, user := range []string{"u", "other"} {
				registry := &citationRegistry{}
				id := registry.toolSources("knowledge_search", citationKBResult)[0].ID
				base := peModel(`{"strategy":"direct","tasks":[{"id":"s","agent":"synthesis_review","title":"回答"}]}`, new(atomic.Int32)).respond
				m := &deepTestModel{respond: func(ctx context.Context, msgs []*schema.Message) (*schema.Message, error) {
					text := `历史来源<cite id="` + id + `" />`
					if profile == ProfileQuick {
						return schema.AssistantMessage(text, nil), nil
					}
					msg, err := base(ctx, msgs)
					if err == nil && len(msg.ToolCalls) > 0 && msg.ToolCalls[0].Function.Name == "respond" {
						return peCall("respond", map[string]string{"response": text}), nil
					}
					return msg, err
				}}
				req.UserID = user
				req.History = []entity.ChatMessage{{Role: "assistant", Content: answer}}
				var done string
				for _, ev := range collectPEEvents(t, ctx, e, req, m) {
					if ev.Type == EventError {
						t.Fatalf("新运行失败: %+v", ev)
					}
					if ev.Type == EventDone {
						done = ev.Content
					}
				}
				if done != "历史来源（来源未核验）" || calls.Load() != 1 {
					t.Fatalf("历史来源跨运行泄漏: user=%s answer=%q calls=%d", user, done, calls.Load())
				}
			}
		})
	}
}

func TestCitationCheckpointRecoveryAcrossProcesses(t *testing.T) {
	phase, dir := os.Getenv("SOLVIFY_CITATION_PHASE"), os.Getenv("SOLVIFY_CITATION_DIR")
	if phase == "" {
		dir = t.TempDir()
		for _, phase := range []string{"save", "restore"} {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCitationCheckpointRecoveryAcrossProcesses$", "-test.timeout=50s")
			cmd.Env = append(os.Environ(), "SOLVIFY_CITATION_PHASE="+phase, "SOLVIFY_CITATION_DIR="+dir)
			out, err := cmd.CombinedOutput()
			cancel()
			if err != nil {
				t.Fatalf("引用跨进程%s失败: %v\n%s", phase, err, out)
			}
		}
		return
	}
	for _, profile := range []Profile{ProfileQuick, ProfileDeep} {
		storeDir := filepath.Join(dir, string(profile))
		if err := os.MkdirAll(storeDir, 0700); err != nil {
			t.Fatal(err)
		}
		e := deepTestEngine()
		e.cfg.DeepMaxSubagentIterations = 6
		e.WithCheckpointRepo(artifactArgumentCheckpointRepo{store: probeFileStore{storeDir}})
		var calls atomic.Int32
		ctx := citationEngineContext(t, e, true, &calls)
		req := Request{Profile: profile, UserID: "u", SessionID: "citation-recovery", Query: "介绍select", ModelName: "gpt-4o", MaxContextLength: 64000, OutputLimit: 4096}
		if phase == "restore" {
			raw, err := os.ReadFile(filepath.Join(storeDir, "interrupt.json"))
			var interrupt Event
			if err != nil || json.Unmarshal(raw, &interrupt) != nil {
				t.Fatal("未持久化中断", err)
			}
			req.CheckpointID = interrupt.CheckpointID
			req.ResumeData = map[string]any{interrupt.InterruptID: "继续"}
			other := req
			other.UserID = "other"
			if _, err := e.restoreContextRequest(ctx, other); err == nil {
				t.Fatal("跨用户恢复了来源")
			}
			restored, err := e.restoreContextRequest(ctx, req)
			if err != nil || len(restored.citations.snapshot()) != 1 {
				t.Fatal("来源快照未恢复", err)
			}
			fresh := req
			fresh.CheckpointID = ""
			fresh.ResumeData = nil
			unrelated, err := e.restoreContextRequest(ctx, fresh)
			if err != nil || len(unrelated.citations.snapshot()) != 0 {
				t.Fatal("来源泄漏到新运行")
			}
		}
		events := collectPEEvents(t, ctx, e, req, citationExecutionModel(t, profile, true))
		if phase == "restore" {
			assertCitationExecution(t, events)
			if calls.Load() != 0 {
				t.Fatal("恢复重复检索了已完成来源")
			}
			continue
		}
		var interrupt Event
		for _, ev := range events {
			if ev.Type == EventError || ev.Type == EventDone {
				t.Fatalf("未可靠暂停: %+v", ev)
			}
			if ev.Type == EventInterrupt {
				interrupt = ev
			}
		}
		if interrupt.CheckpointID == "" || calls.Load() != 1 {
			t.Fatal("未在检索后暂停")
		}
		raw, _ := json.Marshal(interrupt)
		if err := os.WriteFile(filepath.Join(storeDir, "interrupt.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		checkpoint, _, err := e.buildCheckpointStore(req.SessionID).Get(ctx, interrupt.CheckpointID)
		cp, _, decodeErr := decodeContextCheckpoint(checkpoint)
		if err != nil || decodeErr != nil || len(cp.Citations) != 1 {
			t.Fatal("来源没有写入checkpoint", err, decodeErr)
		}
		cp.Citations[0].Title = "被篡改的标题"
		tampered, _ := json.Marshal(cp)
		if _, _, err := decodeContextCheckpoint(append(append([]byte(nil), checkpointEnvelopePrefix...), tampered...)); err == nil {
			t.Fatal("来源篡改未被哈希检测")
		}
	}
}
