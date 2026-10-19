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

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

func TestNewToolsKeepConfiguredWindowAndConsumeInputBudget(t *testing.T) {
	var calls atomic.Int32
	var receivedTools atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var payload struct {
			Tools []json.RawMessage `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		receivedTools.Store(int32(len(payload.Tools)))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	const declared = 32768
	client, err := NewOpenAIClient(context.Background(), OpenAIClientConfig{Model: "provider-model", BaseURL: server.URL, APIKey: "synthetic", MaxContextLength: declared, Config: capabilityConfig(t, func(c map[string]any) { c["max_input_tokens"] = declared })})
	if err != nil {
		t.Fatal(err)
	}
	input := []*schema.Message{schema.UserMessage("读取测试数据")}
	tools := []*schema.ToolInfo{{Name: "existing_tool", Desc: "原有测试工具", ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{"value": {Type: schema.String}})}}
	first, err := client.ChatModel().WithTools(tools)
	if err != nil {
		t.Fatal(err)
	}
	initialTokens := ToolsTokens(tools, "provider-model")
	tools = append(tools, &schema.ToolInfo{Name: "new_tool", Desc: "新加入的工具", ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{"query": {Type: schema.String, Required: true}})})
	second, err := first.WithTools(tools)
	if err != nil {
		t.Fatal(err)
	}
	if ToolsTokens(tools, "provider-model") <= initialTokens {
		t.Fatal("新增工具未增加预算占用")
	}
	for index, cm := range []model.ToolCallingChatModel{first, second} {
		limits, ok := cm.(interface {
			ContextWindow() int
			InputLimit() int
			OutputLimit() int
		})
		if !ok || limits.ContextWindow() != declared || limits.InputLimit() != declared || limits.OutputLimit() != 256 {
			t.Fatal("绑定新工具丢失模型额度")
		}
		if _, err = cm.Generate(context.Background(), input); err != nil {
			t.Fatal(err)
		}
		if receivedTools.Load() != int32(index+1) {
			t.Fatal("HTTP工具定义与绑定不一致")
		}
	}
	before := calls.Load()
	oversized := append(append([]*schema.ToolInfo{}, tools...), &schema.ToolInfo{Name: "oversized_new_tool", Desc: strings.Repeat("工具参数与描述\n", 6000)})
	third, err := second.WithTools(oversized)
	if err != nil {
		t.Fatal(err)
	}
	if third.(interface{ ContextWindow() int }).ContextWindow() != declared {
		t.Fatal("超大工具改变了模型窗口")
	}
	if _, err = third.Generate(context.Background(), input); !errors.Is(err, ErrContextBudget) || calls.Load() != before {
		t.Fatalf("新增工具超限未在HTTP之前拦截: %v", err)
	}
	if _, err = second.Generate(context.Background(), input, model.WithTools(oversized)); !errors.Is(err, ErrContextBudget) || calls.Load() != before {
		t.Fatalf("调用级新工具绕过预算: %v", err)
	}
	if _, err = client.ChatModel().Generate(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	if client.MaxContextLength() != declared || receivedTools.Load() != 0 {
		t.Fatal("绑定工具污染共享客户端")
	}
}
