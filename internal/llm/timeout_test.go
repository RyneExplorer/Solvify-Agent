package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudwego/eino/schema"

	"solvify-agent/pkg/config"
)

func TestSystemTimeoutReachesCachedChatClient(t *testing.T) {
	resetClientCache(t)
	t.Setenv("LLM_TIMEOUT", "")
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("llm:\n  timeout: 1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { loaded.LLM.Timeout = config.Default().LLM.Timeout })
	stop := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-stop:
		}
	}))
	defer server.Close()
	defer close(stop)
	cfg := testModelConfig(func(c *ModelConfig) { c.BaseURL = server.URL })
	client, err := NewClientFromModelConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	// 环境变量覆盖同一份 YAML；相同模型在策略改变后必须使用新的缓存条目。
	t.Setenv("LLM_TIMEOUT", "2")
	updated, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { updated.LLM.Timeout = config.Default().LLM.Timeout })
	longer, err := NewClientFromModelConfig(context.Background(), cfg)
	if err != nil || longer == client {
		t.Fatalf("环境变量覆盖超时时限后仍复用旧客户端：%v", err)
	}
	reused, err := NewClientFromModelConfig(context.Background(), cfg)
	if err != nil || reused != longer {
		t.Fatalf("相同超时策略未命中客户端缓存：%v", err)
	}
	if got := cfg.openAIConfig().Timeout; got != 2*time.Second {
		t.Fatalf("环境变量未覆盖YAML：%v", got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2500*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = client.ChatModel().Generate(ctx, []*schema.Message{schema.UserMessage("hi")})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
		t.Fatalf("系统配置的1秒应先于上游2.5秒结束调用：耗时=%v 错误=%v 上游=%v", time.Since(started), err, ctx.Err())
	}
	t.Logf("响应头返回后仍受系统超时限制：耗时=%v 错误=%v", time.Since(started), err)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 3500*time.Millisecond)
	defer cancel2()
	started = time.Now()
	_, err = longer.ChatModel().Generate(ctx2, []*schema.Message{schema.UserMessage("hi")})
	if !errors.Is(err, context.DeadlineExceeded) || ctx2.Err() != nil || time.Since(started) < 1500*time.Millisecond {
		t.Fatalf("覆盖后的2秒时限未实际生效：耗时=%v 错误=%v 上游=%v", time.Since(started), err, ctx2.Err())
	}
	t.Logf("环境变量覆盖后实际调用时限：耗时=%v", time.Since(started))
}

func TestModelHTTPTimeoutBehavior(t *testing.T) {
	for _, scenario := range []string{"slow_headers", "slow_body", "stream_before_content", "stream_after_content", "success", "parent_deadline", "parent_cancel"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			timeout := 200 * time.Millisecond
			if scenario == "parent_deadline" {
				ctx, cancel = context.WithTimeout(ctx, 50*time.Millisecond)
				defer cancel()
				timeout = time.Second
			}
			stop := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if scenario == "parent_cancel" {
					cancel()
				}
				if scenario == "success" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
					return
				}
				if scenario != "slow_headers" {
					w.Header().Set("Content-Type", "application/json")
					if scenario == "stream_before_content" || scenario == "stream_after_content" {
						w.Header().Set("Content-Type", "text/event-stream")
					}
					w.WriteHeader(http.StatusOK)
					if scenario == "stream_after_content" {
						_, _ = fmt.Fprint(w, "data: {\"id\":\"test\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"partial\"}}]}\n\n")
					}
					w.(http.Flusher).Flush()
				}
				select {
				case <-r.Context().Done():
				case <-stop:
				}
			}))
			defer server.Close()
			defer close(stop)
			cfg := testModelConfig(func(c *ModelConfig) { c.BaseURL = server.URL }).openAIConfig()
			cfg.Timeout = timeout
			client, err := NewOpenAIClient(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			messages := []*schema.Message{schema.UserMessage("hi")}
			var content string
			if scenario == "stream_before_content" || scenario == "stream_after_content" {
				stream, streamErr := client.ChatModel().Stream(ctx, messages)
				err = streamErr
				if stream != nil {
					defer stream.Close()
					for err == nil {
						message, readErr := stream.Recv()
						err = readErr
						if message != nil {
							content += message.Content
						}
					}
				}
			} else {
				message, callErr := client.ChatModel().Generate(ctx, messages)
				err = callErr
				if message != nil {
					content = message.Content
				}
			}
			switch scenario {
			case "success":
				if err != nil || content != "ok" {
					t.Fatalf("正常请求失败：%q %v", content, err)
				}
			case "parent_cancel":
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("未保留请求取消：%v", err)
				}
			default:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("未返回调用超时：%v", err)
				}
				if scenario != "parent_deadline" && ctx.Err() != nil {
					t.Fatalf("等待到了上游兜底，单次超时未生效：%v", ctx.Err())
				}
				if scenario == "stream_after_content" && content != "partial" {
					t.Fatalf("超时前的部分输出丢失：%q", content)
				}
			}
		})
	}
}

func TestModelTimeoutClientsReuseConnections(t *testing.T) {
	var connections atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	server.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			connections.Add(1)
		}
	}
	server.Start()
	defer server.Close()
	for _, timeout := range []time.Duration{time.Second, 2 * time.Second} {
		cfg := testModelConfig(func(c *ModelConfig) { c.BaseURL = server.URL }).openAIConfig()
		cfg.Timeout = timeout
		client, err := NewOpenAIClient(context.Background(), cfg)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.ChatModel().Generate(context.Background(), []*schema.Message{schema.UserMessage("hi")}); err != nil {
			t.Fatal(err)
		}
	}
	if got := connections.Load(); got != 1 {
		t.Fatalf("不同超时客户端未复用TCP连接：连接数=%d", got)
	}
}
