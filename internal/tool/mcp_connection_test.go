package tool

import (
	"context"
	"encoding/json"
	"testing"

	einotool "github.com/cloudwego/eino/components/tool"
	"gorm.io/datatypes"

	"solvify-agent/internal/model/entity"
)

func TestMCPFactoryRevalidatesRevocationAndCredentialRotation(t *testing.T) {
	config := newMCPConfig(`{}`)
	config.Template.ProviderConfig = datatypes.JSON(`{"mcp":{"transport":"http","url":"https://example.com/mcp","headers":{"Authorization":"Bearer old"}}}`)
	store := &fakeConfigStore{configs: []entity.MCPConnection{config}}
	f := NewFactory(&fakeRegistry{provider: &fakeMultiProvider{tools: []einotool.BaseTool{deadlineProbeTool{}}}}, store)
	tools := f.CreateAgentTools(context.Background(), "u1")
	if len(tools) != 1 {
		t.Fatal("未加载MCP")
	}
	invoke := tools[0].(einotool.InvokableTool)
	if _, err := invoke.InvokableRun(context.Background(), `{}`); err != nil {
		t.Fatal(err)
	}
	store.configs[0].IsEnabled = false
	if _, err := invoke.InvokableRun(context.Background(), `{}`); err == nil {
		t.Fatal("停用后继续调用")
	}
	store.configs[0].IsEnabled = true
	store.configs[0].Template.ProviderConfig = datatypes.JSON(`{"mcp":{"transport":"http","url":"https://example.com/mcp","headers":{"Authorization":"Bearer new"}}}`)
	if _, err := invoke.InvokableRun(context.Background(), `{}`); err == nil {
		t.Fatal("旧工具继续使用已轮换凭据")
	}
}
func TestMCPResolveRejectsHostOverrideAndUnknownCredentialFields(t *testing.T) {
	c := newMCPConfig(`{"api_key":"key"}`)
	c.Template.ConfigSchema = datatypes.JSON(`{"type":"object","properties":{"api_key":{"type":"string"}},"required":["api_key"]}`)
	for _, headers := range []map[string]string{{"Host": "evil"}, {"Authorization": "Bearer {{missing}}"}, {"Authorization": "Bearer {{api_key}}\r\nInjected: yes"}} {
		raw, _ := json.Marshal(ProviderConfig{MCP: &MCPProviderConfig{Transport: "http", URL: "https://example.com", Headers: headers}})
		c.Template.ProviderConfig = raw
		if _, _, err := ResolveMCPConnection(&c); err == nil {
			t.Fatal("非法凭据绑定被接受")
		}
	}
	c.Template.ProviderType = "http"
	if _, _, err := ResolveMCPConnection(&c); err == nil {
		t.Fatal("HTTP预置仍可加载")
	}
}
