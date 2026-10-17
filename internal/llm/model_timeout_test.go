package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"
)

func TestStreamTimeoutPolicy(t *testing.T) {
	for _, scenario := range []string{"headers", "heartbeat", "idle", "reasoning", "tools", "continuous", "total", "disconnect", "cancel", "close", "nonstream"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stop := make(chan struct{})
			entered := make(chan struct{})
			released := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				defer close(released)
				_, _ = io.Copy(io.Discard, r.Body)
				wait := func(d time.Duration) bool {
					timer := time.NewTimer(d)
					defer timer.Stop()
					select {
					case <-timer.C:
						return true
					case <-r.Context().Done():
						return false
					case <-stop:
						return false
					}
				}
				if scenario == "headers" {
					wait(3 * time.Second)
					return
				}
				if scenario == "nonstream" {
					if wait(300 * time.Millisecond) {
						fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
					}
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				if scenario == "cancel" {
					cancel()
					wait(3 * time.Second)
					return
				}
				for i := 0; i < 12; i++ {
					delta := `"content":"x"`
					if scenario == "reasoning" {
						delta = `"reasoning_content":"think"`
					}
					if scenario == "tools" {
						delta = `"tool_calls":[{"index":0,"function":{"arguments":"x"}}]`
					}
					if scenario == "heartbeat" {
						fmt.Fprint(w, ": keepalive\n\n")
					} else {
						fmt.Fprintf(w, "data: {\"id\":\"test\",\"choices\":[{\"index\":0,\"delta\":{%s}}]}\n\n", delta)
					}
					w.(http.Flusher).Flush()
					if scenario == "idle" || scenario == "close" {
						wait(3 * time.Second)
						return
					}
					if scenario == "disconnect" {
						conn, _, err := w.(http.Hijacker).Hijack()
						if err == nil {
							conn.Close()
						}
						return
					}
					if !wait(40 * time.Millisecond) {
						return
					}
				}
				fmt.Fprint(w, "data: [DONE]\n\n")
			}))
			defer server.Close()
			defer close(stop)
			cfg := testModelConfig(func(c *ModelConfig) { c.BaseURL = server.URL }).openAIConfig()
			cfg.Timeout = 2 * time.Second
			cfg.StreamFirstOutputTimeout = 200 * time.Millisecond
			cfg.StreamIdleTimeout = 200 * time.Millisecond
			if scenario == "total" {
				cfg.Timeout = 300 * time.Millisecond
			}
			client, err := NewOpenAIClient(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			// race 首次加载 tokenizer 可能耗尽短测试预算；预热后才能验证实际 HTTP 停滞与释放。
			MessageTokens(schema.UserMessage("hi"), cfg.Model)
			started := time.Now()
			if scenario == "nonstream" {
				msg, err := client.ChatModel().Generate(ctx, []*schema.Message{schema.UserMessage("hi")})
				if err != nil || msg.Content != "ok" {
					t.Fatalf("非流式不应受首输出/空闲时限影响: %v", err)
				}
				return
			}
			sr, err := client.ChatModel().Stream(ctx, []*schema.Message{schema.UserMessage("hi")})
			var content string
			if err == nil {
				defer sr.Close()
				for {
					var msg *schema.Message
					msg, err = sr.Recv()
					if err != nil {
						break
					}
					if msg != nil {
						content += msg.Content
					}
					if scenario == "close" {
						// Eino Close 关闭消费侧；取消 context 才能立即中止等待中的 HTTP 读取。
						cancel()
						break
					}
				}
			}
			switch scenario {
			case "continuous", "reasoning", "tools":
				if !errors.Is(err, io.EOF) || time.Since(started) < 400*time.Millisecond {
					t.Fatalf("有效片段应允许完整输出: err=%v elapsed=%v", err, time.Since(started))
				}
			case "cancel", "close":
				if scenario == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatalf("取消被误判: %v", err)
				}
			case "disconnect":
				if err == nil || errors.Is(err, io.EOF) || errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("明确断网应直接报错: %v", err)
				}
			default:
				if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
					t.Fatalf("超时未生效: err=%v elapsed=%v", err, time.Since(started))
				}
				if scenario == "idle" && content != "x" {
					t.Fatalf("部分输出丢失: %q", content)
				}
				if scenario == "headers" && !strings.Contains(err.Error(), "首个有效输出") {
					t.Fatalf("首输出未覆盖响应头: %v", err)
				}
			}
			select {
			case <-entered:
			default:
				t.Fatal("短测试预算在发出 HTTP 前耗尽，未验证到上游停滞行为")
			}
			select {
			case <-released:
			case <-time.After(time.Second):
				t.Fatal("取消/完成后底层 HTTP 未退出")
			}
		})
	}
}
