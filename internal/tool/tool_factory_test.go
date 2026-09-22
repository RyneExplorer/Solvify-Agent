package tool

import (
	"context"
	"testing"

	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"gorm.io/datatypes"

	"solvify-agent/internal/model/entity"
)

// ─── ToolFactory MCP 工具级停用过滤测试 ─────────────────────────────────────
//
// 用户配置 config.disabled_tools 中的工具名不注入 Agent；
// 未配置时全部注入；名单里的名字不存在的工具不受影响。

// fakeBaseTool 最小 BaseTool：只有名字，测试过滤逻辑足够
type fakeBaseTool struct{ name string }

func (t *fakeBaseTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: t.name}, nil
}

// fakeMultiProvider 返回固定工具列表的 MCP 供应商
type fakeMultiProvider struct{ tools []einoTool.BaseTool }

func (p *fakeMultiProvider) Name() string { return "mcp" }
func (p *fakeMultiProvider) Validate(*ExecuteConfig) error {
	return nil
}
func (p *fakeMultiProvider) Execute(context.Context, *ExecuteConfig) (string, error) {
	return "", nil
}
func (p *fakeMultiProvider) GetTools(context.Context, *ProviderConfig) ([]einoTool.BaseTool, error) {
	return p.tools, nil
}

// fakeRegistry 只返回 MCP 供应商的注册表
type fakeRegistry struct {
	provider Provider
}

func (r *fakeRegistry) Register(string, Provider)          {}
func (r *fakeRegistry) Get(string) Provider                { return r.provider }
func (r *fakeRegistry) List() map[string]Provider          { return nil }
func (r *fakeRegistry) Keys() []string                     { return nil }

// fakeConfigStore 返回固定工具配置
type fakeConfigStore struct {
	configs []entity.UserToolConfig
}

func (s *fakeConfigStore) ListEnabledByUserID(context.Context, string) ([]entity.UserToolConfig, error) {
	return s.configs, nil
}

func newMCPConfig(userConfigJSON string) entity.UserToolConfig {
	return entity.UserToolConfig{
		ID:         "cfg-1",
		UserID:     "u1",
		ToolTypeID: "tt-mcp",
		ProviderID: "p-fs",
		Config:     datatypes.JSON(userConfigJSON),
		IsEnabled:  true,
		ToolType:   entity.ToolType{ID: "tt-mcp", ToolKey: "mcp"},
		ToolProvider: entity.ToolProvider{
			ID:           "p-fs",
			ToolTypeID:   "tt-mcp",
			ProviderKey:  "filesystem",
			ProviderType: "mcp",
			ProviderConfig: datatypes.JSON(`{"mcp":{"transport":"stdio"}}`),
		},
	}
}

func toolsFor(t *testing.T, userConfigJSON string) []einoTool.BaseTool {
	t.Helper()
	multi := &fakeMultiProvider{tools: []einoTool.BaseTool{
		&fakeBaseTool{name: "read_file"},
		&fakeBaseTool{name: "write_file"},
	}}
	f := NewFactory(&fakeRegistry{provider: multi}, &fakeConfigStore{
		configs: []entity.UserToolConfig{newMCPConfig(userConfigJSON)},
	}, nil)
	return f.CreateAgentTools(context.Background(), "u1")
}

func toolNames(tools []einoTool.BaseTool) map[string]bool {
	names := make(map[string]bool, len(tools))
	for _, tl := range tools {
		if info, err := tl.Info(context.Background()); err == nil {
			names[info.Name] = true
		}
	}
	return names
}

// 未配置 disabled_tools：全部工具注入
func TestToolFactory_MCP_NoDisabledTools_AllInjected(t *testing.T) {
	tools := toolsFor(t, `{}`)
	if len(tools) != 2 {
		t.Fatalf("工具数 = %d，期望 2", len(tools))
	}
}

// 配置 disabled_tools：被点名的工具不注入；前缀拼接仍然正确
func TestToolFactory_MCP_DisabledTools_Filtered(t *testing.T) {
	tools := toolsFor(t, `{"disabled_tools":["write_file"]}`)
	if len(tools) != 1 {
		t.Fatalf("工具数 = %d，期望 1", len(tools))
	}
	names := toolNames(tools)
	if !names["mcp_filesystem_read_file"] {
		t.Fatalf("保留工具不见或前缀错误: %v", names)
	}
	if names["mcp_filesystem_write_file"] {
		t.Fatal("被停用工具仍注入 Agent")
	}
}

// disabled_tools 里的名字不存在时不影响其他工具
func TestToolFactory_MCP_DisabledUnknownName_AllInjected(t *testing.T) {
	tools := toolsFor(t, `{"disabled_tools":["not_exist"]}`)
	if len(tools) != 2 {
		t.Fatalf("工具数 = %d，期望 2", len(tools))
	}
}