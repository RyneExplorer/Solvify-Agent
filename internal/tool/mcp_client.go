package tool

import (
	"context"
	einoTool "github.com/cloudwego/eino/components/tool"
)

// MCPClient 直接发现MCP工具，工具执行由返回的BaseTool负责。
type MCPClient interface {
	Validate(*MCPConfig) error
	GetTools(context.Context, *MCPConfig) ([]einoTool.BaseTool, error)
}
