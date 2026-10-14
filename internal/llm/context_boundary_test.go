package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/pkg/config"
	"solvify-agent/pkg/tokenutil"
)

func TestContextExactInputBoundaryBeforeHTTP(t *testing.T) {
	for _, inputLimit := range []int{0, 1024} {
		t.Run(fmt.Sprintf("input_limit_%d", inputLimit), func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(`{"id":"boundary","object":"chat.completion","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"正常"},"finish_reason":"stop"}]}`))
			}))
			defer server.Close()
			client, err := NewOpenAIClient(context.Background(), OpenAIClientConfig{Model: "gpt-4o", APIKey: "synthetic", BaseURL: server.URL + "/v1", MaxContextLength: 8192,
				Config: []byte(fmt.Sprintf(`{"max_input_tokens":%d,"max_output_tokens":4096,"max_completion_tokens":4096,"output_token_field":"max_completion_tokens","tool_calling":true,"reasoning_capability":"none","reasoning_mode":"default"}`, inputLimit))})
			if err != nil {
				t.Fatal(err)
			}
			budget, err := ResolveContextBudget("", "gpt-4o", 8192, 4096, false, config.ContextDefaults())
			if err != nil {
				t.Fatal(err)
			}
			budget = budget.WithInputLimit(inputLimit)
			if tokenutil.CountTokens("x\n", "gpt-4o") != 2 {
				t.Fatal("边界样本的编码假设不成立")
			}
			for _, delta := range []int{-1, 0, 1} {
				target := budget.Hard + delta
				contentTokens := target - 4
				content := strings.Repeat("x\n", contentTokens/2)
				if contentTokens%2 != 0 {
					content += "x"
				}
				messages := []*schema.Message{schema.UserMessage(content)}
				if actual := MessagesTokens(messages, "gpt-4o"); actual != target {
					t.Fatalf("未命中精确边界：实际=%d 目标=%d", actual, target)
				}
				before := requests.Load()
				_, err = client.ChatModel().Generate(context.Background(), messages)
				if delta <= 0 {
					if err != nil || requests.Load() != before+1 {
						t.Fatalf("预算内请求被拒绝：输入=%d 错误=%v", target, err)
					}
				} else {
					if !errors.Is(err, ErrContextBudget) || requests.Load() != before {
						t.Fatalf("超过1 token仍发送HTTP：%v", err)
					}
					stream, streamErr := client.ChatModel().Stream(context.Background(), messages, model.WithMaxTokens(4096))
					if stream != nil {
						_, readErr := stream.Recv()
						stream.Close()
						if streamErr == nil {
							streamErr = readErr
						}
					}
					if !errors.Is(streamErr, ErrContextBudget) || requests.Load() != before {
						t.Fatalf("流式超限仍发送HTTP：%v", streamErr)
					}
				}
				t.Logf("硬上限=%d 输入=%d HTTP调用增量=%d", budget.Hard, target, requests.Load()-before)
			}
		})
	}
}
