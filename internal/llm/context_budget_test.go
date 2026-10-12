package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	einoOpenai "github.com/cloudwego/eino-ext/components/model/openai"
	aclOpenai "github.com/cloudwego/eino-ext/libs/acl/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/pkg/config"
	apperrors "solvify-agent/pkg/errors"
)

func TestContextBudgetHardAndSoft(t *testing.T) {
	p := config.DefaultContextPolicy()
	b, err := ResolveContextBudget("test", "unknown", 8192, 4096, true, p)
	if err != nil {
		t.Fatal(err)
	}
	if b.Check(b.Hard) != nil || !errors.Is(b.Check(b.Hard+1), ErrContextBudget) || b.Soft >= b.Hard || b.Target >= b.Soft {
		t.Fatal("软硬预算错误")
	}
	if _, err = ResolveContextBudget("test", "unknown", 0, 4096, false, p); !errors.Is(err, ErrContextConfig) {
		t.Fatal("未知窗口应拒绝")
	}
	if _, err = ResolveContextBudget("test", "gpt-4o", 0, 4096, false, p); err != nil {
		t.Fatal("注册窗口不可解析", err)
	}
	if apperrors.Classify(b.Check(b.Hard+1)) != apperrors.KindContextBudget {
		t.Fatal("本地拒绝不得误判供应商超窗")
	}
}

func TestContextModelChecksActualPayloadBeforeHTTP(t *testing.T) {
	var requests atomic.Int32
	var actualOutput atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		var body struct {
			MaxCompletionTokens int  `json:"max_completion_tokens"`
			Stream              bool `json:"stream"`
			MaxTokens           int  `json:"max_tokens"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		actualOutput.Store(int32(body.MaxCompletionTokens))
		if body.MaxTokens != 0 {
			t.Error("不应同时发送两类输出上限")
		}
		if body.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"id\":\"test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"正常\"},\"finish_reason\":null}]}\n\ndata: [DONE]\n\n"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"test","object":"chat.completion","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"正常"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	client, err := NewOpenAIClient(context.Background(), OpenAIClientConfig{Model: "gpt-4o", APIKey: "test", BaseURL: server.URL + "/v1", MaxContextLength: 8192})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		opts   []model.Option
		msgs   []*schema.Message
		reject bool
	}{
		{"长问题", nil, []*schema.Message{schema.UserMessage(strings.Repeat("问题", 6000))}, true},
		{"工具参数", nil, []*schema.Message{schema.UserMessage("问"), schema.AssistantMessage("", []schema.ToolCall{{ID: "call", Function: schema.FunctionCall{Name: "search", Arguments: strings.Repeat("参数", 6000)}}})}, true},
		{"未知模型", []model.Option{model.WithModel("unknown-model")}, []*schema.Message{schema.UserMessage("问")}, true},
		{"长指令", nil, []*schema.Message{schema.SystemMessage(strings.Repeat("系统指令", 6000)), schema.UserMessage("问")}, true},
		{"扩展输出", []model.Option{einoOpenai.WithMaxCompletionTokens(9000)}, []*schema.Message{schema.UserMessage("问")}, true},
		{"通用输出", []model.Option{model.WithMaxTokens(9000)}, []*schema.Message{schema.UserMessage("问")}, true},
		{"额外字段输出", []model.Option{aclOpenai.WithExtraFields(map[string]any{"max_completion_tokens": 9000})}, []*schema.Message{schema.UserMessage("问")}, true},
		{"调用工具", []model.Option{model.WithTools([]*schema.ToolInfo{{Name: "search", Desc: strings.Repeat("工具描述", 5000)}})}, []*schema.Message{schema.UserMessage("问")}, true},
		{"正常", []model.Option{model.WithMaxTokens(128)}, []*schema.Message{schema.UserMessage("问")}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, streaming := range []bool{false, true} {
				before := requests.Load()
				m := client.ChatModel()

				if streaming {
					var stream *schema.StreamReader[*schema.Message]
					stream, err = m.Stream(context.Background(), c.msgs, c.opts...)
					if stream != nil {
						_, readErr := stream.Recv()
						if err == nil && readErr != nil {
							err = readErr
						}
						stream.Close()
					}
				} else {
					_, err = m.Generate(context.Background(), c.msgs, c.opts...)
				}
				if c.reject {
					if err == nil || requests.Load() != before {
						t.Fatalf("超限发送了请求: err=%v calls=%d", err, requests.Load()-before)
					}
				} else if err != nil || requests.Load() != before+1 || actualOutput.Load() != 128 {
					t.Fatalf("预算内调用失败: %v output=%d", err, actualOutput.Load())
				}
			}
		})
	}
	bound, err := client.ChatModel().WithTools([]*schema.ToolInfo{{Name: "bound", Desc: strings.Repeat("工具描述", 5000)}})
	if err != nil {
		t.Fatal(err)
	}
	before := requests.Load()
	_, err = bound.Generate(context.Background(), []*schema.Message{schema.UserMessage("问")})
	if err == nil || requests.Load() != before {
		t.Fatal("WithTools 未保护")
	}
}

func TestActualBudgetUsesBusinessModelWindow(t *testing.T) {
	policy := config.DefaultContextPolicy()
	m := &contextModel{modelName: "gpt-4o", declared: 64000, output: 4096, policy: policy}
	ctx := WithBudgetIdentity(context.Background(), "business-model-budget-test", 64000)
	ReduceContextBudgetOnError("business-model-budget-test", apperrors.KindContextLength)
	t.Cleanup(func() { ResetEffectiveMaxContextLength("business-model-budget-test") })
	raw, _ := json.Marshal(map[string]any{"model": "gpt-4o", "max_completion_tokens": 4096, "messages": []map[string]string{{"role": "user", "content": strings.Repeat("问题", 5000)}}})
	if _, err := m.checkPayload(ctx, nil, raw); !errors.Is(err, ErrContextBudget) {
		t.Fatal("遗漏运行时窗口修正", err)
	}
}
