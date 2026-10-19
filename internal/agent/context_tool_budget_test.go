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

	"github.com/cloudwego/eino/adk"
	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/internal/llm"
	"solvify-agent/internal/repository"
	"solvify-agent/pkg/config"
	"solvify-agent/pkg/tokenutil"
)

func saveBudgetArtifact(t *testing.T, e *Engine, req Request, content, call string) *schema.Message {
	t.Helper()
	endpoint := e.artifactMiddleware(req)(func(context.Context, *compose.ToolInput) (*compose.ToolOutput, error) {
		return &compose.ToolOutput{Result: content}, nil
	})
	out, err := endpoint(context.Background(), &compose.ToolInput{Name: "search", CallID: call})
	if err != nil {
		t.Fatal(err)
	}
	return schema.ToolMessage(out.Result, call)
}

func TestToolContextUsesAvailableWindowAndPreservesOriginal(t *testing.T) {
	e := deepTestEngine()
	repo := &memoryArtifactRepo{}
	e.WithArtifactRepo(repo)
	req := Request{UserID: "u", SessionID: "s", RunID: "r", AgentName: "knowledge_research", TaskID: "k", ModelName: "gpt-4o", MaxContextLength: 1048576, OutputLimit: 2048, Query: "查找证据"}
	body := strings.Repeat("证据含中文📄和转义\"\\\n", 360) + guardProbeTail
	stored := saveBudgetArtifact(t, e, req, body, "call")
	state := &adk.ChatModelAgentState{Messages: append([]*schema.Message{schema.SystemMessage("依据证据回答"), schema.UserMessage(req.Query)}, testToolGroup("call", stored.Content)...)}
	large, err := e.expandToolContext(context.Background(), req, state)
	if err != nil {
		t.Fatal(err)
	}
	var full artifactReference
	if json.Unmarshal([]byte(large.Messages[3].Content), &full) != nil || full.Content != body || !full.Complete {
		t.Fatal("大窗口未直接显示完整原文")
	}
	if stored.Content != state.Messages[3].Content || strings.Contains(stored.Content, guardProbeTail) {
		t.Fatal("原引用被就地改写")
	}
	req.MaxContextLength = 8192
	small, err := e.expandToolContext(context.Background(), req, state)
	if err != nil {
		t.Fatal(err)
	}
	var partial artifactReference
	if json.Unmarshal([]byte(small.Messages[3].Content), &partial) != nil || partial.Complete || partial.ArtifactID != full.ArtifactID || partial.Content == "" || !strings.HasPrefix(body, partial.Content) || partial.NextOffset != len([]rune(partial.Content)) {
		t.Fatalf("小窗口未保留可靠分页引用: %+v", partial)
	}
	b, _ := requestContextBudget(req)
	if n := llm.MessagesTokens(small.Messages, req.ModelName); n > b.Target {
		t.Fatalf("投影超过实际总预算: %d > %d", n, b.Target)
	}
	a, err := repo.FindScoped(context.Background(), artifactScope(req), full.ArtifactID)
	if err != nil || a.Content != body {
		t.Fatal("投影损坏已保存原文", err)
	}
	for _, change := range []func(*Request){func(r *Request) { r.UserID = "other" }, func(r *Request) { r.RunID = "other" }, func(r *Request) { r.TaskID = "other" }} {
		other := req
		change(&other)
		if _, err := e.expandToolContext(context.Background(), other, state); !errors.Is(err, repository.ErrArtifactUnavailable) {
			t.Fatal("动态展开绕过范围检查", err)
		}
	}
}

func TestToolContextMultipleResultsShareBudgetAndKeepJSON(t *testing.T) {
	e := deepTestEngine()
	e.WithArtifactRepo(&memoryArtifactRepo{})
	req := Request{UserID: "u", SessionID: "s", RunID: "r", ModelName: "gpt-4o", MaxContextLength: 10000, OutputLimit: 1024}
	var sources []map[string]any
	for i := 0; i < 16; i++ {
		sources = append(sources, map[string]any{"id": fmt.Sprintf("chunk_%d", i), "document_id": "doc", "score": float64(16 - i), "content": strings.Repeat("完整分块内容", 30)})
	}
	body, _ := json.Marshal(map[string]any{"sources": sources, "content": "使用真实chunk_id引用"})
	messages := []*schema.Message{schema.SystemMessage("约束必须保留"), schema.UserMessage("比较证据")}
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("call%d", i)
		m := saveBudgetArtifact(t, e, req, string(body), id)
		messages = append(messages, testToolGroup(id, m.Content)...)
	}
	state := &adk.ChatModelAgentState{Messages: messages, ToolInfos: []*schema.ToolInfo{{Name: "search", Desc: "检索"}}}
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := e.expandToolContext(context.Background(), req, state)
			if err != nil {
				t.Error(err)
				return
			}
			b, _ := requestContextBudget(req)
			if n := llm.MessagesTokens(out.Messages, req.ModelName) + llm.ToolsTokens(out.ToolInfos, req.ModelName); n > b.Target {
				t.Errorf("共享额度超限: %d > %d", n, b.Target)
			}
			for j := 3; j < len(out.Messages); j += 2 {
				var ref artifactReference
				if json.Unmarshal([]byte(out.Messages[j].Content), &ref) != nil || ref.Complete || ref.ArtifactID == "" || out.Messages[j].ToolCallID != messages[j].ToolCallID || !json.Valid([]byte(ref.Content)) {
					t.Errorf("投影破坏引用、配对或JSON: %s", out.Messages[j].Content)
					return
				}
				var projected struct {
					Sources []map[string]any `json:"sources"`
				}
				_ = json.Unmarshal([]byte(ref.Content), &projected)
				if len(projected.Sources) == 0 || len(projected.Sources) >= len(sources) || projected.Sources[0]["id"] != "chunk_0" || projected.Sources[0]["content"] != sources[0]["content"] {
					t.Error("未按相关度保留完整分块")
				}
			}
		}()
	}
	wg.Wait()
}

func TestArtifactSelectorsAndSelectedEvidenceReachSynthesis(t *testing.T) {
	p := config.DefaultRuntimeContextPolicy()
	p.ArtifactThreshold = 1
	p.ReadTokens = 8000
	body := "# 前言\n" + strings.Repeat("无关前言。", 900) + "\n# 结论\n尾部唯一证据📄\n```go\n# 伪章节\n```\n## 子节\n核验通过\n# 附录\n其他内容"
	reader, ref, _ := artifactReadFixture(t, p, body)
	for _, selector := range []map[string]any{{"keyword": "尾部唯一证据"}, {"section": "结论"}} {
		selector["artifact_id"] = ref.ArtifactID
		args, _ := json.Marshal(selector)
		raw, err := reader.InvokableRun(context.Background(), string(args))
		if err != nil {
			t.Fatal(err)
		}
		var page artifactReference
		if json.Unmarshal([]byte(raw), &page) != nil || !strings.Contains(page.Content, "尾部唯一证据") || page.Selection == nil {
			t.Fatal("定位未返回目标证据")
		}
		if selector["section"] != "" && selector["section"] != nil && (strings.Contains(page.Content, "其他内容") || !strings.Contains(page.Content, "核验通过")) {
			t.Fatal("章节边界错误")
		}
		e := deepTestEngine()
		e.WithArtifactRepo(reader.repo)
		refs := appendArtifactReference([]artifactReference{ref}, page)
		if len(refs) != 2 || refs[1].Content != "" {
			t.Fatal("读取范围未记录或checkpoint重复存储正文")
		}
		req := Request{UserID: "u", SessionID: "s", RunID: "run", AgentName: "synthesis_review", TaskID: "sum", ModelName: "gpt-4o", MaxContextLength: 64000, OutputLimit: 2048}
		evidence, err := e.taskArtifactEvidence(context.Background(), req, TaskSpec{DependsOn: []string{"t"}}, map[string]TaskResult{"t": {TaskID: "t", Agent: "knowledge_research", Status: TaskSuccess, Artifacts: refs}})
		if err != nil || !strings.Contains(evidence, "尾部唯一证据") || strings.Contains(evidence, "无关前言") || !strings.Contains(evidence, `"Complete":false`) {
			t.Fatalf("汇总丢失实际读取证据: %s, %v", evidence, err)
		}
		// 下一轮不能把精确选择的分页响应又替换成原文首页。
		state := &adk.ChatModelAgentState{Messages: []*schema.Message{schema.ToolMessage(raw, "read")}}
		scoped := req
		scoped.TaskID = "t"
		scoped.AgentName = "knowledge_research"
		expanded, err := e.expandToolContext(context.Background(), scoped, state)
		if err != nil || expanded.Messages[0].Content != raw {
			t.Fatal("选择器响应被重复展开", err)
		}
	}
}

func TestArtifactJSONAndKnowledgeChunkLocators(t *testing.T) {
	p := config.DefaultRuntimeContextPolicy()
	p.ArtifactThreshold = 1
	p.ReadTokens = 4000
	body := `{"a/b":{"~key":[9007199254740993]},"sources":[{"id":"chunk_1","content":"第一个"},{"id":"chunk_2","document_id":"doc","content":"尾部正文"}]}`
	reader, ref, _ := artifactReadFixture(t, p, body)
	for _, tc := range []struct {
		args map[string]any
		want string
	}{{map[string]any{"json_path": "/a~1b/~0key/0"}, "9007199254740993"}, {map[string]any{"chunk_id": "chunk_2"}, `{"id":"chunk_2","document_id":"doc","content":"尾部正文"}`}} {
		tc.args["artifact_id"] = ref.ArtifactID
		args, _ := json.Marshal(tc.args)
		raw, err := reader.InvokableRun(context.Background(), string(args))
		var page artifactReference
		if err != nil || json.Unmarshal([]byte(raw), &page) != nil || page.Content != tc.want || !page.Complete || page.Selection == nil {
			t.Fatalf("JSON定位错误: %s, %v", raw, err)
		}
		if _, err := artifactEvidenceView(body, page); err != nil {
			t.Fatal("结构化证据不能恢复", err)
		}
	}
	for _, args := range []string{`"json_path":"/a~2b"`, `"json_path":"/missing"`, `"json_path":"/sources/01"`, `"chunk_id":"missing"`, `"keyword":"不存在"`, `"section":"不存在"`, `"keyword":"x","json_path":"/sources"`} {
		raw, err := reader.InvokableRun(context.Background(), fmt.Sprintf(`{"artifact_id":%q,%s}`, ref.ArtifactID, args))
		if _, ok := artifactArgumentFailure(artifactReadToolName, raw); err != nil || !ok {
			t.Fatalf("无效定位器未明确反馈: %s, %v", raw, err)
		}
	}
}

func TestArtifactReadingBudgetScalesAndSnapshotReferencesStaySmall(t *testing.T) {
	e := deepTestEngine()
	e.WithArtifactRepo(&memoryArtifactRepo{})
	req := Request{UserID: "u", SessionID: "s", RunID: "r", ModelName: "gpt-4o", MaxContextLength: 8192, OutputLimit: 1024}
	small := e.artifactReadingPolicy(req)
	req.MaxContextLength = 1048576
	large := e.artifactReadingPolicy(req)
	if large.ReadTokens <= small.ReadTokens || small.ReadTokens <= e.runtimePolicy().ReadTokens {
		t.Fatalf("读取仍固定: small=%d large=%d", small.ReadTokens, large.ReadTokens)
	}
	req.InputLimit = 4000
	if limited := e.artifactReadingPolicy(req); limited.ReadTokens >= small.ReadTokens {
		t.Fatal("读取未考虑独立输入上限")
	}
	req.InputLimit = 0
	body := strings.Repeat("证据正文。", 1200)
	m := saveBudgetArtifact(t, e, req, body, "call")
	private := &taskContextSnapshot{}
	ctx := context.WithValue(context.Background(), taskContextSnapshotKey{}, private)
	mw := &runtimeContextMiddleware{engine: e, req: req, instruction: "保持来源"}
	_, _, err := mw.BeforeModelRewriteState(ctx, &adk.ChatModelAgentState{Messages: testToolGroup("call", m.Content)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot runtimeSnapshot
	if json.Unmarshal([]byte(private.Raw), &snapshot) != nil || len(snapshot.References) != 1 || snapshot.References[0].Content != "" || snapshot.References[0].Preview != "" || len(private.Raw) > 3000 {
		t.Fatalf("摘要重复保存正文: %d bytes", len(private.Raw))
	}
}

func TestEngineStoredToolResultNeedsNoReadWhenWindowFits(t *testing.T) {
	e := guardTestEngine()
	e.WithArtifactRepo(&memoryArtifactRepo{})
	probe := guardProbeTool{calls: new(atomic.Int32)}
	e.RegisterInternal("guard_probe", 1, false, DomainKnowledge, func(context.Context, string, []string) einoTool.BaseTool { return probe })
	var modelCalls atomic.Int32
	m := &deepTestModel{respond: func(_ context.Context, messages []*schema.Message) (*schema.Message, error) {
		modelCalls.Add(1)
		last := messages[len(messages)-1]
		if last.Role != schema.Tool {
			return peCall("guard_probe", map[string]any{}), nil
		}
		var ref artifactReference
		if json.Unmarshal([]byte(last.Content), &ref) != nil || !ref.Complete || !strings.HasSuffix(ref.Content, guardProbeTail) {
			return nil, fmt.Errorf("模型未收到完整工具结果")
		}
		return schema.AssistantMessage("依据完整证据完成回答", nil), nil
	}}
	events := collectGuardEvents(t, mustExecute(t, guardTestContext(probe), e, Request{Query: "查询", UserID: "u", SessionID: "full-context", ModelName: "gpt-4o", MaxContextLength: 64000}, m))
	done := false
	for _, ev := range events {
		if ev.Type == EventError {
			t.Fatal(ev.Error)
		}
		done = done || ev.Type == EventDone
	}
	if !done || modelCalls.Load() != 2 || probe.calls.Load() != 1 {
		t.Fatalf("仍需额外分页: done=%t model=%d tool=%d", done, modelCalls.Load(), probe.calls.Load())
	}
}

func TestUnstoredToolOversizeIsRejectedWithoutLosingContent(t *testing.T) {
	body := strings.Repeat("没有原文引用的工具内容", 2000)
	messages := []*schema.Message{schema.UserMessage("查找"), schema.ToolMessage(body, "call")}
	g := newBudgetGuard(Request{ModelName: "gpt-4o", MaxContextLength: 8192, OutputLimit: 1024})
	_, out, err := g.BeforeModelRewriteState(context.Background(), &adk.ChatModelAgentState{Messages: messages}, nil)
	if !errors.Is(err, llm.ErrContextBudget) || out.Messages[1].Content != body {
		t.Fatal("未保存原文的结果被静默截断", err)
	}
	if tokenutil.CountTokens(body, "gpt-4o") <= 8192 {
		t.Fatal("样本没有超过窗口")
	}
}

func TestEngineArtifactStoreFailureStopsWithoutRepeatingTool(t *testing.T) {
	e := guardTestEngine()
	e.WithArtifactRepo(&memoryArtifactRepo{fail: true})
	probe := guardProbeTool{calls: new(atomic.Int32)}
	var calls atomic.Int32
	m := &deepTestModel{respond: func(context.Context, []*schema.Message) (*schema.Message, error) {
		calls.Add(1)
		return peCall("guard_probe", map[string]any{}), nil
	}}
	events := collectGuardEvents(t, mustExecute(t, guardTestContext(probe), e, Request{Query: "读取证据", UserID: "u", SessionID: "store-failed", ModelName: "gpt-4o", MaxContextLength: 64000}, m))
	failed := false
	for _, ev := range events {
		if ev.Type == EventDone {
			t.Fatal("原文保存失败仍报成功")
		}
		failed = failed || ev.Type == EventError
	}
	if !failed || calls.Load() != 1 || probe.calls.Load() != 1 {
		t.Fatalf("原文保存失败被吞掉或工具被重复执行: failed=%t model=%d tool=%d", failed, calls.Load(), probe.calls.Load())
	}
}
