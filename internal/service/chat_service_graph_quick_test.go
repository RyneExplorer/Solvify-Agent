package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	requestdto "solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/rag"
	"solvify-agent/internal/repository"
	"solvify-agent/pkg/config"
)

// ─── 空回答（上游返回 200 但 content 为空）的回归测试 ───────────────────────
//
// 背景：上游 OpenAI 兼容网关会偶发返回「成功但 content 为空」（实测 gpt-5.5 会出现）。
// 旧实现把它当成功收尾：发 done 事件 + 把空 assistant 消息落库，后果是
//   1. 用户看到空白气泡；
//   2. 空消息进入后续 history（AssistantMessage("")），部分厂商对空 content 直接 400，
//      一次空回答会污染整条会话的后续每一轮。
//
// 这里用假上游（httptest）真实驱动一次快速模式全链路，断言终态是 error 事件而非 done，
// 且没有任何 assistant 消息落库。

// newEmptyAnswerUpstream 起一个 OpenAI 兼容的假上游：对任意请求都回一个流式响应，
// 但所有 delta 的 content 都是空串，最后以 finish_reason=stop 收尾。
func newEmptyAnswerUpstream(t *testing.T) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		chunks := []string{
			`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[{"index":0,"delta":{"role":"assistant","content":""},"finish_reason":null}]}`,
			`{"id":"c1","object":"chat.completion.chunk","created":1,"model":"test-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		}
		for _, c := range chunks {
			if _, err := w.Write([]byte("data: " + c + "\n\n")); err != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		if _, err := w.Write([]byte("data: [DONE]\n\n")); err != nil {
			return
		}
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// ensureTestConfig 初始化全局配置。
// 快速模式的检索节点会读 config.Get().RAG.TopK，而 Get() 在未初始化时直接 panic。
// 测试进程通常没有 configs/config.yaml，Load 会回落到 Default()。
func ensureTestConfig(t *testing.T) {
	t.Helper()
	if _, err := config.Load(""); err != nil {
		t.Fatalf("初始化测试配置失败: %v", err)
	}
}

// fakeChatMessageRepo 只实现快速模式链路会用到的方法，其余返回零值。
// Create 把落库的消息投进 createdCh，供「空回答不得落库」的断言使用。
type fakeChatMessageRepo struct {
	createdCh chan *entity.ChatMessage
}

func newFakeChatMessageRepo() *fakeChatMessageRepo {
	return &fakeChatMessageRepo{createdCh: make(chan *entity.ChatMessage, 8)}
}

func (f *fakeChatMessageRepo) Create(_ context.Context, message *entity.ChatMessage) error {
	select {
	case f.createdCh <- message:
	default:
	}
	return nil
}

func (f *fakeChatMessageRepo) FindByID(context.Context, string) (*entity.ChatMessage, error) {
	return nil, nil
}
func (f *fakeChatMessageRepo) FindBySessionID(context.Context, string) ([]entity.ChatMessage, error) {
	return nil, nil
}
func (f *fakeChatMessageRepo) FindBySessionIDForContext(context.Context, string) ([]entity.ChatMessage, error) {
	return nil, nil
}
func (f *fakeChatMessageRepo) FindRecent(context.Context, string, int) ([]entity.ChatMessage, error) {
	return nil, nil
}
func (f *fakeChatMessageRepo) FindRecentForContext(context.Context, string, int) ([]entity.ChatMessage, error) {
	return nil, nil
}
func (f *fakeChatMessageRepo) DeleteBySessionID(context.Context, string) error { return nil }
func (f *fakeChatMessageRepo) SearchByKeyword(context.Context, string, string, int) ([]repository.ChatMessageSearchRow, error) {
	return nil, nil
}
func (f *fakeChatMessageRepo) SearchRecentByKeywords(context.Context, string, []string, int) ([]entity.ChatMessage, error) {
	return nil, nil
}
func (f *fakeChatMessageRepo) SearchRecentByVector(context.Context, string, []float32, int, float64) ([]entity.ChatMessage, error) {
	return nil, nil
}
func (f *fakeChatMessageRepo) UpdateEmbedding(context.Context, string, entity.FloatVector) error {
	return nil
}

// fakeUserModelConfigRepo 只提供 GetByID，返回指向假上游的 openai 兼容配置。
type fakeUserModelConfigRepo struct {
	cfg *entity.UserModelConfig
}

func (f *fakeUserModelConfigRepo) Create(context.Context, *entity.UserModelConfig) error { return nil }
func (f *fakeUserModelConfigRepo) Update(context.Context, *entity.UserModelConfig) error { return nil }
func (f *fakeUserModelConfigRepo) Delete(context.Context, string, string) error          { return nil }
func (f *fakeUserModelConfigRepo) GetByID(context.Context, string, string) (*entity.UserModelConfig, error) {
	return f.cfg, nil
}
func (f *fakeUserModelConfigRepo) ListByUserID(context.Context, string) ([]entity.UserModelConfig, error) {
	return nil, nil
}
func (f *fakeUserModelConfigRepo) ExistsByModelID(context.Context, string, string, string) (bool, error) {
	return false, nil
}

// emptyRetriever 检索一律返回空结果：本用例只关心「上游空回答」这条路径。
type emptyRetriever struct{}

func (emptyRetriever) Retrieve(context.Context, rag.Query) (rag.Result, error) {
	return rag.Result{}, nil
}

// fakeSessionRepo 只实现快速模式链路会用到的方法，其余经内嵌接口保留
// （未实现的方法被调用会 panic —— 用例只走澄清路径，够用且不必写全）。
type fakeSessionRepo struct {
	repository.ChatSessionRepo
	setCalls    int
	pendingData []byte
}

func (f *fakeSessionRepo) SetPendingClarify(_ context.Context, _ string, data []byte) error {
	f.setCalls++
	f.pendingData = data
	return nil
}

// newClarifyUpstream 起一个 OpenAI 兼容的假上游：**非流式**返回一段改写 JSON。
// 快速模式的改写走 Generate（非流式），生成才走 Stream —— 这里只需要前者。
func newClarifyUpstream(t *testing.T, rewriteJSON string) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		body, _ := json.Marshal(map[string]any{
			"id":      "c1",
			"object":  "chat.completion",
			"created": 1,
			"model":   "test-model",
			"choices": []any{map[string]any{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": rewriteJSON},
				"finish_reason": "stop",
			}},
		})
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newQuickTestService 组装一个只依赖假上游的最小 chatService。
func newQuickTestService(t *testing.T, upstreamURL string) (*chatService, *fakeChatMessageRepo, *fakeSessionRepo) {
	t.Helper()
	msgRepo := newFakeChatMessageRepo()
	sessRepo := &fakeSessionRepo{}
	einoRetriever := rag.NewEinoRetrieverAdapter(emptyRetriever{}, 10)
	// 与生产路径同源：Graph 在「构造期」编译一次，请求期复用（见 compileQuickGraph）。
	quickGraph, err := compileQuickGraph(einoRetriever)
	if err != nil {
		t.Fatalf("编译快速检索链路失败: %v", err)
	}
	return &chatService{
		messageRepo: msgRepo,
		sessionRepo: sessRepo,
		userModelConfigRepo: &fakeUserModelConfigRepo{cfg: &entity.UserModelConfig{
			APIFormat:        "openai",
			BaseURL:          upstreamURL,
			ModelID:          "test-model",
			APIKey:           "test-key",
			MaxContextLength: 8192,
		}},
		einoRetriever: einoRetriever,
		quickGraph:    quickGraph,
	}, msgRepo, sessRepo
}

func describeEvents(events []dto.StreamEvent) string {
	parts := make([]string, 0, len(events))
	for _, e := range events {
		if e.Title != "" {
			parts = append(parts, e.Type+"("+e.Title+")")
			continue
		}
		parts = append(parts, e.Type)
	}
	return strings.Join(parts, " → ")
}

func TestProcessMessageGraphQuick_EmptyAnswerBecomesErrorNotBlankMessage(t *testing.T) {
	ensureTestConfig(t)
	upstream := newEmptyAnswerUpstream(t)
	svc, msgRepo, _ := newQuickTestService(t, upstream.URL)

	req := requestdto.SendMessageRequest{
		Content:          "OSI 七层模型分别是什么",
		KnowledgeBaseIDs: []string{"11111111-1111-1111-1111-111111111111"},
		SearchMode:       chatModeQuick,
		ModelID:          "cfg-1",
		ModelType:        "user",
	}
	// 无消费者，但缓冲足够容纳本次全部事件
	eventCh := make(chan dto.StreamEvent, 100)

	svc.processMessageGraphQuick(context.Background(), "user-1", "session-1", "umsg-1", req, eventCh)

	var (
		events   []dto.StreamEvent
		gotStart bool
		gotDone  bool
		gotErr   *dto.StreamEvent
	)
drain:
	for {
		select {
		case ev := <-eventCh:
			events = append(events, ev)
			switch ev.Type {
			case "start":
				gotStart = true
			case "done":
				gotDone = true
			case "error":
				e := ev
				gotErr = &e
			}
		default:
			break drain
		}
	}

	// 空回答不得落库（最重的一条断言，放最前：一旦落库就立刻失败，不被后续断言短路掉）。
	// saveAssistantMessage 由 emitDoneAndSave 在 goroutine 里执行，这里给一个观察窗口。
	select {
	case m := <-msgRepo.createdCh:
		t.Fatalf("空回答仍被落库: role=%s, content=%q；事件=%s", m.Role, m.Content, describeEvents(events))
	case <-time.After(500 * time.Millisecond):
	}

	if !gotStart {
		t.Fatalf("未收到 start 事件，链路没走到生成阶段；实际事件=%s", describeEvents(events))
	}
	if gotDone {
		t.Errorf("空回答被当成成功收尾（收到了 done 事件）；实际事件=%s", describeEvents(events))
	}
	if gotErr == nil {
		t.Fatalf("未收到 error 终态事件；实际事件=%s", describeEvents(events))
	}
	if !gotErr.Done || !gotErr.Retryable {
		t.Errorf("error 事件终态字段不对: Done=%v, Retryable=%v（期望 true/true）", gotErr.Done, gotErr.Retryable)
	}
	if !strings.Contains(gotErr.Title, "未返回内容") {
		t.Errorf("error 事件标题=%q，期望包含「未返回内容」", gotErr.Title)
	}
}

// TestProcessMessageGraphQuick_ClarifySendsClarifyWithoutStart
//
// 协议回归：澄清终态不该先给前端开一个回答气泡。
//
// 背景：旧实现里「要不要澄清」是图外判定的，所以 clarify 事件之前不可能有 start；
// 改写搬进图内之后，判定点挪到了 Invoke 之后 —— 如果 start 仍按老位置先发出去，
// 澄清请求就会先收到 start（前端据此记下 assistantId / 建回答块），再收到 clarify，
// 多出一个永远不会有内容的空回答块（rejectEmptyAnswer 整套逻辑就是为了不让这种块出现）。
//
// 这里断言：事件序列里既没有 start 也没有 done，只有一条 clarify；
// 且追问确实落了库（session 的 PendingClarify + 一条 assistant 追问消息）。
func TestProcessMessageGraphQuick_ClarifySendsClarifyWithoutStart(t *testing.T) {
	ensureTestConfig(t)

	const clarifyQ = "你是要导出哪些数据？是单个知识库还是全部知识库？"
	upstream := newClarifyUpstream(t,
		`{"rewritten":"","intent":"question","keywords":[],"need_clarify":true,"clarify_question":"`+clarifyQ+`"`+
			`,"clarify_options":["单个知识库","全部知识库"]}`)
	svc, msgRepo, sessRepo := newQuickTestService(t, upstream.URL)

	req := requestdto.SendMessageRequest{
		// 含指代表达（「那个方案」）→ 改写节点才会真的调 LLM，从而拿到 need_clarify=true
		Content:          "那个方案呢",
		KnowledgeBaseIDs: []string{"11111111-1111-1111-1111-111111111111"},
		SearchMode:       chatModeQuick,
		ModelID:          "cfg-1",
		ModelType:        "user",
	}
	eventCh := make(chan dto.StreamEvent, 100)

	svc.processMessageGraphQuick(context.Background(), "user-1", "session-1", "umsg-1", req, eventCh)

	var events []dto.StreamEvent
	var clarifySeen int
drain:
	for {
		select {
		case ev := <-eventCh:
			events = append(events, ev)
			switch ev.Type {
			case "start":
				t.Errorf("澄清请求不该收到 start 事件（前端会据此开一个空的回答气泡）；实际事件=%s", describeEvents(events))
			case "done":
				t.Errorf("澄清请求不该收到 done 事件；实际事件=%s", describeEvents(events))
			case "clarify":
				clarifySeen++
				if ev.Clarify == nil {
					t.Error("clarify 事件的载荷为空")
					continue
				}
				if ev.Clarify.Question != clarifyQ {
					t.Errorf("追问文本=%q，期望 %q", ev.Clarify.Question, clarifyQ)
				}
				if len(ev.Clarify.Options) != 2 {
					t.Errorf("追问选项=%v，期望 2 个", ev.Clarify.Options)
				}
				if !ev.Done {
					t.Error("clarify 应为终态事件（Done=true）")
				}
			}
		default:
			break drain
		}
	}

	if clarifySeen != 1 {
		t.Fatalf("clarify 事件收到 %d 条，期望恰好 1 条；实际事件=%s", clarifySeen, describeEvents(events))
	}

	if sessRepo.setCalls != 1 {
		t.Errorf("SetPendingClarify 调用 %d 次，期望 1", sessRepo.setCalls)
	}
	if !bytes.Contains(sessRepo.pendingData, []byte(clarifyQ)) {
		t.Errorf("PendingClarify 里没有追问文本: %s", sessRepo.pendingData)
	}

	// 追问要落一条 assistant 消息：让历史自然串成 [user问题 → assistant追问 → user回答]
	select {
	case m := <-msgRepo.createdCh:
		if m.Role != "assistant" || m.Content != clarifyQ {
			t.Errorf("落库的追问消息不对: role=%s content=%q", m.Role, m.Content)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("追问消息没有落库")
	}
}
