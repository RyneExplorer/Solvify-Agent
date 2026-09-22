package providers

import (
	"testing"

	"solvify-agent/internal/tool"
)

func TestMCPProvider_Name(t *testing.T) {
	p := NewMCPProvider(NewMCPClientPool())
	if p.Name() != "mcp" {
		t.Errorf("Name() = %q, want %q", p.Name(), "mcp")
	}
}

func TestMCPProvider_Validate(t *testing.T) {
	pool := NewMCPClientPool()
	p := NewMCPProvider(pool)

	tests := []struct {
		name    string
		config  *tool.ExecuteConfig
		wantErr bool
	}{
		{
			name: "stdio 配置合法",
			config: &tool.ExecuteConfig{
				ProviderConfig: &tool.ProviderConfig{
					MCP: &tool.MCPProviderConfig{
						Transport: "stdio",
						Command:   "npx",
						Args:      []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "sse 配置合法",
			config: &tool.ExecuteConfig{
				ProviderConfig: &tool.ProviderConfig{
					MCP: &tool.MCPProviderConfig{
						Transport: "sse",
						URL:       "http://localhost:12345/sse",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "http 配置合法",
			config: &tool.ExecuteConfig{
				ProviderConfig: &tool.ProviderConfig{
					MCP: &tool.MCPProviderConfig{
						Transport: "http",
						URL:       "http://localhost:8080/mcp",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "transport 为空",
			config: &tool.ExecuteConfig{
				ProviderConfig: &tool.ProviderConfig{
					MCP: &tool.MCPProviderConfig{},
				},
			},
			wantErr: true,
		},
		{
			name: "stdio 缺少 command",
			config: &tool.ExecuteConfig{
				ProviderConfig: &tool.ProviderConfig{
					MCP: &tool.MCPProviderConfig{
						Transport: "stdio",
					},
				},
			},
			wantErr: true,
		},
		{
			name: "sse 缺少 url",
			config: &tool.ExecuteConfig{
				ProviderConfig: &tool.ProviderConfig{
					MCP: &tool.MCPProviderConfig{
						Transport: "sse",
					},
				},
			},
			wantErr: true,
		},
		{
			name: "不支持的 transport",
			config: &tool.ExecuteConfig{
				ProviderConfig: &tool.ProviderConfig{
					MCP: &tool.MCPProviderConfig{
						Transport: "websocket",
						URL:       "ws://localhost:8080",
					},
				},
			},
			wantErr: true,
		},
		{
			name:    "ProviderConfig 为空",
			config:  &tool.ExecuteConfig{},
			wantErr: true,
		},
		{
			name: "MCP 配置为空",
			config: &tool.ExecuteConfig{
				ProviderConfig: &tool.ProviderConfig{},
			},
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
	_, _, err := pool.GetOrCreate(nil, &tool.MCPProviderConfig{
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
	cfg1 := &tool.MCPProviderConfig{
		Transport: "stdio",
		Command:   "npx",
		Args:      []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
		Timeout:   30,
	}
	cfg2 := &tool.MCPProviderConfig{
		Transport: "stdio",
		Command:   "npx",
		Args:      []string{"-y", "@modelcontextprotocol/server-filesystem", "/tmp"},
		Timeout:   60, // timeout 不同，但 hash 应相同
	}
	cfg3 := &tool.MCPProviderConfig{
		Transport: "stdio",
		Command:   "npx",
		Args:      []string{"-y", "@modelcontextprotocol/server-fetch"}, // args 不同
		Timeout:   30,
	}

	h1 := configHash(cfg1)
	h2 := configHash(cfg2)
	h3 := configHash(cfg3)

	if h1 != h2 {
		t.Error("相同连接配置（timeout 不同）的 hash 应相同")
	}
	if h1 == h3 {
		t.Error("不同连接配置的 hash 应不同")
	}
}
