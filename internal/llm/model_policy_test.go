package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestManagedExecutionPreservesCapabilities(t *testing.T) {
	for _, tc := range []struct{ window, maxOutput, output int }{{32768, 16384, 8192}, {8192, 4096, 2048}, {8192, 1024, 1024}, {8192, 128000, 2048}} {
		p, err := ParseModelOptions([]byte(`{"execution_policy":"managed_v1","max_output_tokens":`+strconv.Itoa(tc.maxOutput)+`,"max_completion_tokens":999999,"output_token_field":"max_tokens","tool_calling":true,"reasoning_capability":"optional","reasoning_mode":"off","reasoning_protocol":"thinking"}`), tc.window)
		if err != nil || *p.MaxCompletionTokens != tc.output || p.ReasoningMode != "off" || p.ReasoningProtocol != "thinking" {
			t.Fatal("执行策略未隔离用户参数或破坏兼容协议", p, err)
		}
	}
	if _, err := ParseModelOptions([]byte(`{"execution_policy":"managed_v1","output_token_field":"max_tokens","tool_calling":true,"reasoning_capability":"none","reasoning_mode":"default"}`), 8192); err == nil {
		t.Fatal("缺失最大输出仍可调用")
	}
}

func TestRemovedExecutionFieldsAreRejected(t *testing.T) {
	for _, key := range []string{"temperature", "timeout", "reasoning_effort"} {
		raw := capabilityConfig(t, func(p map[string]any) { p[key] = 1 })
		if _, err := ParseModelOptions(raw, 8192); err == nil {
			t.Fatal("废弃执行字段仍可由底层直接配置", key)
		}
	}
}

func TestManagedPolicyActualRequest(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
			return
		}
		var output int
		_ = json.Unmarshal(payload["max_tokens"], &output)
		if output != 2048 {
			t.Errorf("实际输出=%d，期望2048", output)
		}
		if _, exists := payload["temperature"]; exists {
			t.Error("仍向上游发送用户温度")
		}
		called = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()
	client, err := NewOpenAIClientDirect(context.Background(), OpenAIClientConfig{BaseURL: srv.URL, Model: "test", MaxContextLength: 8192, Config: []byte(`{"execution_policy":"managed_v1","max_output_tokens":4096,"max_completion_tokens":1,"output_token_field":"max_tokens","tool_calling":true,"reasoning_capability":"none","reasoning_mode":"default"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ChatModel().Generate(context.Background(), []*schema.Message{schema.UserMessage("hi")}); err != nil {
		t.Fatal(err)
	}
	if !called || client.OutputLimit() != 2048 {
		t.Fatal("客户端与预算输出不同源")
	}
}
