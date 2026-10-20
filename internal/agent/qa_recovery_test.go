package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"solvify-agent/pkg/config"
)

func TestReplanMissingDependenciesPreservesCompletedFacts(t *testing.T) {
	r := &peRuntime{engine: deepTestEngine()}
	s := &peState{Request: Request{Query: "研究"}, Results: map[string]TaskResult{}}
	if err := r.installPlan(s, &pePlan{ExecutionPlan: ExecutionPlan{Strategy: "sequential", Tasks: []TaskSpec{{ID: "a", Agent: "knowledge_research", Title: "取得原文"}, {ID: "s", Agent: "synthesis_review", Title: "汇总", DependsOn: []string{"a"}}}}}); err != nil {
		t.Fatal(err)
	}
	mergeTaskResult(&s.Plan, s.Results, TaskResult{TaskID: "a", Agent: "knowledge_research", Status: TaskSuccess, Summary: "可信结果"})
	before, _ := json.Marshal(s.Results)
	newPlan := &pePlan{ExecutionPlan: ExecutionPlan{Strategy: "sequential", Reason: "新建汇总，省略已完成项", Tasks: []TaskSpec{{ID: "next", Agent: "synthesis_review", Title: "新汇总"}}}}
	if err := r.installPlan(s, newPlan); err != nil {
		t.Fatal(err)
	}
	if len(taskByID(s.Plan, "next").DependsOn) != 1 || taskByID(s.Plan, "next").DependsOn[0] != "a" {
		t.Fatalf("没有补齐汇总依赖：%+v", s.Plan)
	}
	after, _ := json.Marshal(s.Results)
	if string(before) != string(after) || taskByID(s.Plan, "a").Status != TaskSuccess {
		t.Fatal("研究结果或完成状态被改写")
	}
	// 未知依赖或可能引入环的图仍被拒绝，不能靠补齐机制通过。
	for _, tasks := range [][]TaskSpec{
		{{ID: "bad", Agent: "synthesis_review", Title: "汇总", DependsOn: []string{"missing"}}},
		{{ID: "research", Agent: "knowledge_research", Title: "错误依赖", DependsOn: []string{"bad"}}, {ID: "bad", Agent: "synthesis_review", Title: "汇总"}},
	} {
		p := &pePlan{ExecutionPlan: ExecutionPlan{Strategy: "sequential", Reason: "非法变更", Tasks: tasks}}
		snapshot, _ := json.Marshal(s)
		if err := r.installPlan(s, p); err == nil {
			t.Fatal("非法依赖图被自动接受")
		}
		current, _ := json.Marshal(s)
		if string(snapshot) != string(current) {
			t.Fatal("无效计划修改了可信状态")
		}
	}
}

func TestArtifactMCPTextKeywordAndContinuation(t *testing.T) {
	text := strings.Repeat("header\\n", 1400) + "\nClose channels\nSend statements: closed -> panic\nTAIL=928"
	raw, _ := json.Marshal(map[string]any{"content": []map[string]string{{"type": "text", "text": text}}})
	reader, ref, _ := artifactReadFixture(t, config.DefaultRuntimeContextPolicy(), string(raw))
	for _, path := range []string{"", ` ,"json_path":"/content/0/text"`} {
		args := fmt.Sprintf(`{"artifact_id":%q,"keyword":"Send statements","limit":15%s}`, ref.ArtifactID, path)
		result, err := reader.InvokableRun(context.Background(), args)
		var page artifactReference
		if err != nil || json.Unmarshal([]byte(result), &page) != nil || page.Selection == nil || page.Selection.Kind != "json_path" || page.Selection.Value != "/content/0/text" || !strings.HasPrefix(page.Content, "Send statements") {
			t.Fatalf("未在解码正文定位：%s %v", result, err)
		}
		result, err = reader.InvokableRun(context.Background(), fmt.Sprintf(`{"artifact_id":%q,"json_path":"/content/0/text","offset":%d}`, ref.ArtifactID, page.NextOffset))
		if err != nil || !strings.Contains(result, "TAIL=928") {
			t.Fatalf("同视图续读丢失末尾：%s %v", result, err)
		}
	}
}

func TestArtifactJSONPointerPreservesLegacyCheckpointView(t *testing.T) {
	const raw = `{"content":[{"type":"text","text":"首行\n末行"}]}`
	legacy, err := artifactJSONPointer(raw, "/content/0/text")
	if err != nil || legacy != `"首行\n末行"` {
		t.Fatalf("旧checkpoint字段视图被改写：%q %v", legacy, err)
	}
	old := artifactReference{Selection: &artifactSelection{Kind: "json_path", Value: "/content/0/text"}, Offset: 0, NextOffset: len([]rune(legacy)), TotalRunes: len([]rune(legacy))}
	got, err := artifactEvidenceView(raw, old)
	if err != nil || got != legacy {
		t.Fatalf("旧读取位置不再有效：%q %v", got, err)
	}
}

func TestRuntimeCheckpointInstructionMigrationKeepsBudget(t *testing.T) {
	req := Request{UserID: "u", SessionID: "s", RunID: "run", TaskID: "t", AgentName: "web_research", ModelName: "gpt-4o", MaxContextLength: 64000}
	previous := "必要指令\n"
	instruction := previous + webResearchReadingInstruction + "\n" + citationFollowupInstruction
	// 新模板中citation规则之前有换行；移除两个已知新增段落恰好得到旧模板。
	previous = strings.ReplaceAll(strings.ReplaceAll(instruction, "\n"+citationFollowupInstruction, ""), webResearchReadingInstruction+"\n", "")
	m := deepTestEngine().runtimeHandler(req, instruction, nil).(*runtimeContextMiddleware)
	if m.matchesSnapshotInstruction(context.Background(), contentHash(previous)) {
		t.Fatal("新执行接受了旧模板指纹")
	}
	runContextTest(t, nil, func(ctx context.Context) error {
		snapshot := runtimeSnapshot{Version: 1, UserID: req.UserID, SessionID: req.SessionID, RunID: req.RunID, AgentID: req.AgentName, TaskID: req.TaskID, InstructionHash: contentHash(previous), Summary: "保留的摘要", SummaryCalls: 2, Revision: 3}
		raw, _ := json.Marshal(snapshot)
		private := &taskContextSnapshot{Raw: string(raw), Restoring: true}
		ctx = context.WithValue(ctx, taskContextSnapshotKey{}, private)
		if m.matchesSnapshotInstruction(ctx, contentHash("任意旧指令")) {
			return fmt.Errorf("接受未知模板")
		}
		_, _, err := m.BeforeModelRewriteState(ctx, &adk.ChatModelAgentState{Messages: []*schema.Message{schema.UserMessage("继续核验")}}, nil)
		if err != nil {
			return err
		}
		var restored runtimeSnapshot
		if json.Unmarshal([]byte(private.Raw), &restored) != nil || restored.SummaryCalls != 2 || restored.Revision != 3 || restored.Summary != "保留的摘要" || restored.InstructionHash != contentHash(instruction) {
			return fmt.Errorf("迁移重置了摘要预算或丢失状态")
		}
		return nil
	})
}

func TestRunnerSeparatesToolPrefaceFromFinalMarkdown(t *testing.T) {
	ctx := context.Background()
	var answer strings.Builder
	ch := make(chan Event, 30)
	e := &Engine{}
	first := schema.StreamReaderFromArray([]*schema.Message{schema.AssistantMessage("正在核对。", nil), schema.AssistantMessage("", []schema.ToolCall{{ID: "call", Function: schema.FunctionCall{Name: "lookup"}}})})
	if err := e.consumeMessageStream(ctx, &adk.TypedMessageVariant[*schema.Message]{Role: schema.Assistant, MessageStream: first}, nil, &answer, ch); err != nil {
		t.Fatal(err)
	}
	second := schema.StreamReaderFromArray([]*schema.Message{schema.AssistantMessage("#", nil), schema.AssistantMessage("# 结果\n\n129.95元", nil)})
	if err := e.consumeMessageStream(ctx, &adk.TypedMessageVariant[*schema.Message]{Role: schema.Assistant, MessageStream: second}, nil, &answer, ch); err != nil {
		t.Fatal(err)
	}
	close(ch)
	var emitted strings.Builder
	for event := range ch {
		if event.Type == EventAnswer {
			emitted.WriteString(event.Content)
		}
	}
	if answer.String() != "正在核对。\n\n## 结果\n\n129.95元" || answer.String() != emitted.String() {
		t.Fatalf("流式与最终正文边界不一致：%q %q", answer.String(), emitted.String())
	}
}
