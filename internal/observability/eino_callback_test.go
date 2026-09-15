package observability

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"go.opentelemetry.io/otel/attribute"
)

// spanCaptureRecorder 包一层真实 Recorder，在 EndSpan 时把 span 投进 channel。
//
// 为什么要它：einoOnStreamEnd 是在 goroutine 里异步 EndSpan 的，
// 直接轮询 state.span.Attrs 会读到未写完的数据（-race 下是 data race）。
// 用 channel 做同步点，既能等到结束、又保证 happens-before。
type spanCaptureRecorder struct {
	Recorder
	ended chan *Span
}

func (r *spanCaptureRecorder) WithTraceRoot(ctx context.Context, attrs TraceRootAttrs) context.Context {
	// 让 ctx 里绑定的 recorder 是包装后的自己，einoOnStreamEnd 会通过 RecorderFromContext 取它
	return context.WithValue(ctx, recorderKey, Recorder(r))
}

func (r *spanCaptureRecorder) EndSpan(ctx context.Context, span *Span, status SpanStatus, err error, attrs Attrs) {
	r.Recorder.EndSpan(ctx, span, status, err, attrs)
	if span != nil {
		select {
		case r.ended <- span:
		default:
		}
	}
}

func newCaptureRecorder(t *testing.T) *spanCaptureRecorder {
	t.Helper()
	return &spanCaptureRecorder{Recorder: newTestRecorder(t), ended: make(chan *Span, 4)}
}

func waitSpan(t *testing.T, rec *spanCaptureRecorder) *Span {
	t.Helper()
	select {
	case s := <-rec.ended:
		return s
	case <-time.After(3 * time.Second):
		t.Fatal("等待 EndSpan 超时：流式路径没有结束 span")
		return nil
	}
}

// startEinoSpan 走一遍 einoOnStart，返回承载 span 引用的 ctx 与 span 本身。
func startEinoSpan(t *testing.T, rec Recorder, info *callbacks.RunInfo, input callbacks.CallbackInput) (context.Context, *Span) {
	t.Helper()
	ctx := rec.WithTraceRoot(context.Background(), TraceRootAttrs{UserID: "u1", SessionID: "s1"})
	ctx = NewEinoCallbackHandler(rec).OnStart(ctx, info, input)
	state, ok := ctx.Value(einoSpanKey{}).(*einoSpanState)
	if !ok || state == nil || state.span == nil {
		t.Fatal("OnStart 之后 ctx 里没有 einoSpanState/span")
	}
	return ctx, state.span
}

// TestGenAIStreamChatAttrs 锁定流式 ChatModel 的 gen_ai 语义约定属性。
//
// 背景：流式路径 eino 只触发 OnEndWithStreamOutput、不触发 OnEnd，
// 所以 token 用量、响应模型、finish_reason 必须读完流之后自己聚合。
// 没有这段聚合，三方追踪平台上主链路（快速/深度模式都是流式）的 LLM 卡片
// 就只有 span 没有模型、没有 token 成本。
func TestGenAIStreamChatAttrs(t *testing.T) {
	rec := newCaptureRecorder(t)
	RegisterGenAIProvider("deepseek-v4-flash", "deepseek")

	info := &callbacks.RunInfo{
		Name:      "ChatModelGenerate",
		Type:      "OpenAI",
		Component: components.ComponentOfChatModel,
	}
	input := &model.CallbackInput{
		Messages: []*schema.Message{
			{Role: schema.System, Content: "你是知识助理"},
			{Role: schema.User, Content: "什么是 Go"},
		},
		Config: &model.Config{
			Model:       "deepseek-v4-flash",
			Temperature: 0.3,
			MaxTokens:   1024,
			TopP:        0.9,
			Stop:        []string{"END"},
		},
	}
	ctx, span := startEinoSpan(t, rec, info, input)

	// ---- OnStart 阶段：身份 + 请求参数 ----
	// temperature / top_p 在 eino 的 model.Config 里是 float32，这里用 float32 断言，
	// 写进 OTel 时的类型由 TestAttrsToOTelHandlesFloat32 兜住。
	wantAttrs := map[string]any{
		AttrGenAIOperationName:      "chat",
		AttrGenAIProviderName:       "deepseek",
		AttrGenAIRequestModel:       "deepseek-v4-flash",
		AttrGenAIRequestTemperature: float32(0.3),
		AttrGenAIRequestMaxTokens:   1024,
		AttrGenAIRequestTopP:        float32(0.9),
	}
	for k, want := range wantAttrs {
		if got := span.Attrs[k]; got != want {
			t.Errorf("span.Attrs[%s] = %v(%T)，期望 %v", k, got, got, want)
		}
	}
	if got, ok := span.Attrs[AttrGenAIRequestStopSequences].([]string); !ok || len(got) != 1 || got[0] != "END" {
		t.Errorf("span.Attrs[%s] = %v，期望 []string{\"END\"}", AttrGenAIRequestStopSequences, span.Attrs[AttrGenAIRequestStopSequences])
	}

	// ---- 流式结束阶段：用量从 chunk 里聚合 ----
	// 按真实形态造 chunk：内容 chunk、只有 usage 没有 Message 的收尾 chunk、带 finish_reason 的 chunk
	chunks := []callbacks.CallbackOutput{
		&model.CallbackOutput{
			Message: &schema.Message{Role: schema.Assistant, Content: "Go 是"},
			Config:  &model.Config{Model: "deepseek-v4-flash"},
		},
		&model.CallbackOutput{
			Message: &schema.Message{Role: schema.Assistant, Content: "编译型语言"},
			Config:  &model.Config{Model: "deepseek-v4-flash"},
		},
		&model.CallbackOutput{
			Config: &model.Config{Model: "deepseek-v4-flash"},
			TokenUsage: &model.TokenUsage{
				PromptTokens:            12,
				CompletionTokens:        34,
				TotalTokens:             46,
				PromptTokenDetails:      model.PromptTokenDetails{CachedTokens: 5},
				CompletionTokensDetails: model.CompletionTokensDetails{ReasoningTokens: 7},
			},
			Message: nil,
		},
		&model.CallbackOutput{
			Message: &schema.Message{
				Role:         schema.Assistant,
				ResponseMeta: &schema.ResponseMeta{FinishReason: "stop"},
			},
			Config: &model.Config{Model: "deepseek-v4-flash"},
		},
	}
	NewEinoCallbackHandler(rec).OnEndWithStreamOutput(ctx, info, schema.StreamReaderFromArray(chunks))

	ended := waitSpan(t, rec)
	if ended != span {
		t.Fatalf("结束的 span 不是 OnStart 创建的那个：%p vs %p", ended, span)
	}
	if ended.Status != SpanStatusOK {
		t.Errorf("span.Status = %s，期望 ok", ended.Status)
	}

	wantEndAttrs := map[string]any{
		AttrGenAIOperationName:        "chat",
		AttrGenAIRequestStream:        true,
		AttrGenAIResponseModel:        "deepseek-v4-flash",
		AttrGenAIUsageInputTokens:     12,
		AttrGenAIUsageOutputTokens:    34,
		AttrGenAIUsageCacheReadTokens: 5,
		AttrGenAIUsageReasoningTokens: 7,
		"prompt_tokens":               12,
		"completion_tokens":           34,
		"total_tokens":                46,
		"cached_tokens":               5,
		"model_id":                    "deepseek-v4-flash",
	}
	for k, want := range wantEndAttrs {
		if got := ended.Attrs[k]; got != want {
			t.Errorf("结束后 span.Attrs[%s] = %v(%T)，期望 %v", k, got, got, want)
		}
	}
	if got, ok := ended.Attrs[AttrGenAIResponseFinishReasons].([]string); !ok || len(got) != 1 || got[0] != "stop" {
		t.Errorf("span.Attrs[%s] = %v，期望 []string{\"stop\"}", AttrGenAIResponseFinishReasons, ended.Attrs[AttrGenAIResponseFinishReasons])
	}
}

// TestGenAIToolRetrieverEmbeddingAttrs 锁定工具 / 检索 / Embedding 的 gen_ai 属性。
// 这三类 span 靠 operation.name + 各自的身份属性（tool.name / query.text / 模型名）
// 才能被三方平台渲染成对应的卡片。
func TestGenAIToolRetrieverEmbeddingAttrs(t *testing.T) {
	rec := newCaptureRecorder(t)
	RegisterGenAIProvider("text-embedding-v4", "openai")

	cases := []struct {
		name  string
		info  *callbacks.RunInfo
		input callbacks.CallbackInput
		want  map[string]any
	}{
		{
			name: "tool",
			info: &callbacks.RunInfo{Name: "knowledge_search", Type: "OpenAITool", Component: components.ComponentOfTool},
			input: &tool.CallbackInput{
				ArgumentsInJSON: `{"query":"go 语言"}`,
			},
			want: map[string]any{
				AttrGenAIOperationName:     "execute_tool",
				AttrGenAIToolName:          "knowledge_search",
				AttrGenAIToolType:          "function",
				AttrGenAIToolCallArguments: `{"query":"go 语言"}`,
			},
		},
		{
			name: "retriever",
			info: &callbacks.RunInfo{Name: "retrieve", Type: "EinoRetrieverAdapter", Component: components.ComponentOfRetriever},
			input: &retriever.CallbackInput{
				Query: "什么是 Go",
				TopK:  3,
			},
			want: map[string]any{
				AttrGenAIOperationName:      "retrieval",
				AttrGenAIRetrievalQueryText: "什么是 Go",
			},
		},
		{
			name: "embedding",
			info: &callbacks.RunInfo{Name: "embed", Type: "OpenAI", Component: components.ComponentOfEmbedding},
			input: &embedding.CallbackInput{
				Texts:  []string{"什么是 Go"},
				Config: &embedding.Config{Model: "text-embedding-v4"},
			},
			want: map[string]any{
				AttrGenAIOperationName: "embeddings",
				AttrGenAIRequestModel:  "text-embedding-v4",
				AttrGenAIProviderName:  "openai",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, span := startEinoSpan(t, rec, tc.info, tc.input)
			for k, want := range tc.want {
				got := span.Attrs[k]
				if s, ok := want.(string); ok {
					// 字符串类属性统一走 PII mask + 截断，做包含匹配即可
					gs, _ := got.(string)
					if !strings.Contains(gs, s) {
						t.Errorf("span.Attrs[%s] = %q，期望包含 %q", k, gs, s)
					}
					continue
				}
				if got != want {
					t.Errorf("span.Attrs[%s] = %v(%T)，期望 %v", k, got, got, want)
				}
			}
		})
	}
}

// TestAttrsToOTelHandlesFloat32 锁定 float32 属性到 OTel 的类型映射。
//
// 回归背景：eino 的 model.Config.Temperature / TopP 是 float32，toKeyValue 原本没有
// float32 分支，会落到字符串兜底 —— gen_ai.request.temperature 于是以 "0.3" 这样的
// 字符串写进 OTel，三方追踪平台的数值解析/图表会直接失败。
func TestAttrsToOTelHandlesFloat32(t *testing.T) {
	kvs := attrsToOTel(Attrs{
		AttrGenAIRequestTemperature: float32(0.7),
		AttrGenAIRequestTopP:        float64(0.9),
		AttrGenAIRequestMaxTokens:   1024,
	})
	got := make(map[string]attribute.Value, len(kvs))
	for _, kv := range kvs {
		got[string(kv.Key)] = kv.Value
	}
	for key, want := range map[string]float64{
		AttrGenAIRequestTemperature: 0.7,
		AttrGenAIRequestTopP:        0.9,
	} {
		v, ok := got[key]
		if !ok {
			t.Fatalf("OTel 属性里缺少 %s", key)
		}
		if v.Type() != attribute.FLOAT64 {
			t.Errorf("%s 的类型是 %s，期望 FLOAT64", key, v.Type())
		}
		// 源头是 eino 的 float32（0.7 → 0.699999988079071），只能按容差比
		if diff := math.Abs(v.AsFloat64() - want); diff > 1e-6 {
			t.Errorf("%s = %v，期望约 %v（差 %v）", key, v.AsFloat64(), want, diff)
		}
	}
	if v := got[AttrGenAIRequestMaxTokens]; v.Type() != attribute.INT64 || v.AsInt64() != 1024 {
		t.Errorf("%s 的类型是 %s 值 %v，期望 INT64/1024", AttrGenAIRequestMaxTokens, v.Type(), v.AsInt64())
	}
}

// TestGenAIProviderRegistry 锁定 modelID → 供应商的登记/反查行为。
func TestGenAIProviderRegistry(t *testing.T) {
	if got := genAIProviderFor(""); got != "" {
		t.Errorf("空 modelID 应返回空字符串，实际 %q", got)
	}
	if got := genAIProviderFor("未登记过的模型"); got != "" {
		t.Errorf("未登记模型应返回空字符串，实际 %q", got)
	}
	RegisterGenAIProvider("m1", "zhipu")
	if got := genAIProviderFor("m1"); got != "zhipu" {
		t.Errorf("genAIProviderFor(m1) = %q，期望 zhipu", got)
	}
	// 重复登记以最后一次为准（用户换供应商的场景）
	RegisterGenAIProvider("m1", "deepseek")
	if got := genAIProviderFor("m1"); got != "deepseek" {
		t.Errorf("覆盖登记后 genAIProviderFor(m1) = %q，期望 deepseek", got)
	}
	// 空 provider 不应覆盖已有值
	RegisterGenAIProvider("m1", "")
	if got := genAIProviderFor("m1"); got != "deepseek" {
		t.Errorf("空 provider 不应覆盖，genAIProviderFor(m1) = %q，期望 deepseek", got)
	}
}
