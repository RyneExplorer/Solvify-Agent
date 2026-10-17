package tool

import (
	"context"

	einoTool "github.com/cloudwego/eino/components/tool"
)

// Provider 工具供应商接口（只负责执行）
type Provider interface {
	// Validate 验证执行配置
	Validate(config *ExecuteConfig) error

	// Execute 执行工具调用
	Execute(ctx context.Context, config *ExecuteConfig) (string, error)
}

// MultiToolProvider 多工具供应商接口（可选实现）
// 一个供应商可提供多个工具，ToolFactory 会为每个工具创建独立 BaseTool
// MCP 类型供应商实现此接口，运行时调用 MCP tools/list 拉取所有工具
type MultiToolProvider interface {
	Provider
	// GetTools 拉取该供应商提供的所有工具
	// 返回的 BaseTool 列表会直接注入 Agent
	GetTools(ctx context.Context, providerConfig *ProviderConfig) ([]einoTool.BaseTool, error)
}

// ExecuteConfig 执行配置
type ExecuteConfig struct {
	// LLM 传入的参数（如 query）
	ToolInput map[string]interface{}

	// 用户配置（API Key 等）
	UserConfig map[string]interface{}

	// 供应商配置（MCP连接）
	ProviderConfig *ProviderConfig

	// 管理员配置（业务参数）
	AdminConfig map[string]interface{}
}

// ProviderConfig 供应商配置（MCP连接）
type ProviderConfig struct {
	MCP *MCPProviderConfig `json:"mcp,omitempty"`
}

// MCPProviderConfig MCP 供应商配置
type MCPProviderConfig struct {
	// 仅由可信配置解析设置，不能由用户JSON覆盖。
	ScopeKey        string            `json:"-"`
	RestrictNetwork bool              `json:"-"`
	Transport       string            `json:"transport"`                 // stdio | sse | http
	Command         string            `json:"command,omitempty"`         // stdio: 可执行文件
	Args            []string          `json:"args,omitempty"`            // stdio: 命令参数
	Env             map[string]string `json:"env,omitempty"`             // stdio: 环境变量
	URL             string            `json:"url,omitempty"`             // sse/http: 端点 URL
	Headers         map[string]string `json:"headers,omitempty"`         // sse/http: 请求头
	Timeout         int               `json:"timeout,omitempty"`         // 兼容旧配置，新字段分别优先
	ConnectTimeout  *int              `json:"connect_timeout,omitempty"` // 建连和初始化的完整时限（秒）
	CallTimeout     *int              `json:"call_timeout,omitempty"`    // tools/list和单次工具调用时限（秒）
	AdminTimeouts   bool              `json:"admin_timeouts,omitempty"`  // 系统预置由管理员保存的时限优先于启动YAML
}
