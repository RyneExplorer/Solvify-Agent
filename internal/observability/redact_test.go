package observability

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

// 这一组测试覆盖 P0-6：**落库出口的 PII 脱敏**。
//
// 缺陷原貌：观测数据有两条互不相干的出口 ——
//   - 日志出口：Recorder → BatchSink → LogSink.Write，LogSink 会先 clean 自己的副本；
//   - 落库出口：Recorder 直接调 dbSink.WriteFeedbacks / WriteAgentSteps / WriteTraces，
//     **完全不经过 BatchSink 扇出**（app.go 用 NewRecorderWithDBSink，dbSink 是单独字段，
//     不是 extraSinks），因此也从不调用任何 clean。
//
// 结果：`RecordFeedback` 把用户原文交给 DB，`chat_feedbacks.comment` 里是裸文本，
// 而同一时刻日志里打印的却是掩码版 —— 排查时看起来「已经脱敏了」，极具欺骗性。
//
// 本文件的断言分三层：
//  1. 出口级：给 Recorder 灌裸数据，断言 DBSink 收到的是掩码版（真回归测试，去掉修复即变红）；
//  2. 纯函数级：sanitize* 不改入参，且幂等；
//  3. 别名级：DBSink 拿到的 trace 树与原树不是同一对象（旧 cleanSpan 就地改写子节点的坑）。

// redactSink 同时抓取三类落库载荷的 DBSink 实现。
type redactSink struct {
	mu        sync.Mutex
	traces    []*Trace
	feedbacks []*Feedback
	steps     []*AgentStep
}

func (s *redactSink) Write(context.Context, *SinkRecord) error { return nil }
func (s *redactSink) Shutdown(context.Context) error           { return nil }

func (s *redactSink) WriteTraces(_ context.Context, ts []*Trace) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.traces = append(s.traces, ts...)
	return nil
}

func (s *redactSink) WriteFeedbacks(_ context.Context, fs []*Feedback) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.feedbacks = append(s.feedbacks, fs...)
	return nil
}

func (s *redactSink) WriteAgentSteps(_ context.Context, ss []*AgentStep) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.steps = append(s.steps, ss...)
	return nil
}

func (s *redactSink) onlyFeedback(t *testing.T) *Feedback {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.feedbacks) != 1 {
		t.Fatalf("期望 DBSink 收到恰好 1 条 feedback，实际 %d 条", len(s.feedbacks))
	}
	return s.feedbacks[0]
}

func (s *redactSink) onlyStep(t *testing.T) *AgentStep {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.steps) != 1 {
		t.Fatalf("期望 DBSink 收到恰好 1 条 agent_step，实际 %d 条", len(s.steps))
	}
	return s.steps[0]
}

func (s *redactSink) onlyTrace(t *testing.T) *Trace {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.traces) != 1 {
		t.Fatalf("期望 DBSink 收到恰好 1 条 trace，实际 %d 条", len(s.traces))
	}
	return s.traces[0]
}

func newRedactRecorder(t *testing.T, sink *redactSink) Recorder {
	t.Helper()
	rec := NewRecorderWithDBSink(testObsConfig(), sink)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = rec.Shutdown(ctx)
	})
	return rec
}

// ─── 测试数据 ────────────────────────────────────────────────────────────────

// 全部用「一眼能认出」的地址与号码，且故意互不相同 ——
// 断言「原文已消失」时不会因为两个串互为子串而误判。
const (
	rawEmailRoot  = "alice@example.com"
	rawEmailErr   = "bob@example.com"
	rawEmailMap   = "carol@example.com"
	rawEmailArr   = "dave@example.com"
	rawEmailEvent = "eve@example.com"
	rawPhoneAttr  = "13712345678"
	rawPhoneChild = "13812345678"
	rawPhoneErr   = "13900001111"
)

var rawPII = []string{
	rawEmailRoot, rawEmailErr, rawEmailMap, rawEmailArr, rawEmailEvent,
	rawPhoneAttr, rawPhoneChild, rawPhoneErr,
}

// rawTraceTree 造一棵「每个可落库字段都带裸 PII」的 span 树，
// 并故意塞入 nil 子节点 / nil 事件 —— 旧 cleanSpan 对 nil 子节点会直接 panic。
func rawTraceTree() *Trace {
	return &Trace{
		ID:         "t-redact",
		RequestID:  "req-redact",
		UserID:     "u-1",
		SessionID:  "s-1",
		SampleRate: 1.0,
		Sampled:    true,
		Root: &Span{
			TraceID:   "t-redact",
			SpanID:    "sp-root",
			Name:      "chat.deep",
			Component: ComponentAgentEngine,
			Status:    SpanStatusError,
			Error:     "上游校验失败: 用户 " + rawEmailErr + " 的手机号 " + rawPhoneErr + " 无效",
			Attrs: Attrs{
				"query":        "帮我把 " + rawEmailRoot + " 的账号停用，回电 " + rawPhoneAttr,
				"nested":       map[string]any{"email": rawEmailMap},
				"list":         []string{rawEmailArr},
				"string_map":   map[string]string{"email": rawEmailMap},
				"not_a_string": 42,
			},
			Events: []*SpanEvent{
				{Name: "llm.first_token", Attrs: Attrs{"note": "联系 " + rawEmailEvent}},
				nil,
			},
			Children: []*Span{
				{
					TraceID:   "t-redact",
					SpanID:    "sp-child",
					ParentID:  "sp-root",
					Name:      "tool.search",
					Component: ComponentAgentTool,
					Status:    SpanStatusOK,
					Attrs:     Attrs{"input": rawPhoneChild},
				},
				nil,
			},
		},
	}
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal 失败: %v", err)
	}
	return string(b)
}

func assertNoRawPII(t *testing.T, where, got string) {
	t.Helper()
	for _, raw := range rawPII {
		if strings.Contains(got, raw) {
			t.Errorf("%s 仍含裸 PII %q，全文: %s", where, raw, got)
		}
	}
}

// ─── 1. 出口级回归（去掉修复即变红） ────────────────────────────────────────

// TestRecordFeedback_DBSinkGetsMaskedComment 是本缺陷的直接回归测试。
//
// 反馈评论是用户自由填写的文本，最可能夹带邮箱 / 手机号。
// 修复前 `RecordFeedback` 把 `fb` 原样交给 dbSink，chat_feedbacks.comment 落裸文本。
func TestRecordFeedback_DBSinkGetsMaskedComment(t *testing.T) {
	sink := &redactSink{}
	rec := newRedactRecorder(t, sink)

	fb := &Feedback{
		MessageID: "m-1",
		UserID:    "u-1",
		Rating:    -1,
		Comment:   "答错了。请联系 " + rawEmailRoot + " 或 " + rawPhoneAttr + " 复现",
		Reasons:   []string{"答案错误 " + rawEmailArr},
	}
	rec.RecordFeedback(fb)

	got := sink.onlyFeedback(t)
	assertNoRawPII(t, "chat_feedbacks.comment/reasons", mustJSON(t, got))

	// 掩码要保留可读的头尾，而不是整段抹掉（否则前端无法定位问题）。
	if !strings.Contains(got.Comment, "al***@example.com") {
		t.Errorf("邮箱掩码形态不对: %q", got.Comment)
	}
	if !strings.Contains(got.Comment, "137****5678") {
		t.Errorf("手机号掩码形态不对: %q", got.Comment)
	}

	// 出口不得改写调用方对象：SubmitFeedback 之后本地变量还要用。
	if fb.Comment == got.Comment {
		t.Error("dbSink 收到了与入参同一个 *Feedback，说明没有走副本")
	}
	if !strings.Contains(fb.Comment, rawEmailRoot) {
		t.Errorf("入参 comment 被就地改写了: %q", fb.Comment)
	}
}

// TestRecordAgentStep_DBSinkGetsMaskedFields 覆盖 agent_step 出口的四个自由文本字段。
func TestRecordAgentStep_DBSinkGetsMaskedFields(t *testing.T) {
	sink := &redactSink{}
	rec := newRedactRecorder(t, sink)

	step := &AgentStep{
		TaskID:            "task-1",
		StepIndex:         0,
		ThinkingSummary:   "用户在问 " + rawEmailRoot + " 的权限",
		ToolName:          "search",
		ToolInputMasked:   `{"q":"` + rawEmailMap + `"}`,
		ToolResultSummary: "命中 3 条，其中一条属于 " + rawEmailArr,
		ToolStatus:        "error",
		ToolError:         "鉴权失败: token=abcdefghijklmnop " + rawPhoneChild,
		LatencyMs:         120,
	}
	rec.RecordAgentStep(step)

	got := sink.onlyStep(t)
	assertNoRawPII(t, "chat_agent_steps.*", mustJSON(t, got))

	if !strings.Contains(got.ToolError, "abcd***mnop") {
		t.Errorf("密钥掩码形态不对: %q", got.ToolError)
	}
	if strings.Contains(step.ToolError, "abcd***mnop") {
		t.Error("入参 agent_step 被就地改写了")
	}
}

// TestRecordTrace_DBSinkGetsSanitizedIndependentCopy 覆盖 trace 出口。
//
// 两条断言：
//   - 脱敏：DB 那份树里没有裸 PII；
//   - 独立：DB 拿到的树与原树**不是同一个对象**。落库仓储会就地对树做
//     stripInternalSpanAttrs 改写 + json.Marshal，而日志侧在 BatchSink 的后台 goroutine
//     上并发序列化同一棵树 —— 共享可变树就是对同一结构体的并发读写。
func TestRecordTrace_DBSinkGetsSanitizedIndependentCopy(t *testing.T) {
	sink := &redactSink{}
	rec := newRedactRecorder(t, sink)

	tr := rawTraceTree()
	before := mustJSON(t, tr)
	rec.RecordTrace(tr)

	got := sink.onlyTrace(t)
	assertNoRawPII(t, "chat_traces.span_tree/error", mustJSON(t, got))

	if got.Root == tr.Root {
		t.Error("DBSink 拿到了原树的同一个 *Span，落库侧的就地改写会污染日志侧")
	}
	if after := mustJSON(t, tr); after != before {
		t.Errorf("原树被就地改写了\nbefore=%s\nafter =%s", before, after)
	}
	// 关键字段不能被深拷贝丢掉。
	if got.ID != tr.ID || got.UserID != tr.UserID || got.SessionID != tr.SessionID || !got.Sampled {
		t.Errorf("深拷贝丢了 trace 元信息: %+v", got)
	}
	if got.Root.Name != "chat.deep" || got.Root.Status != SpanStatusError {
		t.Errorf("深拷贝丢了根 span 元信息: %+v", got.Root)
	}
}

// ─── 2. 纯函数级 ────────────────────────────────────────────────────────────

// TestSanitizeTrace_DoesNotMutateOriginal 钉死「不改入参」。
//
// 回归价值：旧 cleanSpan 用 `s.Children[i] = &cp` 就地替换子节点 —— 调用方以为只是
// 拿了份干净副本去打印，手里的树已经被改了一半。谁把 sanitizeTrace 里的 cloneSpan
// 去掉「优化」成直接在原树上清，本测试立刻变红。
func TestSanitizeTrace_DoesNotMutateOriginal(t *testing.T) {
	pii := NewPIISanitizer(200, true)
	tr := rawTraceTree()
	before := mustJSON(t, tr)

	cleaned := sanitizeTrace(tr, pii)

	assertNoRawPII(t, "sanitizeTrace 返回值", mustJSON(t, cleaned))
	if after := mustJSON(t, tr); after != before {
		t.Errorf("sanitizeTrace 改了入参\nbefore=%s\nafter =%s", before, after)
	}
	// nil 子节点 / nil 事件必须被安全跳过（旧实现对 nil 子节点解引用 panic）。
	if len(cleaned.Root.Children) != 1 || cleaned.Root.Children[0] == nil {
		t.Errorf("nil 子节点处理不对，Children=%+v", cleaned.Root.Children)
	}
	if len(cleaned.Root.Events) != 1 || cleaned.Root.Events[0] == nil {
		t.Errorf("nil 事件处理不对，Events=%+v", cleaned.Root.Events)
	}
	// 非字符串 attrs 值不能被打平或丢失。
	if got, ok := cleaned.Root.Attrs["not_a_string"]; !ok || got != 42 {
		t.Errorf("非字符串 attr 丢了: %v", cleaned.Root.Attrs["not_a_string"])
	}
	// 嵌套结构里的 PII 也要清（SanitizeAttrs 递归）。
	nested, ok := cleaned.Root.Attrs["nested"].(map[string]any)
	if !ok {
		t.Fatalf("nested attr 类型变了: %T", cleaned.Root.Attrs["nested"])
	}
	if _, leaked := nested["email"]; !leaked || strings.Contains(nested["email"].(string), rawEmailMap) {
		t.Errorf("嵌套 map 里的 PII 未清理: %v", nested)
	}
}

// TestSanitizeFeedback_ReasonsCopiedNotAliased 断言 Reasons 切片被复制，
// 否则 dbSink 与调用方共享同一个底层数组。
func TestSanitizeFeedback_ReasonsCopiedNotAliased(t *testing.T) {
	pii := NewPIISanitizer(200, true)
	f := &Feedback{Comment: rawEmailRoot, Reasons: []string{"理由 " + rawEmailArr}}

	cleaned := sanitizeFeedback(f, pii)

	assertNoRawPII(t, "sanitizeFeedback 返回值", mustJSON(t, cleaned))
	if &f.Reasons[0] == &cleaned.Reasons[0] {
		t.Error("Reasons 底层数组被共享了")
	}
	if f.Reasons[0] != "理由 "+rawEmailArr {
		t.Errorf("入参 Reasons 被就地改写: %v", f.Reasons)
	}
}

// TestSanitizeRecord_Idempotent 断言二次脱敏与一次等价。
//
// 为什么需要：出口可能叠加多层脱敏（例如将来把清洁上提到扇出边界、LogSink 保留一份
// 纵深防御）。若掩码产物能再次命中自己的正则，二次脱敏会把 "al***@example.com" 继续啃，
// 出现 "al***@***" 这类信息量归零的结果。
func TestSanitizeRecord_Idempotent(t *testing.T) {
	pii := NewPIISanitizer(200, true)
	rec := &SinkRecord{
		Kind:      "trace",
		Timestamp: time.Unix(0, 0),
		Trace:     rawTraceTree(),
		Feedback:  &Feedback{Comment: "联系 " + rawEmailRoot + " " + rawPhoneAttr, Reasons: []string{rawEmailArr}},
		AgentStep: &AgentStep{ThinkingSummary: rawEmailEvent, ToolError: "token=abcdefghijklmnop"},
		Attrs:     Attrs{"note": rawEmailMap},
	}

	once := sanitizeRecord(rec, pii)
	twice := sanitizeRecord(once, pii)

	assertNoRawPII(t, "一次脱敏结果", mustJSON(t, once))
	if a, b := mustJSON(t, once), mustJSON(t, twice); a != b {
		t.Errorf("脱敏不幂等\nonce =%s\ntwice=%s", a, b)
	}
}

// TestSanitizeString_Idempotent 直接钉住脱敏器本身的幂等性（上面 Record 级幂等的根基）。
func TestSanitizeString_Idempotent(t *testing.T) {
	pii := NewPIISanitizer(500, true)
	inputs := []string{
		rawEmailRoot,
		rawPhoneAttr,
		"Authorization: Bearer sk-abcdefghijklmnop",
		"api_key=abcdefghijklmnop",
		"混合文本 " + rawEmailRoot + " / " + rawPhoneChild + " / token=abcdefghijklmnop",
	}
	for _, in := range inputs {
		once := pii.SanitizeString(in)
		if twice := pii.SanitizeString(once); twice != once {
			t.Errorf("SanitizeString 不幂等: in=%q once=%q twice=%q", in, once, twice)
		}
		if once == in {
			t.Errorf("SanitizeString 没生效: in=%q", in)
		}
	}
}

// TestSanitizeRecord_NilPIIKeepsShallowCopy 钉住 pii 为 nil 时的退化行为：
// 不脱敏，但顶层仍是副本，调用方不会被 sink 改写。
func TestSanitizeRecord_NilPIIKeepsShallowCopy(t *testing.T) {
	rec := &SinkRecord{Kind: "feedback", Feedback: &Feedback{Comment: rawEmailRoot}}
	cp := sanitizeRecord(rec, nil)
	if cp == rec {
		t.Fatal("pii 为 nil 时也应返回顶层副本")
	}
	if cp.Feedback.Comment != rawEmailRoot {
		t.Errorf("pii 为 nil 时不该改动内容: %q", cp.Feedback.Comment)
	}
	if sanitizeRecord(nil, nil) != nil {
		t.Error("nil 入参应返回 nil")
	}
}
