package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/internal/rag"
	"solvify-agent/internal/tool"
	"solvify-agent/pkg/config"
)

func collectPEBehaviorEvents(t *testing.T, ctx context.Context, e *Engine, req Request, m *deepTestModel) []Event {
	t.Helper()
	if _, ok := prebuiltToolsFromContext(ctx); !ok {
		ctx = withPrebuiltTools(ctx, prebuiltToolsBundle{Buckets: map[ToolDomain][]einoTool.BaseTool{DomainKnowledge: nil}})
	}
	ch, err := e.Execute(ctx, req, m)
	if err != nil {
		t.Fatal(err)
	}
	var events []Event
	for {
		select {
		case event, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, event)
		case <-ctx.Done():
			t.Fatalf("深度流程未关闭事件流: %v", ctx.Err())
		}
	}
}

func requirePEError(t *testing.T, events []Event) {
	t.Helper()
	found := false
	for _, event := range events {
		if event.Type == EventError {
			found = true
		}
		if event.Type == EventAnswer || event.Type == EventDone {
			t.Fatalf("失败时错误地发出最终回答: %+v", event)
		}
	}
	if !found {
		t.Fatalf("期望 EventError，实际事件: %+v", events)
	}
}

func TestPlanExecuteSameRoleParallelTasksOverlap(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var active, maxActive, started atomic.Int32
	release := make(chan struct{})
	var releaseOnce sync.Once
	base := peModel(parallelTestPlan, new(atomic.Int32))
	respond := base.respond
	base.respond = func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
		if strings.Contains(messages[0].Content, "研究知识库") {
			current := active.Add(1)
			for {
				old := maxActive.Load()
				if current <= old || maxActive.CompareAndSwap(old, current) {
					break
				}
			}
			if started.Add(1) == 2 {
				releaseOnce.Do(func() { close(release) })
			}
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			active.Add(-1)
			return schema.AssistantMessage("研究结果", nil), nil
		}
		return respond(ctx, messages)
	}
	events := collectPEBehaviorEvents(t, ctx, deepTestEngine(), Request{Query: "并行研究", UserID: "u", SessionID: "parallel"}, base)
	var answers, done int
	for _, event := range events {
		if event.Type == EventError {
			t.Fatalf("并行任务失败: %+v", event)
		}
		if event.Type == EventAnswer {
			answers++
		}
		if event.Type == EventDone {
			done++
		}
	}
	if started.Load() != 2 || maxActive.Load() < 2 {
		t.Fatalf("同角色任务没有重叠执行: started=%d max_active=%d", started.Load(), maxActive.Load())
	}
	if answers != 1 || done != 1 {
		t.Fatalf("最终事件数量错误: answers=%d done=%d", answers, done)
	}
}

func TestPlanExecuteSequentialDependencyOrder(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	plan := `{"strategy":"sequential","tasks":[{"id":"a","agent":"knowledge_research","title":"第一步","description":"第一步"},{"id":"b","agent":"knowledge_research","title":"第二步","description":"第二步","depends_on":["a"]},{"id":"s","agent":"synthesis_review","title":"汇总","depends_on":["b"]}]}`
	var mu sync.Mutex
	var order []string
	base := peModel(plan, new(atomic.Int32))
	respond := base.respond
	base.respond = func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
		if strings.Contains(messages[0].Content, "研究知识库") {
			query := messages[len(messages)-1].Content
			if strings.Contains(query, "第二步") && !strings.Contains(query, "研究结论") {
				return nil, fmt.Errorf("依赖任务缺少可信前置结果")
			}
			mu.Lock()
			if strings.Contains(messages[len(messages)-1].Content, "第一步") {
				order = append(order, "a")
			} else if strings.Contains(messages[len(messages)-1].Content, "第二步") {
				order = append(order, "b")
			}
			mu.Unlock()
		}
		return respond(ctx, messages)
	}
	events := collectPEBehaviorEvents(t, ctx, deepTestEngine(), Request{Query: "依赖研究", UserID: "u", SessionID: "sequential"}, base)
	for _, event := range events {
		if event.Type == EventError {
			t.Fatalf("串行依赖失败: %+v", event)
		}
	}
	if len(order) != 2 || order[0] != "a" || order[1] != "b" {
		t.Fatalf("依赖任务顺序错误: %v", order)
	}
}

func TestPlanExecuteMultipleApprovalsResumeOneByOne(t *testing.T) {
	var calls, research atomic.Int32
	toolCtx := withPrebuiltTools(context.Background(), prebuiltToolsBundle{Buckets: map[ToolDomain][]einoTool.BaseTool{
		DomainExternal: {deepApprovalTool{calls: &calls}},
	}})
	plan := `{"strategy":"parallel","tasks":[{"id":"k","agent":"knowledge_research","title":"独立知识研究"},{"id":"w1","agent":"web_research","title":"外部一"},{"id":"w2","agent":"web_research","title":"外部二"},{"id":"s","agent":"synthesis_review","title":"汇总","depends_on":["k","w1","w2"]}]}`
	m := peModel(plan, &research)
	respond := m.respond
	m.respond = func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
		if strings.Contains(messages[0].Content, "使用已挂载的外部工具") {
			for _, message := range messages {
				if message.Role == schema.Tool {
					return schema.AssistantMessage("外部结果", nil), nil
				}
			}
			return peCall("write_file", map[string]any{}), nil
		}
		return respond(ctx, messages)
	}
	engine := deepTestEngine()
	req := Request{Query: "两项外部研究", UserID: "u", SessionID: "approvals"}
	ctx, cancel := context.WithTimeout(toolCtx, 20*time.Second)
	defer cancel()
	events := collectPEBehaviorEvents(t, ctx, engine, req, m)
	for step := 0; step < 2; step++ {
		var interrupt *Event
		for i := range events {
			if events[i].Type == EventInterrupt {
				interrupt = &events[i]
				break
			}
		}
		if interrupt == nil {
			t.Fatalf("第 %d 次没有审批中断: %+v", step+1, events)
		}
		if calls.Load() != int32(step) || research.Load() != 1 {
			t.Fatalf("审批前工具或独立研究次数错误: calls=%d research=%d", calls.Load(), research.Load())
		}
		raw, found, err := engine.buildCheckpointStore(req.SessionID).Get(ctx, interrupt.CheckpointID)
		if err != nil || !found {
			t.Fatalf("读取 checkpoint 失败: found=%t err=%v", found, err)
		}
		other := deepTestEngine()
		if err := other.buildCheckpointStore(req.SessionID).Set(ctx, interrupt.CheckpointID, raw); err != nil {
			t.Fatal(err)
		}
		req.CheckpointID = interrupt.CheckpointID
		req.ResumeData = map[string]any{interrupt.InterruptID: &ApprovalResult{Approved: true}}
		events = collectPEBehaviorEvents(t, ctx, other, req, m)
		engine = other
	}
	if calls.Load() != 2 {
		t.Fatalf("两个审批点未逐个执行: calls=%d", calls.Load())
	}
	if research.Load() != 1 {
		t.Fatalf("审批恢复重复执行成功的独立研究: %d", research.Load())
	}
	done := false
	for _, event := range events {
		if event.Type == EventError {
			t.Fatalf("审批恢复失败: %+v", event)
		}
		if event.Type == EventDone {
			done = true
		}
	}
	if !done {
		t.Fatal("所有审批通过后未完成根流程")
	}
}

func TestPlanExecuteRejectsIncompleteDuplicateAndMismatchedDelegation(t *testing.T) {
	for _, test := range []struct {
		name, taskID, role string
		duplicate          bool
	}{
		{name: "missing task", taskID: "a", role: "knowledge_research"},
		{name: "role mismatch", taskID: "a", role: "synthesis_review"},
		{name: "premature dependency", taskID: "final", role: "synthesis_review"},
		{name: "duplicate task", taskID: "a", role: "knowledge_research", duplicate: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var research atomic.Int32
			m := peModel(parallelTestPlan, &research)
			respond := m.respond
			m.respond = func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
				if strings.Contains(messages[0].Content, "Solvify Executor") {
					message := peCall(test.role, map[string]string{"task_id": test.taskID})
					if test.duplicate {
						copy := message.ToolCalls[0]
						copy.ID += "-duplicate"
						message.ToolCalls = append(message.ToolCalls, copy)
					}
					return message, nil
				}
				return respond(ctx, messages)
			}
			events := collectPEEvents(t, deepTestContext(t), deepTestEngine(), Request{Query: test.name, UserID: "u", SessionID: test.name}, m)
			requirePEError(t, events)
			if research.Load() != 0 {
				t.Fatalf("非法批次校验前执行了合法分支: research=%d", research.Load())
			}
		})
	}
}

func TestPlanExecuteRejectsInvalidInitialPlans(t *testing.T) {
	for _, plan := range []string{
		`{"strategy":"parallel","tasks":[{"id":"a","agent":"unsupported","title":"未知角色"},{"id":"s","agent":"synthesis_review","title":"汇总","depends_on":["a"]}]}`,
		`{"strategy":"parallel","tasks":[{"id":"a","agent":"knowledge_research","title":"研究","depends_on":["s"]},{"id":"s","agent":"synthesis_review","title":"汇总","depends_on":["a"]}]}`,
		`{"strategy":"parallel","tasks":[{"id":"a","agent":"knowledge_research","title":"研究"},{"id":"s","agent":"synthesis_review","title":"汇总","depends_on":["missing"]}]}`,
		`{"strategy":"parallel","tasks":[{"id":"a","agent":"knowledge_research","title":"研究"},{"id":"s","agent":"synthesis_review","title":"汇总"}]}`,
	} {
		t.Run(plan, func(t *testing.T) {
			var research atomic.Int32
			events := collectPEEvents(t, deepTestContext(t), deepTestEngine(), Request{Query: "非法计划", UserID: "u", SessionID: "invalid"}, peModel(plan, &research))
			requirePEError(t, events)
			if research.Load() != 0 {
				t.Fatal("非法计划执行了研究")
			}
		})
	}
}

func TestPlanExecuteRejectsIllegalDispatchAndEarlyRespond(t *testing.T) {
	tests := []struct {
		name  string
		model func() *deepTestModel
	}{
		{name: "illegal dispatch", model: func() *deepTestModel {
			m := peModel(`{"strategy":"direct","tasks":[{"id":"s","agent":"synthesis_review","title":"回答"}]}`, new(atomic.Int32))
			m.respond = func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
				if strings.Contains(messages[0].Content, "Solvify Executor") {
					return peCall("synthesis_review", map[string]string{"task_id": "unknown"}), nil
				}
				return peModel(`{"strategy":"direct","tasks":[{"id":"s","agent":"synthesis_review","title":"回答"}]}`, new(atomic.Int32)).respond(ctx, messages)
			}
			return m
		}},
		{name: "early respond", model: func() *deepTestModel {
			m := peModel(parallelTestPlan, new(atomic.Int32))
			m.respond = func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
				if strings.Contains(messages[0].Content, "你是 Replanner") {
					return peCall("respond", map[string]string{"response": "提前结束"}), nil
				}
				return peModel(parallelTestPlan, new(atomic.Int32)).respond(ctx, messages)
			}
			return m
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			events := collectPEBehaviorEvents(t, ctx, deepTestEngine(), Request{Query: test.name, UserID: "u", SessionID: test.name}, test.model())
			requirePEError(t, events)
		})
	}
}

func TestPlanExecuteModelErrorAndCancellationCloseStream(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	errorModel := &deepTestModel{respond: func(context.Context, []*schema.Message) (*schema.Message, error) {
		return nil, fmt.Errorf("模型故障")
	}}
	events := collectPEBehaviorEvents(t, ctx, deepTestEngine(), Request{Query: "模型错误", UserID: "u", SessionID: "model-error"}, errorModel)
	requirePEError(t, events)

	cancelCtx, stop := context.WithCancel(withPrebuiltTools(context.Background(), prebuiltToolsBundle{Buckets: map[ToolDomain][]einoTool.BaseTool{DomainKnowledge: nil}}))
	started := make(chan struct{})
	var once sync.Once
	m := &deepTestModel{respond: func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
		once.Do(func() { close(started) })
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	ch, err := deepTestEngine().Execute(cancelCtx, Request{Query: "取消", UserID: "u", SessionID: "cancel"}, m)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		stop()
		t.Fatal("取消测试模型未开始执行")
	}
	stop()
	timeout := time.NewTimer(3 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return
			}
		case <-timeout.C:
			t.Fatal("取消后事件流未关闭")
		}
	}
}

type peBehaviorRetriever struct{}

func (peBehaviorRetriever) Retrieve(_ context.Context, query rag.Query) (rag.Result, error) {
	if query.UserID != "u" || len(query.KnowledgeBaseIDs) != 1 || query.KnowledgeBaseIDs[0] != "kb" {
		return rag.Result{}, fmt.Errorf("任务丢失检索权限范围")
	}
	return rag.Result{Hit: true, Documents: []rag.Document{{ID: query.Question, DocumentID: "doc-" + query.Question, KnowledgeBaseID: "kb", Title: query.Question, Content: "可信资料", Score: 1}}}, nil
}

func TestPlanExecuteTaskInputAndKnowledgeSourcesIsolated(t *testing.T) {
	if _, err := config.Load(""); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	engine := deepTestEngine()
	var mu sync.Mutex
	var tools []*tool.KnowledgeSearchTool
	engine.RegisterInternal("knowledge_search", 1, false, DomainKnowledge, func(_ context.Context, user string, kb []string) einoTool.BaseTool {
		instance := tool.NewKnowledgeSearchTool(peBehaviorRetriever{}).WithContext(user, kb)
		mu.Lock()
		tools = append(tools, instance)
		mu.Unlock()
		return instance
	})
	m := peModel(parallelTestPlan, new(atomic.Int32))
	respond := m.respond
	var checkedResults atomic.Bool
	m.respond = func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
		system := messages[0].Content
		if strings.Contains(system, "研究知识库") {
			for _, message := range messages {
				if strings.Contains(message.Content, "Solvify Executor") || strings.Contains(message.Content, "当前批次：") {
					return nil, fmt.Errorf("泄漏主管完整上下文")
				}
			}
			query := messages[1].Content
			topic := "topic-A"
			if strings.Contains(query, "topic-B") {
				topic = "topic-B"
			}
			for _, message := range messages {
				if message.Role == schema.Tool {
					return schema.AssistantMessage("结果-"+topic, nil), nil
				}
			}
			return peCall("knowledge_search", map[string]string{"query": topic}), nil
		}
		if strings.Contains(system, "你是 Replanner") && !checkedResults.Load() {
			content := messages[len(messages)-1].Content
			index := strings.Index(content, "执行状态：")
			var state struct{ Results map[string]TaskResult }
			if index < 0 || json.Unmarshal([]byte(content[index+len("执行状态："):]), &state) != nil {
				return nil, fmt.Errorf("缺少研究执行状态")
			}
			for id, topic := range map[string]string{"a": "topic-A", "b": "topic-B"} {
				result := state.Results[id]
				if result.Summary != "结果-"+topic || len(result.Evidence) != 1 || len(result.Evidence[0].Sources) != 1 || result.Evidence[0].Sources[0].Title != topic {
					return nil, fmt.Errorf("任务 %s 引用收集器或正文被污染: %+v", id, result)
				}
			}
			checkedResults.Store(true)
		}
		if strings.Contains(system, "仅使用结构化研究结果") {
			query := messages[len(messages)-1].Content
			if !strings.Contains(query, "结果-topic-A") || !strings.Contains(query, "结果-topic-B") {
				return nil, fmt.Errorf("汇总缺少可信前置结果")
			}
		}
		return respond(ctx, messages)
	}
	events := collectPEBehaviorEvents(t, ctx, engine, Request{Query: "联合研究", UserID: "u", SessionID: "isolated", KnowledgeBaseIDs: []string{"kb"}}, m)
	for _, event := range events {
		if event.Type == EventError {
			t.Fatalf("隔离任务失败: %+v", event)
		}
	}
	if !checkedResults.Load() || len(tools) != 2 || tools[0] == tools[1] {
		t.Fatalf("研究任务未使用独立工具实例: checked=%t tools=%d", checkedResults.Load(), len(tools))
	}
}

func TestPlanExecuteEmptyResultAndIterationLimits(t *testing.T) {
	for _, test := range []struct {
		name            string
		outer, executor int
		empty           bool
	}{
		{name: "empty synthesis", empty: true},
		{name: "outer limit", outer: 1},
		{name: "executor limit", executor: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			e := deepTestEngine()
			e.cfg.DeepMaxPlanExecuteRounds = test.outer
			e.cfg.DeepMaxExecutorIterations = test.executor
			m := peModel(parallelTestPlan, new(atomic.Int32))
			respond := m.respond
			if test.empty {
				m.respond = func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
					if strings.Contains(messages[0].Content, "仅使用结构化研究结果") {
						return schema.AssistantMessage("   ", nil), nil
					}
					return respond(ctx, messages)
				}
			}
			events := collectPEBehaviorEvents(t, ctx, e, Request{Query: test.name, UserID: "u", SessionID: test.name}, m)
			requirePEError(t, events)
			var final *ExecutionPlan
			for _, event := range events {
				if event.Plan != nil {
					final = event.Plan
				}
			}
			if final == nil || final.Status != TaskError || final.Phase != "failed" {
				t.Fatalf("失败时未发布完整终态: %+v", final)
			}
			for _, task := range final.Tasks {
				if task.Status == TaskPending || task.Status == TaskRunning || task.Status == TaskWaiting {
					t.Fatalf("流程失败后任务仍可执行: %+v", task)
				}
			}
		})
	}
}
