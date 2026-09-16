package tool

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/schema"
)

// fakeTool 模拟 eino MCP tool：Info 每次返回同一个内部指针（与 eino toolHelper 行为一致）
type fakeTool struct {
	info *schema.ToolInfo
}

func (f *fakeTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return f.info, nil
}

// TestPrefixedTool_Info_NoPrefixAccumulation 验证反复调用 Info 不会让前缀累积
// 背景：线上日志出现过 "mcp_mcp_mcp_mcp_mcp_read_file is not invokable"，
// 说明前缀被叠加了 5 层，导致 Agent 找不到工具。
func TestPrefixedTool_Info_NoPrefixAccumulation(t *testing.T) {
	base := &fakeTool{info: &schema.ToolInfo{Name: "read_file", Desc: "read a file"}}
	p := &prefixedTool{BaseTool: base, prefix: "mcp"}

	for i := 1; i <= 5; i++ {
		info, err := p.Info(context.Background())
		if err != nil {
			t.Fatalf("第 %d 次 Info 返回错误: %v", i, err)
		}
		if info.Name != "mcp_read_file" {
			t.Errorf("第 %d 次 Info().Name = %q, want %q（前缀发生了累积）", i, info.Name, "mcp_read_file")
		}
	}

	// 底层工具的原始名称不应被污染
	if base.info.Name != "read_file" {
		t.Errorf("底层工具名称被污染: got %q, want %q", base.info.Name, "read_file")
	}
}
