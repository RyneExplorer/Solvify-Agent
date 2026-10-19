package agent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"
	"solvify-agent/internal/llm"
	"solvify-agent/pkg/config"
)

type interruptionStreamAgent struct{}

func (*interruptionStreamAgent) Name(context.Context) string        { return "interruption" }
func (*interruptionStreamAgent) Description(context.Context) string { return "流失败测试" }
func (a *interruptionStreamAgent) Run(ctx context.Context, _ *adk.AgentInput, _ ...adk.AgentRunOption) *adk.AsyncIterator[*adk.AgentEvent] {
	iter, generator := adk.NewAsyncIteratorPair[*adk.AgentEvent]()
	go func() {
		defer generator.Close()
		sr, sw := schema.Pipe[*schema.Message](2)
		sw.Send(schema.AssistantMessage("部分答案", nil), nil)
		sw.Send(nil, fmt.Errorf("读取超时: %w", context.DeadlineExceeded))
		sw.Close()
		// 迭代器本身没有 Err，错误只出现在消息流中。
		generator.Send(adk.EventFromMessage(nil, sr, schema.Assistant, ""))
	}()
	return iter
}

func TestModelHTTPTimeoutRetryBoundaries(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(fmt.Sprintf("partial_%t", partial), func(t *testing.T) {
			var calls atomic.Int32
			stop := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				attempt := calls.Add(1)
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				if attempt > 1 {
					fmt.Fprint(w, "data: {\"id\":\"test\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"重试完成\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					return
				}
				if partial {
					fmt.Fprint(w, "data: {\"id\":\"test\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"部分答案\"}}]}\n\n")
				}
				w.(http.Flusher).Flush()
				select {
				case <-r.Context().Done():
				case <-stop:
				}
			}))
			defer server.Close()
			defer close(stop)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			client, err := llm.NewOpenAIClient(ctx, llm.OpenAIClientConfig{
				BaseURL: server.URL, Model: "agent-timeout-test", MaxContextLength: 8192,
				Config:  []byte(`{"max_input_tokens":8192,"max_output_tokens":8192,"max_completion_tokens":2048,"output_token_field":"max_completion_tokens","tool_calling":true,"reasoning_capability":"none","reasoning_mode":"default"}`),
				Timeout: time.Second, StreamFirstOutputTimeout: 200 * time.Millisecond, StreamIdleTimeout: 200 * time.Millisecond,
			})
			if err != nil {
				t.Fatal(err)
			}
			engine := &Engine{cfg: config.AgentConfig{QuickAgentMaxIterations: 4, ModelMaxRetries: 1}}
			events := collectModelRetryEvents(t, ctx, engine, Request{Profile: ProfileQuick, Query: "测试", MaxContextLength: 8192, OutputLimit: 2048}, client.ChatModel())
			if !partial {
				if calls.Load() != 2 || !hasRetryDone(events, "重试完成") {
					t.Fatalf("首输出超时未正确重试: calls=%d events=%+v", calls.Load(), events)
				}
				return
			}
			failures := 0
			var answer strings.Builder
			for _, event := range events {
				if event.Type == EventDone {
					t.Fatal("空闲超时仍发送成功终态")
				}
				if event.Type == EventError {
					failures++
				}
				if event.Type == EventAnswer {
					answer.WriteString(event.Content)
				}
			}
			if calls.Load() != 1 || failures != 1 || answer.String() != "部分答案" {
				t.Fatalf("部分输出超时被重试或丢失: calls=%d failures=%d answer=%q", calls.Load(), failures, answer.String())
			}
		})
	}
}

func TestRunnerStreamErrorCannotBecomeSuccess(t *testing.T) {
	ctx := context.Background()
	runner := adk.NewRunner(ctx, adk.RunnerConfig{Agent: &interruptionStreamAgent{}})
	ch := make(chan Event, 10)
	(&Engine{}).runWithRunner(ctx, runner, "", nil, Request{}, nil, nil, ch)
	close(ch)
	var answer strings.Builder
	failures := 0
	for event := range ch {
		if event.Type == EventAnswer {
			answer.WriteString(event.Content)
		}
		if event.Type == EventError {
			failures++
		}
		if event.Type == EventDone {
			t.Fatal("消息流失败被误判为成功")
		}
	}
	if failures != 1 || answer.String() != "部分答案" {
		t.Fatalf("错误或部分正文丢失: failures=%d answer=%q", failures, answer.String())
	}
}
