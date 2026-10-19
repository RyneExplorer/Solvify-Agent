package mcpclient

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	einotool "github.com/cloudwego/eino/components/tool"

	"solvify-agent/internal/tool"
)

func TestMCPAuthenticationCoversInitializeListAndCall(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]int{}
	server := timeoutMCPServer(t, func(r *http.Request, method string) bool {
		token := r.Header.Get("Authorization")
		if token != "Bearer alice" && token != "Bearer bob" {
			t.Errorf("%s未收到认证", method)
			return false
		}
		mu.Lock()
		seen[token+":"+method]++
		mu.Unlock()
		return true
	})
	pool := NewMCPClientPool()
	defer pool.Close()
	provider := NewMCPClient(pool)
	var hashes []string
	for _, owner := range []string{"alice", "bob"} {
		cfg := &tool.MCPConfig{Transport: "http", URL: server.URL, Headers: map[string]string{"Authorization": "Bearer " + owner}, ScopeKey: owner + ":connection"}
		hashes = append(hashes, configHash(cfg))
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		tools, err := provider.GetTools(ctx, cfg)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		if len(tools) != 1 {
			cancel()
			t.Fatal("工具未发现")
		}
		if _, err = tools[0].(einotool.InvokableTool).InvokableRun(ctx, `{}`); err != nil {
			cancel()
			t.Fatal(err)
		}
		cancel()
	}
	mu.Lock()
	defer mu.Unlock()
	for _, owner := range []string{"alice", "bob"} {
		for _, method := range []string{"initialize", "tools/list", "tools/call"} {
			if seen["Bearer "+owner+":"+method] != 1 {
				t.Fatalf("认证链不完整: %v", seen)
			}
		}
	}
	if hashes[0] == hashes[1] {
		t.Fatal("双用户共享会话")
	}
	a := &tool.MCPConfig{Transport: "http", URL: server.URL, ScopeKey: "a"}
	b := *a
	b.ScopeKey = "b"
	if configHash(a) == configHash(&b) {
		t.Fatal("相同端点无凭据也必须按归属隔离")
	}
}
func TestPersonalMCPNetworkAndResponseLimits(t *testing.T) {
	for _, raw := range []string{"127.0.0.1", "10.0.0.1", "169.254.169.254", "100.64.0.1", "::1", "::ffff:127.0.0.1", "fc00::1", "198.18.0.1", "2001:db8::1"} {
		if publicMCPAddress(netip.MustParseAddr(raw)) {
			t.Fatalf("保留地址被允许: %s", raw)
		}
	}
	for _, raw := range []string{"8.8.8.8", "2606:4700:4700::1111"} {
		if !publicMCPAddress(netip.MustParseAddr(raw)) {
			t.Fatal("公网地址被拒")
		}
	}
	var targetCalls int
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls++; w.WriteHeader(200) }))
	defer target.Close()
	cfg := &tool.MCPConfig{Transport: "http", URL: target.URL, RestrictNetwork: true}
	req, _ := http.NewRequest(http.MethodGet, target.URL, nil)
	if _, err := mcpHTTPClient(cfg).Do(req); err == nil || targetCalls != 0 {
		t.Fatal("个人MCP可访问内网")
	}
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer redirect.Close()
	cfg = &tool.MCPConfig{Transport: "http", URL: redirect.URL, Headers: map[string]string{"Authorization": "secret"}}
	req, _ = http.NewRequest(http.MethodGet, redirect.URL, nil)
	if _, err := mcpHTTPClient(cfg).Do(req); err == nil || targetCalls != 0 {
		t.Fatal("跟随重定向泄露认证")
	}
	// 走真实Transport读取超限响应，必须报错，不能将截断内容当完整结果。
	large := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", (8<<20)+1))
	}))
	defer large.Close()
	cfg = &tool.MCPConfig{Transport: "http", URL: large.URL}
	resp, err := mcpHTTPClient(cfg).Get(large.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err == nil || len(data) != 8<<20 {
		t.Fatalf("响应限制无效: bytes=%d err=%v", len(data), err)
	}
}
