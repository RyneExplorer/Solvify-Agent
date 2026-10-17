package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	einotool "github.com/cloudwego/eino/components/tool"
	"gorm.io/datatypes"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/tool"
)

// 本地JSON-RPC服务覆盖真实MCP SDK的握手、清单及工具调用，不访问外部供应商。
func timeoutMCPServer(t *testing.T, before func(*http.Request, string) bool) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(400)
			return
		}
		if before != nil && !before(r, req.Method) {
			return
		}
		if len(req.ID) == 0 {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": "2025-03-26", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]any{"name": "timeout-test", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": []any{map[string]any{"name": "probe", "description": "测试", "inputSchema": map[string]any{"type": "object", "properties": map[string]any{}}}}}
		case "tools/call":
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}}
		default:
			result = map[string]any{}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	}))
	t.Cleanup(s.Close)
	return s
}

func TestMCPInitializationUsesConfiguredDeadline(t *testing.T) {
	exited := make(chan struct{}, 1)
	s := timeoutMCPServer(t, func(r *http.Request, method string) bool {
		if method == "initialize" {
			<-r.Context().Done()
			exited <- struct{}{}
			return false
		}
		return true
	})
	pool := NewMCPClientPool()
	t.Cleanup(func() { _ = pool.Close() })
	cfg := &tool.MCPProviderConfig{Transport: "http", URL: s.URL, ConnectTimeout: ptrMCPSeconds(1), CallTimeout: ptrMCPSeconds(10)}
	start := time.Now()
	_, _, err := pool.GetOrCreate(context.Background(), cfg)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 4*time.Second {
		t.Fatal("初始化未按独立时限结束", err, time.Since(start))
	}
	select {
	case <-exited:
	case <-time.After(2 * time.Second):
		t.Fatal("超时后底层请求未取消")
	}
}

func TestMCPInitializationCanExceedOldSixtySecondCap(t *testing.T) {
	if testing.Short() {
		t.Skip("真实61秒初始化验证")
	}
	s := timeoutMCPServer(t, func(r *http.Request, method string) bool {
		if method == "initialize" {
			select {
			case <-time.After(61 * time.Second):
			case <-r.Context().Done():
				return false
			}
		}
		return true
	})
	pool := NewMCPClientPool()
	t.Cleanup(func() { _ = pool.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()
	start := time.Now()
	c, _, err := pool.GetOrCreate(ctx, &tool.MCPProviderConfig{Transport: "http", URL: s.URL, ConnectTimeout: ptrMCPSeconds(65), CallTimeout: ptrMCPSeconds(1)})
	if err != nil || c == nil || time.Since(start) < 60*time.Second {
		t.Fatal("65秒配置仍被旧60秒截断或被调用时限限制", err, time.Since(start))
	}
}

func TestMCPSharedInitializationAndPoolClose(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		t.Run(map[bool]string{false: "parent_cancel", true: "pool_close"}[shutdown], func(t *testing.T) {
			started, release, exited := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var calls atomic.Int32
			s := timeoutMCPServer(t, func(r *http.Request, method string) bool {
				if method != "initialize" {
					return true
				}
				if calls.Add(1) == 1 {
					close(started)
				}
				defer close(exited)
				select {
				case <-release:
					return true
				case <-r.Context().Done():
					return false
				}
			})
			pool := NewMCPClientPool()
			t.Cleanup(func() { _ = pool.Close() })
			cfg := &tool.MCPProviderConfig{Transport: "http", URL: s.URL, ConnectTimeout: ptrMCPSeconds(10)}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			first := make(chan error, 1)
			go func() { _, _, err := pool.GetOrCreate(ctx, cfg); first <- err }()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("初始化未开始")
			}
			if shutdown {
				closed := make(chan struct{})
				go func() { _ = pool.Close(); close(closed) }()
				select {
				case <-closed:
				case <-time.After(2 * time.Second):
					t.Fatal("关闭池未取消初始化")
				}
				select {
				case <-exited:
				case <-time.After(2 * time.Second):
					t.Fatal("关闭池后底层请求未退出")
				}
				if err := <-first; err == nil {
					t.Fatal("关闭后等待者收到成功")
				}
				return
			}
			cancel()
			if err := <-first; !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			second := make(chan error, 1)
			go func() { _, _, err := pool.GetOrCreate(context.Background(), cfg); second <- err }()
			close(release)
			select {
			case err := <-second:
				if err != nil {
					t.Fatal("一个等待者取消影响其他等待者", err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("共享初始化未完成")
			}
			if calls.Load() != 1 {
				t.Fatal("共享初始化发生重复握手", calls.Load())
			}
		})
	}
}

type mcpTimeoutConfigStore struct{ configs []entity.MCPConnection }

func (s mcpTimeoutConfigStore) ListEnabledByUserID(context.Context, string) ([]entity.MCPConnection, error) {
	return s.configs, nil
}

func (s mcpTimeoutConfigStore) Get(_ context.Context, userID, id string) (*entity.MCPConnection, error) {
	for _, c := range s.configs {
		if c.ID == id && c.UserID == userID {
			return &c, nil
		}
	}
	return nil, fmt.Errorf("missing")
}

func TestMCPCallAndListUseIndependentTimeout(t *testing.T) {
	for _, stage := range []string{"list", "call", "parent"} {
		t.Run(stage, func(t *testing.T) {
			exited := make(chan struct{}, 1)
			s := timeoutMCPServer(t, func(r *http.Request, method string) bool {
				if (stage == "list" && method == "tools/list") || (stage != "list" && method == "tools/call") {
					<-r.Context().Done()
					exited <- struct{}{}
					return false
				}
				return true
			})
			pool := NewMCPClientPool()
			t.Cleanup(func() { _ = pool.Close() })
			provider := NewMCPProvider(pool)
			cfg := &tool.ProviderConfig{MCP: &tool.MCPProviderConfig{Transport: "http", URL: s.URL, ConnectTimeout: ptrMCPSeconds(10), CallTimeout: ptrMCPSeconds(1)}}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if stage == "list" {
				start := time.Now()
				_, err := provider.Execute(ctx, &tool.ExecuteConfig{ProviderConfig: cfg})
				if err == nil || ctx.Err() != nil || time.Since(start) > 3*time.Second {
					t.Fatal("清单请求没有单次调用时限", err)
				}
			} else {
				registry := tool.NewProviderRegistry()
				registry.Register("mcp", provider)
				raw, _ := json.Marshal(cfg)
				store := mcpTimeoutConfigStore{configs: []entity.MCPConnection{{ID: "c1", UserID: "user", IsEnabled: true, Config: datatypes.JSON(`{}`), TemplateID: func() *string { v := "p1"; return &v }(), Template: &entity.ToolProvider{ProviderKey: "test", ProviderType: "mcp", IsEnabled: true, ToolType: entity.ToolType{ToolKey: "mcp", IsEnabled: true}, ProviderConfig: datatypes.JSON(raw)}}}}
				tools := tool.NewFactory(registry, store).CreateAgentTools(ctx, "user")
				if len(tools) != 1 {
					t.Fatal("真实MCP工具加载失败")
				}
				callCtx := ctx
				if stage == "parent" {
					var stop context.CancelFunc
					callCtx, stop = context.WithTimeout(ctx, 100*time.Millisecond)
					defer stop()
				}
				start := time.Now()
				_, err := tools[0].(einotool.InvokableTool).InvokableRun(callCtx, `{}`)
				if !errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil || time.Since(start) > 3*time.Second {
					t.Fatal("工具调用未按独立时限/父时限结束", err)
				}
			}
			select {
			case <-exited:
			case <-time.After(2 * time.Second):
				t.Fatal("请求未取消")
			}
		})
	}
}
