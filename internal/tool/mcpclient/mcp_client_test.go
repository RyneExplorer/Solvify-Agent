package mcpclient

import (
	"testing"

	"solvify-agent/internal/tool"
)

func TestMCPClient_Validate(t *testing.T) {
	pool := NewMCPClientPool()
	p := NewMCPClient(pool)

	tests := []struct {
		name    string
		config  *tool.MCPConfig
		wantErr bool
	}{
		{
			name: "stdio 不再支持（网页版只能 http/sse）",
			config: &tool.MCPConfig{
				Transport: "stdio",
				Command:   "npx",
				Args:      []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
			},
			wantErr: true,
		},
		{
			name: "sse 配置合法",
			config: &tool.MCPConfig{
				Transport: "sse",
				URL:       "http://localhost:12345/sse",
			},
			wantErr: false,
		},
		{
			name: "http 配置合法",
			config: &tool.MCPConfig{
				Transport: "http",
				URL:       "http://localhost:8080/mcp",
			},
			wantErr: false,
		},
		{
			name:    "transport 为空",
			config:  &tool.MCPConfig{},
			wantErr: true,
		},
		{
			name: "stdio 缺少 command（已被整体拒绝）",
			config: &tool.MCPConfig{
				Transport: "stdio",
			},
			wantErr: true,
		},
		{
			name: "sse 缺少 url",
			config: &tool.MCPConfig{
				Transport: "sse",
			},
			wantErr: true,
		},
		{
			name: "不支持的 transport",
			config: &tool.MCPConfig{
				Transport: "websocket",
				URL:       "ws://localhost:8080",
			},
			wantErr: true,
		},
		{
			name:    "ProviderConfig 为空",
			config:  nil,
			wantErr: true,
		},
		{
			name:    "MCP 配置为空",
			config:  &tool.MCPConfig{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := p.Validate(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr = %v", err, tt.wantErr)
			}
		})
	}
}

func TestMCPClientPool_GetOrCreate_InvalidConfig(t *testing.T) {
	pool := NewMCPClientPool()

	// 非法配置应返回错误
	_, _, err := pool.GetOrCreate(nil, &tool.MCPConfig{
		Transport: "invalid",
	})
	if err == nil {
		t.Error("GetOrCreate() 期望返回错误，实际为 nil")
	}
}

func TestMCPClientPool_GetOrCreate_NilConfig(t *testing.T) {
	pool := NewMCPClientPool()

	_, _, err := pool.GetOrCreate(nil, nil)
	if err == nil {
		t.Error("GetOrCreate() 期望返回错误，实际为 nil")
	}
}

func TestMCPClientPool_Close(t *testing.T) {
	pool := NewMCPClientPool()
	// 空池关闭不应报错
	if err := pool.Close(); err != nil {
		t.Errorf("Close() 期望返回 nil，实际 = %v", err)
	}
}

func TestConfigHash(t *testing.T) {
	cfg1 := &tool.MCPConfig{
		Transport: "http",
		URL:       "https://a.example.com/mcp",
		Timeout:   30,
	}
	cfg2 := &tool.MCPConfig{
		Transport:   "http",
		URL:         "https://a.example.com/mcp",
		CallTimeout: ptrMCPSeconds(60),
		Timeout:     30, // 仅调用时限不同，仍复用连接
	}
	cfg3 := &tool.MCPConfig{
		Transport: "http",
		URL:       "https://b.example.com/mcp", // url 不同
		Timeout:   30,
	}

	h1 := configHash(cfg1)
	h2 := configHash(cfg2)
	h3 := configHash(cfg3)

	if h1 != h2 {
		t.Error("仅调用时限不同的配置应复用连接")
	}
	if h1 == h3 {
		t.Error("不同连接配置的 hash 应不同")
	}
	cfg2.ConnectTimeout = ptrMCPSeconds(60)
	if h1 == configHash(cfg2) {
		t.Error("连接时限改变不能命中旧初始化缓存")
	}
}

func ptrMCPSeconds(seconds int) *int { return &seconds }
