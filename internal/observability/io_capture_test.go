package observability

// 本文件锁定「组件输入输出 / 模型输入输出」这条链路的可见性。
// 对应问题文档：docs/可观测性问题记录-输入输出不可见-20260923.md 的 P0-1 / P0-2 / P1-4 / P2-5。
//
// 为什么这几条合在一起写：它们是同一个失效模式 —— span 采到了，但内容为空，且没有任何报错。
// 分开写会让人以为「token 用量有了就算通了」，从而漏掉内容这一层。

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// TestStreamChatCapturesOutputAndTTFT 锁定流式 ChatModel 的两项「结束态」内容属性：
// 回复正文（P0-1）与首字延迟（P2-5）。
//
// 回归背景：流式路径（快速模式、深度模式的主链路）原本只聚合 token 用量与 finish_reason，
// 不聚合正文 —— 于是本地 span_tree 和三方平台上，模型输出整段不可见，只剩 token 数；
// 首字延迟则完全没有出口，而它是流式应用最关键、且唯一无法从总耗时反推的体验指标。
//
// 为什么用 schema.Pipe 手工喂流、而不是 StreamReaderFromArray：后者所有 chunk 同刻到达，
// 「首字延迟」会退化成 0，测试就分不清「量的是首字」还是「量的是整条流」。
// 这里在首字前后各插一段真实延时，让 ttft 与 duration 成为两个可区分的量。
func TestStreamChatCapturesOutputAndTTFT(t *testing.T) {
	rec := newCaptureRecorder(t)
	RegisterGenAIProvider("deepseek-v4-flash", "deepseek")

	const (
		beforeFirstChunk = 50 * time.Millisecond
		afterFirstChunk  = 50 * time.Millisecond
	)

	info := &callbacks.RunInfo{
		Name:      "ChatModelStream",
		Type:      "OpenAI",
		Component: components.ComponentOfChatModel,
	}
	input := &model.CallbackInput{
		Messages: []*schema.Message{{Role: schema.User, Content: "什么是 Go"}},
		Config:   &model.Config{Model: "deepseek-v4-flash"},
	}
	ctx, span := startEinoSpan(t, rec, info, input)

	sr, sw := schema.Pipe[callbacks.CallbackOutput](4)
	go func() {
		defer sw.Close()
		time.Sleep(beforeFirstChunk)
		sw.Send(&model.CallbackOutput{
			Message: &schema.Message{Role: schema.Assistant, Content: "Go 是"},
			Config:  &model.Config{Model: "deepseek-v4-flash"},
		}, nil)
		time.Sleep(afterFirstChunk)
		sw.Send(&model.CallbackOutput{
			Message: &schema.Message{Role: schema.Assistant, Content: "编译型语言"},
			Config:  &model.Config{Model: "deepseek-v4-flash"},
		}, nil)
		// 真实形态：用量与 finish_reason 出现在收尾 chunk 上，且这两个 chunk 常常没有正文
		sw.Send(&model.CallbackOutput{
			Config:     &model.Config{Model: "deepseek-v4-flash"},
			TokenUsage: &model.TokenUsage{PromptTokens: 8, CompletionTokens: 9, TotalTokens: 17},
		}, nil)
		sw.Send(&model.CallbackOutput{
			Message: &schema.Message{Role: schema.Assistant, ResponseMeta: &schema.ResponseMeta{FinishReason: "stop"}},
			Config:  &model.Config{Model: "deepseek-v4-flash"},
		}, nil)
	}()

	NewEinoCallbackHandler(rec).OnEndWithStreamOutput(ctx, info, sr)

	ended := waitSpan(t, rec)
	if ended != span {
		t.Fatalf("结束的 span 不是 OnStart 创建的那个：%p vs %p", ended, span)
	}

	// ---- P0-1：回复正文必须完整可见 ----
	const wantReply = "Go 是编译型语言"
	if got, _ := ended.Attrs["reply_preview"].(string); !strings.Contains(got, wantReply) {
		t.Errorf("reply_preview = %q，期望包含 %q", got, wantReply)
	}
	rawOut, _ := ended.Attrs[AttrGenAIOutputMessages].(string)
	if rawOut == "" {
		t.Fatalf("%s 缺失：三方平台的 Generation 卡片拿不到模型输出", AttrGenAIOutputMessages)
	}
	var outMsgs []struct {
		Role  string `json:"role"`
		Parts []struct {
			Type    string `json:"type"`
			Content string `json:"content"`
		} `json:"parts"`
	}
	if err := json.Unmarshal([]byte(rawOut), &outMsgs); err != nil {
		t.Fatalf("%s 不是合法 JSON：%v（原值 %q）", AttrGenAIOutputMessages, err, rawOut)
	}
	if len(outMsgs) != 1 || outMsgs[0].Role != "assistant" {
		t.Fatalf("%s 结构不对：%+v", AttrGenAIOutputMessages, outMsgs)
	}
	if len(outMsgs[0].Parts) != 1 || outMsgs[0].Parts[0].Content != wantReply {
		t.Errorf("%s 的正文 = %+v，期望单个 text part 且内容为 %q", AttrGenAIOutputMessages, outMsgs[0].Parts, wantReply)
	}

	// ---- P2-5：首字延迟 ----
	ttftMs := attrInt(t, ended.Attrs, "ttft_ms")
	if ttftMs < 30 || ttftMs > 80 {
		t.Errorf("ttft_ms = %d，期望落在 [30,80]：首字之后又等了 50ms，若取的是整条流耗时会超上限", ttftMs)
	}
	durMs := attrInt(t, ended.Attrs, "duration_ms")
	if durMs-ttftMs < 20 {
		t.Errorf("duration_ms(%d) - ttft_ms(%d) = %d，余量太小：首字延迟很可能不是「首字」那一刻", durMs, ttftMs, durMs-ttftMs)
	}
	ttftSec, ok := ended.Attrs[AttrGenAIServerTimeToFirstToken].(float64)
	if !ok {
		t.Fatalf("%s 缺失或类型不是 float64（实际 %T）", AttrGenAIServerTimeToFirstToken, ended.Attrs[AttrGenAIServerTimeToFirstToken])
	}
	if ttftSec <= 0 {
		t.Errorf("%s = %v，期望 > 0", AttrGenAIServerTimeToFirstToken, ttftSec)
	}
	if diff := math.Abs(ttftSec*1000 - float64(ttftMs)); diff > 1 {
		t.Errorf("%s(%v s) 与 ttft_ms(%d) 不一致：两个口径必须来自同一次测量", AttrGenAIServerTimeToFirstToken, ttftSec, ttftMs)
	}
}

// TestChatStartCapturesFullInputMessages 锁定模型输入的完整可见性（P0-2 + P1-3 的模型侧）。
//
// 回归背景：输入侧原本只有 system_prompt_preview / last_user_msg_preview 两个短预览，
// assistant 的 tool_call 与 tool 的返回完全不进 attrs ——
// 于是在 ReAct 里「模型为什么这么答」最需要看的那部分恰好看不到。
func TestChatStartCapturesFullInputMessages(t *testing.T) {
	rec := newCaptureRecorder(t)
	RegisterGenAIProvider("deepseek-v4-flash", "deepseek")

	// 720 rune：远超旧的 ContentMaxChars(200)，用来证明截断不再发生在「落 attrs」这一步
	toolResult := strings.Repeat("检索片段内容。", 120)

	tools := make([]*schema.ToolInfo, 0, 21)
	for i := 0; i < 21; i++ {
		tools = append(tools, &schema.ToolInfo{Name: fmt.Sprintf("knowledge_tool_%02d", i)})
	}

	info := &callbacks.RunInfo{Name: "ChatModelGenerate", Type: "OpenAI", Component: components.ComponentOfChatModel}
	input := &model.CallbackInput{
		Messages: []*schema.Message{
			{Role: schema.System, Content: "你是知识助理"},
			{Role: schema.User, Content: "什么是 Go"},
			{Role: schema.Assistant, ToolCalls: []schema.ToolCall{{
				ID:       "call_1",
				Type:     "function",
				Function: schema.FunctionCall{Name: "knowledge_search", Arguments: `{"query":"Go"}`},
			}}},
			{Role: schema.Tool, ToolCallID: "call_1", Content: toolResult},
		},
		Tools:  tools,
		Config: &model.Config{Model: "deepseek-v4-flash"},
	}
	_, span := startEinoSpan(t, rec, info, input)

	raw, _ := span.Attrs[AttrGenAIInputMessages].(string)
	if raw == "" {
		t.Fatalf("%s 缺失：三方平台的 Generation 卡片 Input 是空的", AttrGenAIInputMessages)
	}
	var msgs []struct {
		Role  string `json:"role"`
		Parts []struct {
			Type      string          `json:"type"`
			Content   string          `json:"content"`
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"parts"`
	}
	if err := json.Unmarshal([]byte(raw), &msgs); err != nil {
		t.Fatalf("%s 不是合法 JSON：%v（原值 %q）", AttrGenAIInputMessages, err, raw)
	}
	wantRoles := []string{"system", "user", "assistant", "tool"}
	if len(msgs) != len(wantRoles) {
		t.Fatalf("%s 里 messages 条数 = %d，期望 %d（%v）", AttrGenAIInputMessages, len(msgs), len(wantRoles), wantRoles)
	}
	for i, want := range wantRoles {
		if msgs[i].Role != want {
			t.Errorf("第 %d 条 role = %q，期望 %q", i, msgs[i].Role, want)
		}
	}
	// 工具返回必须原样可见：模型输入里占最大体积的就是它，ReAct 排查的核心依据
	if parts := msgs[3].Parts; len(parts) != 1 || utf8.RuneCountInString(parts[0].Content) < utf8.RuneCountInString(toolResult) {
		t.Errorf("tool 消息内容被截断：期望 %d rune，实际 %+v", utf8.RuneCountInString(toolResult), parts)
	}
	// assistant 的 tool_call 必须带上
	if len(msgs[2].Parts) != 1 {
		t.Fatalf("assistant 消息的 parts = %+v，期望 1 个 tool_call part", msgs[2].Parts)
	}
	if p := msgs[2].Parts[0]; p.Type != "tool_call" || p.Name != "knowledge_search" || string(p.Arguments) != `{"query":"Go"}` {
		t.Errorf("assistant 的 tool_call 不对：%+v", p)
	}
	// 工具清单不许折叠成 "(+N more)"：折叠会让 21 个里的 16 个不可见
	list, _ := span.Attrs["tools_list"].(string)
	if strings.Contains(list, "more") {
		t.Errorf("tools_list 被折叠了：%q", list)
	}
	for i := 0; i < 21; i++ {
		if want := fmt.Sprintf("knowledge_tool_%02d", i); !strings.Contains(list, want) {
			t.Errorf("tools_list 缺少 %s：%q", want, list)
		}
	}
}

// TestTruncatePreviewIsTheOnlyLengthSource 锁定内容字段的长度只有一个来源（P1-4）。
//
// 回归背景：TruncatePreview 原本内部先调 SanitizeString，而后者会按 ContentMaxChars 再截一次，
// 于是
//  1. 调用点声明的 maxRunes（500 / 2000 / 4000 / 12000）全部失效，实际恒为 200；
//  2. "…(+X chars)" 尾标分支永不可达，前端无法判断内容到底被砍了多少。
//
// 断言的重点不是「截到几个字」，而是「maxRunes 是唯一决定长度的量」——
// 所以 ContentMaxChars 故意配成 200 这个远小于 maxRunes 的值，好让二次截断立刻暴露。
func TestTruncatePreviewIsTheOnlyLengthSource(t *testing.T) {
	const contentMaxChars = 200
	s := NewPIISanitizer(contentMaxChars, true)

	long := strings.Repeat("字", 500)

	// 1) 正文必须留够 maxRunes 个 rune，不被 ContentMaxChars 二次截断
	got := s.TruncatePreview(long, 300)
	if n := strings.Count(got, "字"); n != 300 {
		t.Errorf("TruncatePreview(500字, 300) 正文 = %d rune，期望 300（%d 是兜底值，不该管内容字段）", n, contentMaxChars)
	}
	// 2) 截断了就必须给出「砍掉多少」的尾标
	if !strings.HasSuffix(got, "…(+200 chars)") {
		t.Errorf("TruncatePreview(500字, 300) 尾标缺失或数量不对，实际 %q", got)
	}
	// 3) maxRunes 大于 ContentMaxChars 时同样不被二次截断
	if got := s.TruncatePreview(long, 500); got != long {
		t.Errorf("TruncatePreview(500字, 500) 被改动，实际 %d rune", utf8.RuneCountInString(got))
	}
	// 4) 恰好等长时原样返回，不加尾标
	exact := strings.Repeat("字", 300)
	if got := s.TruncatePreview(exact, 300); got != exact {
		t.Errorf("恰好等长时内容不应改动，实际 %q", got)
	}
	// 5) maxRunes <= 0 必须退回 contentLenShort 兜底，不能变成「不截断」
	gotShort := s.TruncatePreview(strings.Repeat("字", 600), 0)
	if n := strings.Count(gotShort, "字"); n != contentLenShort {
		t.Errorf("maxRunes=0 未按 contentLenShort(%d) 兜底，正文 %d rune", contentLenShort, n)
	}
	if !strings.HasSuffix(gotShort, "…(+100 chars)") {
		t.Errorf("maxRunes=0 兜底后尾标不对：%q", gotShort)
	}
	// 6) 长度控制不能把 PII 脱敏挤掉
	if got := s.TruncatePreview("联系方式 13812345678", 500); got != "联系方式 138****5678" {
		t.Errorf("TruncatePreview 的 PII mask 没生效：%q", got)
	}
}

// TestSanitizeAttrsDoesNotClampContentAttrs 锁定「落库 / 三方导出出口不把内容字段截回兜底值」（P1-4 的另一半）。
//
// 回归背景：SanitizeAttrs 是 cleanSpan（落库）与 StartSpan/EndSpan（三方导出）的共同出口，
// 原本对所有字符串一律按 ContentMaxChars 截断 —— 产生侧辛苦放宽到 4000 / 12000 的内容，
// 到这里被无声截回 200，span_tree 与三方平台上看到的仍是残片。
func TestSanitizeAttrsDoesNotClampContentAttrs(t *testing.T) {
	s := NewPIISanitizer(200, true)
	long := strings.Repeat("字", 1500)

	out := s.SanitizeAttrs(Attrs{
		"reply_preview":            long,
		AttrGenAIInputMessages:     long,
		AttrGenAIOutputMessages:    long,
		AttrGenAIToolCallResult:    long,
		AttrGenAIToolCallArguments: long,
		"error":                    long, // 非内容字段：仍按 ContentMaxChars 兜底
	})

	contentKeys := []string{
		"reply_preview",
		AttrGenAIInputMessages,
		AttrGenAIOutputMessages,
		AttrGenAIToolCallResult,
		AttrGenAIToolCallArguments,
	}
	for _, key := range contentKeys {
		got, _ := out[key].(string)
		if n := strings.Count(got, "字"); n != 1500 {
			t.Errorf("内容字段 %s 被截断：正文 %d rune，期望 1500", key, n)
		}
	}
	// 兜底规则仍然生效：非内容字段按 ContentMaxChars 截断（+1 是省略号）
	if got, _ := out["error"].(string); utf8.RuneCountInString(got) != 201 {
		t.Errorf("非内容字段 error 应截到 201 rune，实际 %d", utf8.RuneCountInString(got))
	}
}

// attrInt 从 attrs 里取一个整数值。
//
// 项目内 attrs 的值是 any，数字型字段既有 string（duration_ms / ttft_ms 的历史写法）
// 也有 int，测试里统一收口，避免每处断言各写一遍类型分支。
func attrInt(t *testing.T, attrs Attrs, key string) int {
	t.Helper()
	switch v := attrs[key].(type) {
	case string:
		n, err := strconv.Atoi(v)
		if err != nil {
			t.Fatalf("attrs[%s] = %q 不是整数", key, v)
		}
		return n
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	default:
		t.Fatalf("attrs[%s] 缺失或类型不支持：%T", key, attrs[key])
		return 0
	}
}
