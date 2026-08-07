package service

import (
	"context"
	"fmt"
	"io"
	"testing"

	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	pkgLogger "solvify-agent/pkg/logger"
)

// mockChatModel eino BaseChatModel 的最小实现，用于测试
type mockChatModel struct {
	generateFn func(ctx context.Context, input []*schema.Message, opts ...einoModel.Option) (*schema.Message, error)
	streamFn   func(ctx context.Context, input []*schema.Message, opts ...einoModel.Option) (*schema.StreamReader[*schema.Message], error)
}

func (m *mockChatModel) Generate(ctx context.Context, input []*schema.Message, opts ...einoModel.Option) (*schema.Message, error) {
	if m.generateFn != nil {
		return m.generateFn(ctx, input, opts...)
	}
	return &schema.Message{Content: "mock response"}, nil
}

func (m *mockChatModel) Stream(ctx context.Context, input []*schema.Message, opts ...einoModel.Option) (*schema.StreamReader[*schema.Message], error) {
	if m.streamFn != nil {
		return m.streamFn(ctx, input, opts...)
	}
	return nil, io.EOF
}

func TestIsValidIntent(t *testing.T) {
	cases := []struct {
		name   string
		intent string
		want   bool
	}{
		{"greeting 合法", intentGreeting, true},
		{"chitchat 合法", intentChitchat, true},
		{"question 合法", intentQuestion, true},
		{"identity 合法", intentIdentity, true},
		{"meta 合法", intentMeta, true},
		{"空字符串非法", "", false},
		{"未知值非法", "tool_call", false},
		{"拼写错误非法", "questions", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isValidIntent(c.intent); got != c.want {
				t.Errorf("isValidIntent(%q) = %v, want %v", c.intent, got, c.want)
			}
		})
	}
}

func TestBuildRewriteHistory(t *testing.T) {
	// 构造 InputMsgs: [System, User1, Assistant1, User2, Assistant2, User3(current)]
	msgs := []*schema.Message{
		schema.SystemMessage("你是知识助理"),
		schema.UserMessage("Go 的接口怎么实现"),
		schema.AssistantMessage("接口用 interface 关键字定义...", nil),
		schema.UserMessage("错误处理有哪些方式"),
		schema.AssistantMessage("Go 用 error 返回值...", nil),
		schema.UserMessage("那 context 怎么用"), // UserQuestionIndex = 5
	}

	history := buildRewriteHistory(msgs, 5, 3)

	// 应排除 system 和当前 User3，只保留 User1/Assistant1/User2/Assistant2
	if history == "" {
		t.Fatal("history 为空，应该有内容")
	}
	if !containsAll(history, "Go 的接口怎么实现", "接口用 interface 关键字", "错误处理有哪些方式", "Go 用 error 返回值") {
		t.Errorf("history 未包含预期历史内容: %s", history)
	}
	if containsAll(history, "你是知识助理") {
		t.Error("history 不应该包含 system message")
	}
	if containsAll(history, "那 context 怎么用") {
		t.Error("history 不应该包含当前用户问题")
	}
}

func TestBuildRewriteHistory_EmptyMsgs(t *testing.T) {
	history := buildRewriteHistory(nil, 0, 3)
	if history != "" {
		t.Errorf("nil msgs 应返回空，got: %q", history)
	}
}

func TestBuildRewriteHistory_NoPreviousMessages(t *testing.T) {
	msgs := []*schema.Message{
		schema.SystemMessage("你好"),
		schema.UserMessage("这是第一个问题"),
	}
	history := buildRewriteHistory(msgs, 1, 3)
	if history != "" {
		t.Errorf("没有历史消息应返回空，got: %q", history)
	}
}

func TestBuildRewriteHistory_MaxRoundsLimit(t *testing.T) {
	// 构造 6 轮历史，maxRounds=2 → 应该只保留最后 2 轮
	msgs := []*schema.Message{
		schema.SystemMessage("sys"),
		schema.UserMessage("q1"), schema.AssistantMessage("a1", nil),
		schema.UserMessage("q2"), schema.AssistantMessage("a2", nil),
		schema.UserMessage("q3"), schema.AssistantMessage("a3", nil),
		schema.UserMessage("q4"), schema.AssistantMessage("a4", nil),
		schema.UserMessage("q5"), schema.AssistantMessage("a5", nil),
		schema.UserMessage("q6"), schema.AssistantMessage("a6", nil),
		schema.UserMessage("current"), // idx = 13
	}
	history := buildRewriteHistory(msgs, 13, 2)

	if containsAll(history, "q1", "a1") {
		t.Error("maxRounds=2 不应包含最早的 q1/a1")
	}
	if !containsAll(history, "q5", "a5", "q6", "a6") {
		t.Errorf("maxRounds=2 应保留最后两轮 q5/a5, q6/a6, got: %s", history)
	}
}

func containsAll(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if !containsStr(s, sub) {
			return false
		}
	}
	return true
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// --- doRewriteWithLLM 单元测试 ---

func init() {
	// 初始化默认 logger，避免 nil pointer
	_ = pkgLogger.InitDefault()
}

func TestDoRewriteWithLLM_Greeting(t *testing.T) {
	// mock ChatModel 返回 greeting JSON
	mock := &mockChatModel{
		generateFn: func(ctx context.Context, input []*schema.Message, opts ...einoModel.Option) (*schema.Message, error) {
			return &schema.Message{Content: `{"rewritten":"","intent":"greeting","keywords":[]}`}, nil
		},
	}
	ctx := withGraphChatModel(context.Background(), mock)
	input := &quickGraphInput{
		OriginalQuery: "你好",
		InputMsgs: []*schema.Message{
			schema.SystemMessage("你是知识助理"),
			schema.UserMessage("你好"),
		},
		UserQuestionIndex: 1,
		ModelName:         "test-model",
	}

	rewritten, intent, _, skipRetrieve := doRewriteWithLLM(ctx, input)

	if intent != intentGreeting {
		t.Errorf("intent = %q, want %q", intent, intentGreeting)
	}
	if !skipRetrieve {
		t.Error("greeting 场景 skipRetrieve 应为 true")
	}
	// 空 rewritten 会被 fallback 成原始 query（合理：改写不能是空的）
	if rewritten != "你好" {
		t.Errorf("greeting 场景 rewritten 应 fallback 为原始 query, got %q", rewritten)
	}
}

func TestDoRewriteWithLLM_QuestionWithHistory(t *testing.T) {
	// mock ChatModel 返回 question JSON，做指代消解
	mock := &mockChatModel{
		generateFn: func(ctx context.Context, input []*schema.Message, opts ...einoModel.Option) (*schema.Message, error) {
			// 验证输入里包含历史上下文
			if len(input) != 2 {
				t.Errorf("input msgs 应为 [system, user], got len=%d", len(input))
			}
			return &schema.Message{Content: `{"rewritten":"Go 中 interface 和 context 怎么配合使用","intent":"question","keywords":["Go","interface","context"]}`}, nil
		},
	}
	ctx := withGraphChatModel(context.Background(), mock)
	input := &quickGraphInput{
		OriginalQuery: "那 context 呢",
		InputMsgs: []*schema.Message{
			schema.SystemMessage("你是知识助理"),
			schema.UserMessage("Go 怎么实现接口"),
			schema.AssistantMessage("用 interface 关键字...", nil),
			schema.UserMessage("那 context 呢"),
		},
		UserQuestionIndex: 3,
		ModelName:         "test-model",
	}

	rewritten, intent, keywords, skipRetrieve := doRewriteWithLLM(ctx, input)

	if intent != intentQuestion {
		t.Errorf("intent = %q, want %q", intent, intentQuestion)
	}
	if skipRetrieve {
		t.Error("question 场景 skipRetrieve 应为 false")
	}
	if rewritten != "Go 中 interface 和 context 怎么配合使用" {
		t.Errorf("rewritten = %q, want 改写后的完整问题", rewritten)
	}
	if len(keywords) != 3 {
		t.Errorf("keywords len = %d, want 3", len(keywords))
	}
}

func TestDoRewriteWithLLM_Fallback_NoChatModel(t *testing.T) {
	// context 里没有 ChatModel → 应 fallback 原始 query
	input := &quickGraphInput{
		OriginalQuery: "那 context 呢",
		InputMsgs: []*schema.Message{
			schema.SystemMessage("你是知识助理"),
			schema.UserMessage("那 context 呢"),
		},
		UserQuestionIndex: 1,
		ModelName:         "test-model",
	}

	rewritten, intent, _, skipRetrieve := doRewriteWithLLM(context.Background(), input)

	if rewritten != input.OriginalQuery {
		t.Errorf("无 ChatModel 时 rewritten 应等于原始 query, got %q", rewritten)
	}
	if intent != intentQuestion {
		t.Errorf("无 ChatModel 时 intent 应为 question, got %q", intent)
	}
	if skipRetrieve {
		t.Error("无 ChatModel 时不应跳过检索")
	}
}

func TestDoRewriteWithLLM_Fallback_GenerateError(t *testing.T) {
	// ChatModel.Generate 报错 → 应 fallback
	mock := &mockChatModel{
		generateFn: func(ctx context.Context, input []*schema.Message, opts ...einoModel.Option) (*schema.Message, error) {
			return nil, fmt.Errorf("LLM timeout")
		},
	}
	ctx := withGraphChatModel(context.Background(), mock)
	input := &quickGraphInput{
		OriginalQuery: "测试问题",
		InputMsgs: []*schema.Message{
			schema.SystemMessage("你是知识助理"),
			schema.UserMessage("测试问题"),
		},
		UserQuestionIndex: 1,
		ModelName:         "test-model",
	}

	rewritten, intent, _, skipRetrieve := doRewriteWithLLM(ctx, input)

	if rewritten != input.OriginalQuery {
		t.Errorf("Generate 报错时应 fallback 原始 query, got %q", rewritten)
	}
	if intent != intentQuestion {
		t.Errorf("Generate 报错时 intent 应为 question, got %q", intent)
	}
	if skipRetrieve {
		t.Error("Generate 报错时不应跳过检索")
	}
}

func TestDoRewriteWithLLM_Fallback_JSONParseError(t *testing.T) {
	// LLM 返回无效 JSON → 应 fallback
	mock := &mockChatModel{
		generateFn: func(ctx context.Context, input []*schema.Message, opts ...einoModel.Option) (*schema.Message, error) {
			return &schema.Message{Content: "不是有效的 JSON"}, nil
		},
	}
	ctx := withGraphChatModel(context.Background(), mock)
	input := &quickGraphInput{
		OriginalQuery: "测试问题",
		InputMsgs: []*schema.Message{
			schema.SystemMessage("你是知识助理"),
			schema.UserMessage("测试问题"),
		},
		UserQuestionIndex: 1,
		ModelName:         "test-model",
	}

	rewritten, intent, _, _ := doRewriteWithLLM(ctx, input)

	if rewritten != input.OriginalQuery {
		t.Errorf("JSON 解析失败时应 fallback 原始 query, got %q", rewritten)
	}
	if intent != intentQuestion {
		t.Errorf("JSON 解析失败时 intent 应为 question, got %q", intent)
	}
}

func TestDoRewriteWithLLM_Chitchat(t *testing.T) {
	mock := &mockChatModel{
		generateFn: func(ctx context.Context, input []*schema.Message, opts ...einoModel.Option) (*schema.Message, error) {
			return &schema.Message{Content: `{"rewritten":"","intent":"chitchat","keywords":[]}`}, nil
		},
	}
	ctx := withGraphChatModel(context.Background(), mock)
	input := &quickGraphInput{
		OriginalQuery: "讲个笑话",
		InputMsgs: []*schema.Message{
			schema.SystemMessage("你是知识助理"),
			schema.UserMessage("讲个笑话"),
		},
		UserQuestionIndex: 1,
		ModelName:         "test-model",
	}

	_, intent, _, skipRetrieve := doRewriteWithLLM(ctx, input)

	if intent != intentChitchat {
		t.Errorf("intent = %q, want %q", intent, intentChitchat)
	}
	if !skipRetrieve {
		t.Error("chitchat 场景 skipRetrieve 应为 true")
	}
}

// --- QuickAnswerGraph 集成测试 ---

// 注：完整 QuickAnswerGraph 端到端测试需要 EinoRetrieverAdapter + 真实 Retriever + Runnable.Compile/Invoke 流程，
// 通过 HTTP API 层测试更合适。这里 doRewriteWithLLM 单元测试已覆盖 Rewrite 节点核心逻辑。
// 完整链路：HTTP /metrics 端点已在运行时验证（greeting → skip_retrieve=true、question → rewrite → retrieve → generate）
