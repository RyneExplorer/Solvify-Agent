package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"solvify-agent/internal/llm"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
	"solvify-agent/pkg/config"
	"solvify-agent/pkg/tokenutil"
)

type memoryArtifactRepo struct {
	mu     sync.Mutex
	values map[string]entity.ContextArtifact
	fail   bool
}

func (r *memoryArtifactRepo) SaveImmutable(ctx context.Context, a *entity.ContextArtifact, _ repository.ArtifactLimits) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail {
		return errors.New("保存失败")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.values == nil {
		r.values = map[string]entity.ContextArtifact{}
	}
	if old, ok := r.values[a.ID]; ok && old.Hash != a.Hash {
		return repository.ErrArtifactUnavailable
	}
	r.values[a.ID] = *a
	return nil
}
func (r *memoryArtifactRepo) FindScoped(ctx context.Context, s repository.ArtifactScope, id string) (*entity.ContextArtifact, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a, ok := r.values[id]
	if !ok || a.UserID != s.UserID || a.SessionID != s.SessionID || a.RunID != s.RunID || a.AgentID != s.AgentID || a.TaskID != s.TaskID {
		return nil, repository.ErrArtifactUnavailable
	}
	return &a, nil
}
func (r *memoryArtifactRepo) DeleteExpired(context.Context, time.Time) (int64, error) { return 0, nil }

func TestContextArtifactSaveReadAndFailure(t *testing.T) {
	e := deepTestEngine()
	repo := &memoryArtifactRepo{}
	e.WithArtifactRepo(repo)
	req := Request{UserID: "u", SessionID: "s", RunID: "run", AgentName: "knowledge_research", TaskID: "t", ModelName: "gpt-4o"}
	content := strings.Repeat("原文证据，包含中文。", 1200)
	endpoint := e.artifactMiddleware(req)(func(context.Context, *compose.ToolInput) (*compose.ToolOutput, error) {
		return &compose.ToolOutput{Result: content}, nil
	})
	out, err := endpoint(context.Background(), &compose.ToolInput{Name: "search", CallID: "call"})
	if err != nil {
		t.Fatal(err)
	}
	var ref artifactReference
	if json.Unmarshal([]byte(out.Result), &ref) != nil || len(ref.ArtifactID) != 64 || len(repo.values) != 1 {
		t.Fatal("未提交原文引用")
	}
	reader := artifactReadTool{repo, artifactScope(req), e.runtimePolicy(), req.ModelName}
	var rebuilt strings.Builder
	for offset := 0; ; {
		args, _ := json.Marshal(map[string]any{"artifact_id": ref.ArtifactID, "offset": offset, "limit": 700})
		raw, err := reader.InvokableRun(context.Background(), string(args))
		if err != nil {
			t.Fatal(err)
		}
		if tokenutil.CountTokens(raw, req.ModelName) > e.runtimePolicy().ReadTokens {
			t.Fatal("读取输出含包装超过额度")
		}
		var part struct {
			Content    string
			NextOffset int `json:"next_offset"`
			Complete   bool
		}
		if err = json.Unmarshal([]byte(raw), &part); err != nil {
			t.Fatal(err)
		}
		rebuilt.WriteString(part.Content)
		if part.Complete {
			break
		}
		if part.NextOffset <= offset {
			t.Fatal("读取无进展")
		}
		offset = part.NextOffset
	}
	if rebuilt.String() != content {
		t.Fatal("分段读取未还原原文")
	}
	for _, change := range []func(*repository.ArtifactScope){func(s *repository.ArtifactScope) { s.UserID = "other" }, func(s *repository.ArtifactScope) { s.SessionID = "other" }, func(s *repository.ArtifactScope) { s.RunID = "other" }, func(s *repository.ArtifactScope) { s.TaskID = "other" }, func(s *repository.ArtifactScope) { s.AgentID = "other" }} {
		other := reader
		change(&other.scope)
		if _, err = other.InvokableRun(context.Background(), fmt.Sprintf(`{"artifact_id":%q}`, ref.ArtifactID)); !errors.Is(err, repository.ErrArtifactUnavailable) {
			t.Fatal("越权读取")
		}
	}
	for _, args := range []string{`{"artifact_id":"../secret"}`, fmt.Sprintf(`{"artifact_id":%q,"offset":-1}`, ref.ArtifactID), fmt.Sprintf(`{"artifact_id":%q,"limit":4001}`, ref.ArtifactID), fmt.Sprintf(`{"artifact_id":%q,"offset":999999}`, ref.ArtifactID)} {
		if _, err = reader.InvokableRun(context.Background(), args); err == nil {
			t.Fatal("非法范围放行")
		}
	}
	repo.fail = true
	out, err = endpoint(context.Background(), &compose.ToolInput{Name: "search", CallID: "failed"})
	if err != nil || out.Result != content {
		t.Fatal("写失败改变原文")
	}
	for _, fact := range []string{`{"success": false,"message":"` + content + `"}`, "用户拒绝审批，操作未执行" + content} {
		protected := e.artifactMiddleware(req)(func(context.Context, *compose.ToolInput) (*compose.ToolOutput, error) {
			return &compose.ToolOutput{Result: fact}, nil
		})
		output, _ := protected(context.Background(), &compose.ToolInput{Name: "search"})
		if output.Result != fact {
			t.Fatal("执行事实被外置")
		}
	}
	repo.fail = false
	a := repo.values[ref.ArtifactID]
	a.Content = "篡改"
	repo.values[a.ID] = a
	if _, err = reader.InvokableRun(context.Background(), fmt.Sprintf(`{"artifact_id":%q}`, ref.ArtifactID)); err == nil {
		t.Fatal("哈希未校验")
	}
}

func testToolGroup(id, content string) []*schema.Message {
	return []*schema.Message{schema.AssistantMessage("", []schema.ToolCall{{ID: id, Type: "function", Function: schema.FunctionCall{Name: "search", Arguments: "{}"}}}), schema.ToolMessage(content, id)}
}
func TestCompletedToolGroupsProtectsProtocolAndFacts(t *testing.T) {
	multi := schema.AssistantMessage("", []schema.ToolCall{{ID: "a"}, {ID: "b"}})
	valid := append([]*schema.Message{multi, schema.ToolMessage("A", "a"), schema.ToolMessage("B", "b")}, testToolGroup("last", "最新")...)
	groups := completedToolGroups(valid)
	if len(groups) != 1 || groups[0].End != 3 {
		t.Fatal("多工具组不完整")
	}
	for _, first := range [][]*schema.Message{{multi, schema.ToolMessage("A", "a")}, {multi, schema.ToolMessage("A", "a"), schema.ToolMessage("重复", "a")}, testToolGroup("bad", `{"success": false}`), testToolGroup("denied", "用户拒绝，未执行")} {
		if got := completedToolGroups(append(first, testToolGroup("last", "最新")...)); len(got) != 0 {
			t.Fatal("活动或失败组被摘要")
		}
	}
}

type contextTestAgent struct{ run func(context.Context) error }

func (*contextTestAgent) Name(context.Context) string        { return "runtime_test" }
func (*contextTestAgent) Description(context.Context) string { return "运行上下文工程测试" }
func (a *contextTestAgent) Run(ctx context.Context, _ *adk.AgentInput, _ ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	iter, g := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	go func() {
		defer g.Close()
		if err := a.run(ctx); err != nil {
			g.Send(&adk.AgentEvent{Err: err})
		}
	}()
	return iter
}
func runContextTest(t *testing.T, values map[string]any, run func(context.Context) error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runContextTestInContext(t, ctx, values, run)
}

func runContextTestInContext(t *testing.T, ctx context.Context, values map[string]any, run func(context.Context) error) {
	t.Helper()
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: &contextTestAgent{run: run}})
	iter := runner.Run(ctx, []*schema.Message{schema.UserMessage("问题")}, adk.WithSessionValues(values))
	for {
		ev, ok := iter.Next()
		if !ok {
			break
		}
		if ev.Err != nil {
			t.Fatal(ev.Err)
		}
	}
}

func TestRuntimeSummaryBoundedImmutableAndFailure(t *testing.T) {
	for _, scenario := range []string{"success", "failure", "no_progress", "quick", "stale", "timeout"} {
		t.Run(scenario, func(t *testing.T) {
			e := deepTestEngine()
			e.cfg.RuntimeContext = config.DefaultRuntimeContextPolicy()
			e.cfg.RuntimeContext.SummaryMaxCalls = 1
			if scenario == "timeout" {
				e.cfg.RuntimeContext.SummaryTimeoutSeconds = 1
			}
			req := Request{Profile: ProfileDeep, UserID: "u", SessionID: "s", RunID: "run", AgentName: "knowledge_research", TaskID: "t", ModelID: "runtime-test", ModelName: "gpt-4o", MaxContextLength: 8192}
			if scenario == "quick" {
				req.Profile = ProfileQuick
			}
			messages := append([]*schema.Message{schema.UserMessage("保留当前问题")}, testToolGroup("old", strings.Repeat("证据 ", 1800))...)
			messages = append(messages, testToolGroup("latest", strings.Repeat("最新证据 ", 600))...)
			original, _ := json.Marshal(messages)
			calls := 0
			cm := &deepTestModel{respond: func(summaryCtx context.Context, input []*schema.Message) (*schema.Message, error) {
				calls++
				if scenario == "timeout" {
					<-summaryCtx.Done()
					return schema.AssistantMessage("超时后返回的摘要", nil), nil
				}
				if llm.MessagesTokens(input, req.ModelName) > e.runtimePolicy().SummaryInput {
					return nil, errors.New("摘要输入超限")
				}
				if scenario == "failure" {
					return nil, errors.New("摘要失败")
				}
				if scenario == "no_progress" {
					return schema.AssistantMessage(strings.Repeat("无进展 ", 2000), nil), nil
				}
				if scenario == "stale" {
					messages[2] = schema.ToolMessage("源版本改变", "old")
				}
				return schema.AssistantMessage("目标：查证。发现：已完成旧查询。证据：旧工具。待办：综合最新发现。", nil), nil
			}}
			m := e.runtimeHandler(req, "必要指令", cm).(*runtimeContextMiddleware)
			runContextTest(t, nil, func(ctx context.Context) error {
				state := &adk.ChatModelAgentState{Messages: messages}
				_, next, err := m.BeforeModelRewriteState(ctx, state, nil)
				if err != nil {
					return err
				}
				if scenario == "success" || scenario == "quick" {
					if next == state || len(next.Messages) != 4 || next.Messages[0].Role != schema.User || next.Messages[1].Content != "保留当前问题" {
						return errors.New("摘要未保持问题/最新组")
					}
					if raw, _ := json.Marshal(messages); string(raw) != string(original) {
						return errors.New("原消息被修改")
					}
				} else if next != state {
					return errors.New("失败不应替换状态")
				}
				_, _, err = m.BeforeModelRewriteState(ctx, state, nil)
				if err != nil {
					return err
				}
				expected := 1
				if calls != expected {
					return fmt.Errorf("摘要次数=%d，期望%d", calls, expected)
				}
				raw, _ := adk.GetSessionValue(ctx, m.key())
				var snapshot runtimeSnapshot
				if json.Unmarshal([]byte(raw.(string)), &snapshot) != nil {
					return errors.New("快照未发布")
				}
				if (scenario == "success" || scenario == "quick") && (snapshot.Revision != 1 || len(snapshot.CoveredGroups) != 1) {
					return errors.New("覆盖范围未记录")
				}
				return nil
			})
		})
	}
}

func TestRuntimeContextsIsolateParallelTasks(t *testing.T) {
	e := deepTestEngine()
	runContextTest(t, nil, func(ctx context.Context) error {
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for _, id := range []string{"a", "b"} {
			wg.Add(1)
			go func(id string) {
				defer wg.Done()
				req := Request{Profile: ProfileQuick, RunID: "run", TaskID: id, AgentName: "research", ModelName: "gpt-4o", ModelID: "isolation", MaxContextLength: 64000}
				m := e.runtimeHandler(req, "指令", nil).(*runtimeContextMiddleware)
				_, _, err := m.BeforeModelRewriteState(ctx, &adk.ChatModelAgentState{Messages: []*schema.Message{schema.UserMessage(id)}}, nil)
				if err == nil {
					v, ok := adk.GetSessionValue(ctx, m.key())
					var s runtimeSnapshot
					if !ok || json.Unmarshal([]byte(v.(string)), &s) != nil || s.TaskID != id {
						err = errors.New("任务快照串线")
					}
				}
				errs <- err
			}(id)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				return err
			}
		}
		return nil
	})
}

func TestTaskArtifactEvidenceBoundedAndSourceMissing(t *testing.T) {
	e := deepTestEngine()
	repo := &memoryArtifactRepo{}
	e.WithArtifactRepo(repo)
	req := Request{UserID: "u", SessionID: "s", RunID: "r", AgentName: "knowledge_research", TaskID: "research", ModelName: "gpt-4o"}
	content := strings.Repeat("完整证据原文。", 1500)
	endpoint := e.artifactMiddleware(req)(func(context.Context, *compose.ToolInput) (*compose.ToolOutput, error) {
		return &compose.ToolOutput{Result: content}, nil
	})
	out, err := endpoint(context.Background(), &compose.ToolInput{Name: "search", CallID: "call"})
	if err != nil {
		t.Fatal(err)
	}
	var ref artifactReference
	if err = json.Unmarshal([]byte(out.Result), &ref); err != nil {
		t.Fatal(err)
	}
	results := map[string]TaskResult{"research": {TaskID: "research", Agent: "knowledge_research", Status: TaskSuccess, Artifacts: []artifactReference{ref}}}
	task := TaskSpec{ID: "synthesis", Agent: "synthesis_review", DependsOn: []string{"research"}}
	req.TaskID = "synthesis"
	req.AgentName = "synthesis_review"
	evidence, err := e.taskArtifactEvidence(context.Background(), req, task, results)
	if err != nil || !strings.Contains(evidence, ref.ArtifactID) || !strings.Contains(evidence, `"Complete":false`) {
		t.Fatal("汇总证据未读取或未标片段", err)
	}
	delete(repo.values, ref.ArtifactID)
	if _, err = e.taskArtifactEvidence(context.Background(), req, task, results); !errors.Is(err, repository.ErrArtifactUnavailable) {
		t.Fatal("源丢失被伪装为空")
	}
}
