package agent

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	langfuse "github.com/cloudwego/eino-ext/callbacks/langfuse/v2"
	"github.com/cloudwego/eino/callbacks"
	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"

	"solvify-agent/internal/observability"
)

type peSpanCapture struct {
	mu    sync.Mutex
	spans []sdktrace.ReadOnlySpan
}

func TestPlanExecuteRootTraceCompletionPhase(t *testing.T) {
	for _, failed := range []bool{false, true} {
		name := "completed"
		if failed {
			name = "failed"
		}
		t.Run(name, func(t *testing.T) {
			capture := &peSpanCapture{}
			provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(capture))
			t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
			ctx, span := provider.Tracer("completion-test").Start(deepTestContext(t), "completion-root")
			m := peModel(`{"strategy":"direct","tasks":[{"id":"s","agent":"synthesis_review","title":"审查"}]}`, new(atomic.Int32))
			base := m.respond
			m.respond = func(ctx context.Context, msgs []*schema.Message) (*schema.Message, error) {
				msg, err := base(ctx, msgs)
				if failed && err == nil && strings.Contains(msgs[0].Content, "Solvify Planner") {
					msg.ResponseMeta = &schema.ResponseMeta{FinishReason: "length"}
				}
				return msg, err
			}
			collectPEEvents(t, ctx, deepTestEngine(), Request{Query: "核对", UserID: "u", SessionID: "root-phase"}, m)
			span.End()
			capture.mu.Lock()
			defer capture.mu.Unlock()
			if len(capture.spans) != 1 || peSpanAttr(capture.spans[0], "langfuse.observation.metadata.phase") != name {
				t.Fatalf("根链路终态不准确: want=%s spans=%+v", name, capture.spans)
			}
		})
	}
}

func TestPlanExecuteLangfuseResumeCorrelation(t *testing.T) {
	capture := &peSpanCapture{}
	handler, err := langfuse.NewHandler(context.Background(), &langfuse.Config{
		Host: "https://langfuse.invalid", PublicKey: "pk-test", SecretKey: "sk-test",
		SpanExporter: capture, MaxExportBatchSize: 1, BatchTimeout: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })
	var research, writes atomic.Int32
	baseCtx := withPrebuiltTools(deepTestContext(t), prebuiltToolsBundle{Buckets: map[ToolDomain][]einoTool.BaseTool{DomainExternal: {deepApprovalTool{calls: &writes}}}})
	newContext := func() context.Context {
		ctx := handler.StartTrace(baseCtx, langfuse.WithName("deep-resume"))
		return callbacks.InitCallbacks(ctx, nil, observability.WithExecutionTracing(handler))
	}
	const plan = `{"strategy":"parallel","tasks":[{"id":"a","agent":"knowledge_research","title":"内部研究"},{"id":"w","agent":"web_research","title":"外部研究"},{"id":"s","agent":"synthesis_review","title":"汇总","depends_on":["a","w"]}]}`
	m := peModel(plan, &research)
	baseResponse := m.respond
	m.respond = func(ctx context.Context, messages []*schema.Message) (*schema.Message, error) {
		if strings.Contains(messages[0].Content, "使用已挂载的外部工具") {
			for _, message := range messages {
				if message.Role == schema.Tool {
					return schema.AssistantMessage("外部结果", nil), nil
				}
			}
			return peCall("write_file", map[string]any{}), nil
		}
		return baseResponse(ctx, messages)
	}
	e := deepTestEngine()
	req := Request{Query: "审批研究", UserID: "u", SessionID: "s"}
	ctx := newContext()
	firstEvents := collectPEEvents(t, ctx, e, req, m)
	var interrupt Event
	for _, event := range firstEvents {
		if event.Type == EventError {
			t.Fatalf("中断前执行错误: %+v", event)
		}
		if event.Type == EventInterrupt {
			interrupt = event
		}
	}
	if interrupt.CheckpointID == "" || interrupt.PlanID == "" {
		t.Fatal("缺少审批 checkpoint/计划标识")
	}
	handler.EndTrace(ctx, "等待审批")
	other := deepTestEngine()
	raw, found, err := e.buildCheckpointStore("s").Get(ctx, interrupt.CheckpointID)
	if err != nil || !found {
		t.Fatalf("checkpoint 未保存: %v", err)
	}
	if err := other.buildCheckpointStore("s").Set(ctx, interrupt.CheckpointID, raw); err != nil {
		t.Fatal(err)
	}
	req.CheckpointID = interrupt.CheckpointID
	req.ResumeData = map[string]any{interrupt.InterruptID: &ApprovalResult{Approved: true}}
	ctx = newContext()
	for _, event := range collectPEEvents(t, ctx, other, req, m) {
		if event.Type == EventError {
			t.Fatalf("恢复后执行错误: %+v", event)
		}
	}
	handler.EndTrace(ctx, "最终结论")
	flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := handler.Shutdown(flushCtx); err != nil {
		t.Fatal(err)
	}
	capture.mu.Lock()
	spans := append([]sdktrace.ReadOnlySpan(nil), capture.spans...)
	capture.mu.Unlock()
	seenInitial, seenResumed, knowledgeCount := false, false, 0
	traceIDs := map[trace.TraceID]bool{}
	for _, span := range spans {
		if span.Name() == "knowledge_research.professional" {
			knowledgeCount++
		}
		if span.Name() != "web_research.professional" {
			continue
		}
		metadata := "langfuse.observation.metadata."
		if peSpanAttr(span, metadata+"checkpoint_id") != interrupt.CheckpointID || peSpanAttr(span, metadata+"plan_id") != interrupt.PlanID || peSpanAttr(span, metadata+"task_id") != "w" {
			t.Errorf("恢复关联字段不一致: %+v", span.Attributes())
		}
		traceIDs[span.SpanContext().TraceID()] = true
		switch peSpanAttr(span, metadata+"resumed") {
		case "true":
			seenResumed = true
		case "false":
			seenInitial = true
		}
	}
	if !seenInitial || !seenResumed || len(traceIDs) != 2 || knowledgeCount != 1 || research.Load() != 1 || writes.Load() != 1 {
		t.Fatalf("跨请求恢复关联或成功任务复用错误: initial=%t resumed=%t traces=%d knowledge=%d research=%d writes=%d", seenInitial, seenResumed, len(traceIDs), knowledgeCount, research.Load(), writes.Load())
	}
}

func (c *peSpanCapture) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.spans = append(c.spans, spans...)
	return nil
}
func (*peSpanCapture) Shutdown(context.Context) error { return nil }

func peSpanAttr(span sdktrace.ReadOnlySpan, key string) string {
	for _, attr := range span.Attributes() {
		if string(attr.Key) == key {
			return attr.Value.Emit()
		}
	}
	return ""
}

func TestPlanExecuteLangfuseHierarchyAndParallelMetadata(t *testing.T) {
	capture := &peSpanCapture{}
	handler, err := langfuse.NewHandler(context.Background(), &langfuse.Config{
		Host: "https://langfuse.invalid", PublicKey: "pk-test", SecretKey: "sk-test",
		SpanExporter: capture, MaxExportBatchSize: 1, BatchTimeout: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = handler.Shutdown(context.Background()) })
	ctx := handler.StartTrace(deepTestContext(t), langfuse.WithName("deep-native"))
	ctx = callbacks.InitCallbacks(ctx, nil, observability.WithExecutionTracing(handler))
	const plan = `{"strategy":"parallel","tasks":[{"id":"a","agent":"knowledge_research","title":"内部研究 A"},{"id":"b","agent":"knowledge_research","title":"内部研究 B"},{"id":"s","agent":"synthesis_review","title":"汇总","depends_on":["a","b"]}]}`
	var research atomic.Int32
	events := collectPEEvents(t, ctx, deepTestEngine(), Request{Query: "并行研究", UserID: "u", SessionID: "s"}, peModel(plan, &research))
	for _, ev := range events {
		if ev.Type == EventError {
			t.Fatalf("原生流程失败: %+v", ev)
		}
	}
	if research.Load() != 2 {
		t.Fatalf("研究任务未完成: %d", research.Load())
	}
	handler.EndTrace(ctx, "最终结论")
	// 官方 Agent 回调异步消费 Events。Shutdown 等待消费结束后再刷出所有 span。
	flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := handler.Shutdown(flushCtx); err != nil {
		t.Fatal(err)
	}
	capture.mu.Lock()
	spans := append([]sdktrace.ReadOnlySpan(nil), capture.spans...)
	capture.mu.Unlock()
	byID := map[trace.SpanID]sdktrace.ReadOnlySpan{}
	byName := map[string][]sdktrace.ReadOnlySpan{}
	for _, span := range spans {
		byID[span.SpanContext().SpanID()] = span
		byName[span.Name()] = append(byName[span.Name()], span)
	}
	for _, name := range []string{"plan_execute_replan", "planner", "executor", "replanner", "knowledge_research", "knowledge_research.professional", "synthesis_review.professional"} {
		if len(byName[name]) == 0 {
			t.Errorf("缺少原生 span %s", name)
		}
	}
	seen := map[string]bool{}
	for _, span := range byName["knowledge_research.professional"] {
		metadata := "langfuse.observation.metadata."
		taskID := peSpanAttr(span, metadata+"task_id")
		seen[taskID] = true
		if peSpanAttr(span, metadata+"plan_id") == "" || peSpanAttr(span, metadata+"checkpoint_id") == "" || peSpanAttr(span, metadata+"plan_version") != "1" || peSpanAttr(span, metadata+"execution_round") != "1" {
			t.Errorf("专业 Agent 缺少正确关联字段: %s %+v", taskID, span.Attributes())
		}
		// AgentTool Agent → 专业 Agent，往上还必须有委派 Tool 和 Executor。
		parent := byID[span.Parent().SpanID()]
		if parent == nil || parent.Name() != "knowledge_research" || peSpanAttr(parent, metadata+"task_id") != taskID {
			t.Fatalf("专业 Agent 父节点或并行任务标识错误: task=%s parent=%v", taskID, parent)
		}
		toolFound, executorFound := false, false
		for ancestor := parent; ancestor != nil; ancestor = byID[ancestor.Parent().SpanID()] {
			if peSpanAttr(ancestor, metadata+"eino_component") == "Tool" {
				toolFound = true
				if peSpanAttr(ancestor, metadata+"task_id") != taskID {
					t.Errorf("委派工具任务标识错误: %+v", ancestor.Attributes())
				}
			}
			if ancestor.Name() == "executor" {
				executorFound = true
			}
		}
		if !toolFound || !executorFound {
			t.Errorf("层级缺少 AgentTool 或 Executor: task=%s tool=%t executor=%t", taskID, toolFound, executorFound)
		}
	}
	if !seen["a"] || !seen["b"] || len(seen) != 2 {
		t.Errorf("同角色并行任务标识未隔离: %+v", seen)
	}
}
