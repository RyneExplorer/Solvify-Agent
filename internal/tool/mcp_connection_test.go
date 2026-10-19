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
	config.Template.Connection = datatypes.JSON(`{"transport":"http","url":"https://example.com/mcp","headers":{"Authorization":"Bearer old"}}`)
	store := &fakeConfigStore{configs: []entity.MCPConnection{config}}
	f := NewFactory(&fakeMCPClient{tools: []einotool.BaseTool{deadlineProbeTool{}}}, store)
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
	// 使用底层名称，停用后已有实例也必须拒绝执行。
	baseInfo, _ := deadlineProbeTool{}.Info(context.Background())
	store.configs[0].Config, _ = json.Marshal(map[string]interface{}{"disabled_tools": []string{baseInfo.Name}})
	if _, err := invoke.InvokableRun(context.Background(), `{}`); err == nil {
		t.Fatal("逐工具停用后旧实例继续调用")
	}
	store.configs[0].Config = datatypes.JSON(`{}`)
	if _, err := invoke.InvokableRun(context.Background(), `{}`); err != nil {
		t.Fatal("重新启用工具后无法调用", err)
	}
	store.configs[0].Template.Connection = datatypes.JSON(`{"transport":"http","url":"https://example.com/mcp","headers":{"Authorization":"Bearer new"}}`)
	if _, err := invoke.InvokableRun(context.Background(), `{}`); err == nil {
		t.Fatal("旧工具继续使用已轮换凭据")
	}
}
func TestMCPResolveRejectsHostOverrideAndUnknownCredentialFields(t *testing.T) {
	c := newMCPConfig(`{"api_key":"key"}`)
	c.Template.ConfigSchema = datatypes.JSON(`{"type":"object","properties":{"api_key":{"type":"string"}},"required":["api_key"]}`)
	for _, headers := range []map[string]string{{"Host": "evil"}, {"Authorization": "Bearer {{missing}}"}, {"Authorization": "Bearer {{api_key}}\r\nInjected: yes"}} {
		raw, _ := json.Marshal(MCPConfig{Transport: "http", URL: "https://example.com", Headers: headers})
		c.Template.Connection = raw
		if _, _, err := ResolveMCPConnection(&c); err == nil {
			t.Fatal("非法凭据绑定被接受")
		}
	}
	c.Template.IsEnabled = false
	if _, _, err := ResolveMCPConnection(&c); err == nil {
		t.Fatal("停用预置仍可加载")
	}
}
