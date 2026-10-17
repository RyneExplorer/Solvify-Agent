package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"gorm.io/datatypes"
	"solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/tool"
	"solvify-agent/internal/tool/providers"
)

func TestMCPAdminIndependentTimeouts(t *testing.T) {
	for _, system := range []bool{false, true} {
		repo := &providerTimeoutRepo{saved: entity.ToolProvider{ProviderType: "mcp", IsSystem: system, ProviderConfig: datatypes.JSON(`{"mcp":{"transport":"http","url":"https://example.invalid","headers":{"Authorization":"test-placeholder"},"timeout":45}}`)}}
		registry := tool.NewProviderRegistry()
		registry.Register("mcp", providers.NewMCPProvider(nil))
		svc := NewToolProviderService(repo, providerTimeoutTypeRepo{}, registry, nil, nil)
		for _, values := range [][2]int{{90, 120}, {30, 60}} {
			raw := json.RawMessage(`{"mcp":{"connect_timeout":` + jsonNumber(values[0]) + `,"call_timeout":` + jsonNumber(values[1]) + `}}`)
			if !system {
				raw = json.RawMessage(`{"mcp":{"transport":"http","url":"https://example.invalid","connect_timeout":` + jsonNumber(values[0]) + `,"call_timeout":` + jsonNumber(values[1]) + `}}`)
			}
			out, err := svc.Update(context.Background(), "id", request.UpdateToolProviderRequest{ProviderConfig: &raw})
			if err != nil {
				t.Fatal(err)
			}
			var pc tool.ProviderConfig
			if err = json.Unmarshal(out.ProviderConfig, &pc); err != nil {
				t.Fatal(err)
			}
			if pc.MCP.ConnectionTimeout() != time.Duration(values[0])*time.Second || pc.MCP.ToolCallTimeout() != time.Duration(values[1])*time.Second {
				t.Fatal(pc.MCP)
			}
			if system && (pc.MCP.Headers["Authorization"] != "test-placeholder" || pc.MCP.URL != "https://example.invalid" || !pc.MCP.AdminTimeouts) {
				t.Fatal("系统预置连接配置丢失", pc.MCP)
			}
		}
		for _, body := range []string{`{"mcp":{"connect_timeout":0}}`, `{"mcp":{"call_timeout":86401}}`, `{"mcp":{"connect_timeout":"30"}}`, `{"mcp":{"call_timeout":1.5}}`} {
			raw := json.RawMessage(body)
			before := repo.writes
			if _, err := svc.Update(context.Background(), "id", request.UpdateToolProviderRequest{ProviderConfig: &raw}); err == nil || before != repo.writes {
				t.Fatal("非法配置写入", body)
			}
		}
		if system {
			for _, body := range []string{`{"mcp":{"url":"https://changed.invalid"}}`, `{"mcp":{"headers":{}}}`, `{"mcp":{"admin_timeouts":false}}`, `{"mcp":{},"url":"bad"}`} {
				raw := json.RawMessage(body)
				before := repo.writes
				if _, err := svc.Update(context.Background(), "id", request.UpdateToolProviderRequest{ProviderConfig: &raw}); err == nil || before != repo.writes {
					t.Fatal("系统预置连接保护失效", body)
				}
			}
			raw := json.RawMessage(`{"mcp":{}}`)
			if _, err := svc.Update(context.Background(), "id", request.UpdateToolProviderRequest{ProviderConfig: &raw}); err != nil {
				t.Fatal(err)
			}
			var pc tool.ProviderConfig
			_ = json.Unmarshal(repo.saved.ProviderConfig, &pc)
			if pc.MCP.ConnectionTimeout() != 30*time.Second || pc.MCP.ToolCallTimeout() != 300*time.Second || pc.MCP.Timeout != 0 {
				t.Fatal("清空未恢复默认", pc.MCP)
			}
		}
	}
	// 新建供应商同样保存拆分字段。
	repo := &providerTimeoutRepo{}
	registry := tool.NewProviderRegistry()
	registry.Register("mcp", providers.NewMCPProvider(nil))
	svc := NewToolProviderService(repo, providerTimeoutTypeRepo{}, registry, nil, nil)
	_, err := svc.Create(context.Background(), request.CreateToolProviderRequest{ProviderType: "mcp", ProviderConfig: json.RawMessage(`{"transport":"http","url":"https://example.invalid","connect_timeout":90,"call_timeout":120}`)})
	if err != nil {
		t.Fatal(err)
	}
	var pc tool.ProviderConfig
	_ = json.Unmarshal(repo.saved.ProviderConfig, &pc)
	if *pc.MCP.ConnectTimeout != 90 || *pc.MCP.CallTimeout != 120 {
		t.Fatal(pc.MCP)
	}
}

func jsonNumber(value int) string { b, _ := json.Marshal(value); return string(b) }
