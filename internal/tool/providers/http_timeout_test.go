package providers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"solvify-agent/internal/tool"
)

func TestHTTPToolExecutionTimeout(t *testing.T) {
	for _, stage := range []string{"headers", "body", "parent", "cancel", "override", "invalid"} {
		t.Run(stage, func(t *testing.T) {
			var calls atomic.Int32
			exited := make(chan struct{}, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if stage == "body" {
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
				}
				if stage == "override" {
					select {
					case <-r.Context().Done():
					case <-time.After(500 * time.Millisecond):
						fmt.Fprint(w, "ok")
					}
					exited <- struct{}{}
					return
				}
				<-r.Context().Done()
				exited <- struct{}{}
			}))
			defer server.Close()
			p := NewHTTPProvider(300 * time.Millisecond)
			defer p.client.CloseIdleConnections()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if stage == "parent" {
				var shorter context.CancelFunc
				ctx, shorter = context.WithTimeout(ctx, 150*time.Millisecond)
				defer shorter()
			}
			if stage == "cancel" {
				cancel()
			}
			seconds := 0
			if stage == "override" {
				seconds = 1
			}
			if stage == "invalid" {
				seconds = -1
			}
			result, err := p.Execute(ctx, &tool.ExecuteConfig{ProviderConfig: &tool.ProviderConfig{URL: server.URL, Method: "GET", Timeout: seconds}, ToolInput: map[string]interface{}{"timeout": 99999}, UserConfig: map[string]interface{}{"timeout": 99999}})
			if stage == "override" {
				if err != nil || result != "ok" {
					t.Fatalf("覆盖未生效: %q %v", result, err)
				}
			} else if stage == "cancel" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			} else if stage == "invalid" {
				if err == nil {
					t.Fatal("读取失败仍执行")
				}
			} else if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal(err)
			}
			if stage == "cancel" || stage == "invalid" {
				if calls.Load() != 0 {
					t.Fatal("不应发出HTTP请求")
				}
				return
			}
			select {
			case <-exited:
			case <-time.After(time.Second):
				t.Fatal("超时后HTTP请求未关闭")
			}
			if calls.Load() != 1 {
				t.Fatal("工具超时不应自动重试")
			}
		})
	}
}

func TestHTTPToolAllowsLongerThanOldLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(16 * time.Second):
			fmt.Fprint(w, "long result")
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	p := NewHTTPProvider(2 * time.Second)
	defer p.client.CloseIdleConnections()
	result, err := p.Execute(context.Background(), &tool.ExecuteConfig{ProviderConfig: &tool.ProviderConfig{URL: server.URL, Method: "GET", Timeout: 25}})
	if err != nil || result != "long result" {
		t.Fatalf("配置25秒应允许16秒HTTP响应: %q %v", result, err)
	}
}
