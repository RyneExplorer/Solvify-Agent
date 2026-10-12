package tool

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
)

// fakeTool 模拟 eino MCP tool：Info 每次返回同一个内部指针（与 eino toolHelper 行为一致）
type fakeTool struct {
	info *schema.ToolInfo
}

func (f *fakeTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return f.info, nil
}

// fakeInvokableTool 模拟 eino-ext 的 MCP 工具：只实现 InvokableTool，不实现 StreamableTool。
// 这是 prefixedTool 包装的真实对象形态，也是审批中间件能否生效的关键前提。
type fakeInvokableTool struct {
	fakeTool
	invoked int
}

func (f *fakeInvokableTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	f.invoked++
	return "已执行", nil
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

// TestPrefixedTool_StreamPath_AppliesInvokableMiddleware 验证：MCP 工具在**流式**链路上
// 依然会经过 Invokable 中间件（危险工具审批 / 澄清追问依赖它）。
//
// 背景（eino v0.9.1 compose/tool_node.go:524-527）：ToolsNode 构造时按接口分流，
//
//	工具实现 StreamableTool → streamable = wrapStreamToolCall(它, 流式中间件列表)
//	否则                    → streamable = invokableToStreamable(已包「Invokable 中间件」的端点)
//
// 也就是说，只要 prefixedTool 自己实现了 StreamableRun，流式链路就只会套用**流式**中间件；
// 而本项目只注册了 ToolMiddleware{Invokable: ...}，流式列表为空 —— 审批被静默跳过，
// 危险 MCP 工具（write/delete/execute 等）会在深度模式下直接执行。
//
// 这个测试是上述行为的回归闸：一旦有人再给 prefixedTool 补上 StreamableRun，它会立刻失败。
func TestPrefixedTool_StreamPath_AppliesInvokableMiddleware(t *testing.T) {
	ctx := context.Background()

	base := &fakeInvokableTool{fakeTool: fakeTool{info: &schema.ToolInfo{Name: "delete_file", Desc: "删除文件"}}}
	p := &prefixedTool{BaseTool: base, prefix: "mcp"}

	middlewareRuns := 0
	middleware := func(next compose.InvokableToolEndpoint) compose.InvokableToolEndpoint {
		return func(ctx context.Context, input *compose.ToolInput) (*compose.ToolOutput, error) {
			middlewareRuns++
			// 真实中间件在这里 StatefulInterrupt 等审批；测试里直接模拟"用户拒绝"：
			// 不放行 next，返回拒绝结果（与 buildDangerousToolMiddleware 的拒绝分支语义一致）
			return &compose.ToolOutput{Result: "❌ 操作被用户拒绝，未执行"}, nil
		}
	}

	node, err := compose.NewToolNode(ctx, &compose.ToolsNodeConfig{
		Tools:               []tool.BaseTool{p},
		ToolCallMiddlewares: []compose.ToolMiddleware{{Invokable: middleware}},
	})
	if err != nil {
		t.Fatalf("构造 ToolsNode 失败: %v", err)
	}

	info, err := p.Info(ctx)
	if err != nil {
		t.Fatalf("获取工具信息失败: %v", err)
	}
	if info.Name != "mcp_delete_file" {
		t.Fatalf("工具名 = %q, want %q", info.Name, "mcp_delete_file")
	}

	idx := 0
	input := schema.AssistantMessage("", []schema.ToolCall{{
		Index: &idx,
		ID:    "call_1",
		Type:  "function",
		Function: schema.FunctionCall{
			Name:      info.Name,
			Arguments: "{}",
		},
	}})

	sr, err := node.Stream(ctx, input)
	if err != nil {
		t.Fatalf("ToolsNode.Stream 失败: %v", err)
	}
	defer sr.Close()
	var out strings.Builder
	for {
		msgs, rErr := sr.Recv()
		if rErr != nil {
			if !errors.Is(rErr, io.EOF) {
				t.Fatalf("消费流失败: %v", rErr)
			}
			break
		}
		for _, m := range msgs {
			if m != nil {
				out.WriteString(m.Content)
			}
		}
	}

	if middlewareRuns == 0 {
		t.Fatalf("流式链路下 Invokable 中间件未被调用：危险工具会绕过审批直接执行。" +
			"prefixedTool 不应实现 StreamableRun（详见本测试注释）")
	}
	if base.invoked != 0 {
		t.Errorf("中间件已拦截，但底层工具仍被执行了 %d 次", base.invoked)
	}
	if !strings.Contains(out.String(), "拒绝") {
		t.Errorf("工具结果未把中间件的拦截结果透出，实际=%q", out.String())
	}
}
