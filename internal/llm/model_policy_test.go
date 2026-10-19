package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/cloudwego/eino/schema"

	"solvify-agent/pkg/config"
)

func TestFitOutputLimitPreservesInputAndMinimum(t *testing.T) {
	p := config.ContextDefaults()
	b, err := ResolveContextBudget("", "", 8192, 4096, false, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name                              string
		requested, used, inputLimit, want int
		fail                              bool
	}{
		{"预算充足", 4096, 100, 0, 4096, false},
		{"原输出预留占满窗口", 8192, 100, 0, 8192 - b.Margin - 100, false},
		{"必要输入让出输出空间", 4096, 4000, 0, 8192 - b.Margin - 4000, false},
		{"输出下限边界", 4096, 8192 - b.Margin - MinimumAdaptiveOutput, 0, MinimumAdaptiveOutput, false},
		{"下限少一token", 4096, 8192 - b.Margin - MinimumAdaptiveOutput + 1, 0, 0, true},
		{"独立输入上限", 4096, 1025, 1024, 0, true},
		{"显式小输出不提高", 128, 100, 0, 128, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := FitOutputLimit(8192, tc.inputLimit, tc.requested, tc.used, p)
			if tc.fail {
				if !errors.Is(err, ErrContextBudget) {
					t.Fatalf("未拒绝不可恢复输入: %v", err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("分配错误: output=%d want=%d err=%v", got, tc.want, err)
			}
			actual, err := ResolveContextBudget("", "", 8192, got, false, p)
			if err != nil || actual.WithInputLimit(tc.inputLimit).Check(tc.used) != nil {
				t.Fatal("分配后仍超预算", err)
			}
		})
	}
}

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
