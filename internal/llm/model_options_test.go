package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func capabilityConfig(t *testing.T, change func(map[string]any)) []byte {
	t.Helper()
	c := map[string]any{"max_input_tokens": 2048, "max_output_tokens": 1024, "max_completion_tokens": 256, "output_token_field": "max_tokens", "tool_calling": true, "reasoning_capability": "none", "reasoning_mode": "default"}
	if change != nil {
		change(c)
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestModelCapabilitiesRequireConfirmedLimits(t *testing.T) {
	for _, key := range []string{"max_output_tokens", "max_completion_tokens", "output_token_field", "tool_calling", "reasoning_capability", "reasoning_mode"} {
		t.Run(key, func(t *testing.T) {
			_, err := ParseModelOptions(capabilityConfig(t, func(c map[string]any) { delete(c, key) }), 8192)
			if !errors.Is(err, ErrContextConfig) {
				t.Fatalf("缺失能力未拒绝: %v", err)
			}
		})
	}
	for _, change := range []func(map[string]any){
		func(c map[string]any) { c["max_input_tokens"] = 9000 },
		func(c map[string]any) { c["max_completion_tokens"] = 1025 },
		func(c map[string]any) { c["unknown"] = true },
		func(c map[string]any) { c["reasoning_capability"] = "always"; c["reasoning_mode"] = "off" },
		func(c map[string]any) { c["reasoning_capability"] = "optional"; c["reasoning_mode"] = "on" },
	} {
		if _, err := ParseModelOptions(capabilityConfig(t, change), 8192); !errors.Is(err, ErrContextConfig) {
			t.Fatalf("非法能力未拒绝: %v", err)
		}
	}
	if _, err := ParseModelOptions(capabilityConfig(t, nil), 0); err == nil {
		t.Fatal("猜测了缺失窗口")
	}
}

func TestOptionalInputLimitUsesSharedWindowAtHTTP(t *testing.T) {
	raw := capabilityConfig(t, func(c map[string]any) { delete(c, "max_input_tokens") })
	if _, err := ParseModelOptions(raw, 8192); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseModelDraft([]byte(`{}`), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseModelOptions([]byte(`{}`), 0); err == nil {
		t.Fatal("未确认配置可运行")
	}
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer s.Close()
	c, err := NewOpenAIClientDirect(context.Background(), OpenAIClientConfig{Model: "test", BaseURL: s.URL, APIKey: "synthetic", MaxContextLength: 8192, Config: raw})
	if err != nil {
		t.Fatal(err)
	}
	if c.InputLimit() != 8192 {
		t.Fatal("把缺失输入限制变为零预算")
	}
	if _, err = c.ChatModel().Generate(context.Background(), []*schema.Message{schema.UserMessage("hi")}); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal(calls)
	}
}

func TestConfiguredProtocolAndInputLimitAtHTTPBoundary(t *testing.T) {
	for _, protocol := range []string{"thinking", "enable_thinking", "reasoning_effort"} {
		t.Run(protocol, func(t *testing.T) {
			var payload map[string]any
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
			}))
			defer server.Close()
			raw := capabilityConfig(t, func(c map[string]any) {
				c["reasoning_capability"] = "optional"
				c["reasoning_mode"] = "on"
				c["reasoning_protocol"] = protocol
				c["tools_with_reasoning"] = true
				if protocol == "reasoning_effort" {
					c["reasoning_mode"] = "default"
					c["reasoning_effort"] = "low"
				}
			})
			client, err := NewOpenAIClient(context.Background(), OpenAIClientConfig{Model: "test", BaseURL: server.URL, APIKey: "synthetic", MaxContextLength: 8192, Config: raw})
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.ChatModel().Generate(context.Background(), []*schema.Message{schema.UserMessage("hi")}, model.WithMaxTokens(128))
			if err != nil {
				t.Fatal(err)
			}
			if payload["max_tokens"] != float64(128) || payload["max_completion_tokens"] != nil {
				t.Fatalf("输出字段错误: %v", payload)
			}
			switch protocol {
			case "thinking":
				if payload[protocol].(map[string]any)["type"] != "enabled" {
					t.Fatal(payload)
				}
			case "enable_thinking":
				if payload[protocol] != true {
					t.Fatal(payload)
				}
			case "reasoning_effort":
				if payload[protocol] != "low" {
					t.Fatal(payload)
				}
			}
			before := calls
			_, err = client.ChatModel().Generate(context.Background(), []*schema.Message{schema.UserMessage(strings.Repeat("长问题", 1200))})
			if !errors.Is(err, ErrContextBudget) || calls != before {
				t.Fatalf("独立输入上限失效: %v calls=%d", err, calls)
			}
			_, err = client.ChatModel().Generate(context.Background(), []*schema.Message{schema.UserMessage("hi")}, model.WithMaxTokens(1025))
			if !errors.Is(err, ErrContextConfig) || calls != before {
				t.Fatalf("输出能力保护失效: %v", err)
			}
		})
	}
}

func TestToolsRejectedBeforeNetwork(t *testing.T) {
	for _, change := range []func(map[string]any){
		func(c map[string]any) { c["tool_calling"] = false },
		func(c map[string]any) { c["reasoning_capability"] = "always" },
	} {
		client, err := NewOpenAIClient(context.Background(), OpenAIClientConfig{Model: "test", BaseURL: "https://example.invalid/v1", APIKey: "synthetic", MaxContextLength: 8192, Config: capabilityConfig(t, change)})
		if err != nil {
			t.Fatal(err)
		}
		if err = client.CheckAgentTools(); !errors.Is(err, ErrContextConfig) {
			t.Fatal(err)
		}
		if _, err = client.ChatModel().WithTools([]*schema.ToolInfo{{Name: "echo"}}); !errors.Is(err, ErrContextConfig) {
			t.Fatal(err)
		}
		_, err = client.ChatModel().Generate(context.Background(), []*schema.Message{schema.UserMessage("hi")}, model.WithTools([]*schema.ToolInfo{{Name: "echo"}}))
		if !errors.Is(err, ErrContextConfig) {
			t.Fatal(err)
		}
	}
}

func TestInputLimitReducesCompressionWatermarks(t *testing.T) {
	b := ContextBudget{Hard: 8000, Soft: 6000, Target: 4000}.WithInputLimit(2000)
	if b.Hard != 2000 || b.Soft != 1500 || b.Target != 1000 {
		t.Fatal(b)
	}
}
