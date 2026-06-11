package tool

import "context"

// Tool 定义可被 Agent 调用的工具接口
type Tool interface {
	// Name 返回工具名称，需全局唯一
	Name() string
	// Description 返回工具描述，供 LLM 理解何时使用
	Description() string
	// Parameters 返回工具参数的 JSON Schema 定义
	Parameters() map[string]any
	// Execute 执行工具，args 为 JSON 格式参数，返回结果文本
	Execute(ctx context.Context, args string) (string, error)
}
