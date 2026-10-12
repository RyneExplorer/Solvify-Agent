package tool

import (
	"context"

	einoTool "github.com/cloudwego/eino/components/tool"
)

// Provider 工具供应商接口（只负责执行）
type Provider interface {
	// Name 供应商类型名称（http, mcp, custom）
	Name() string

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

	// 供应商配置（HTTP 配置等）
	ProviderConfig *ProviderConfig

	// 管理员配置（业务参数）
	AdminConfig map[string]interface{}
}

// ProviderConfig 供应商配置（HTTP 配置等）
type ProviderConfig struct {
	Method          string                 `json:"method"`
	URL             string                 `json:"url"`
	Headers         map[string]string      `json:"headers"`
	BodyTemplate    map[string]interface{} `json:"body_template"`
	ResponseMapping map[string]string      `json:"response_mapping"`
	Auth            *AuthConfig            `json:"auth"`

	// MCP 供应商配置（仅 provider_type=mcp 时使用）
	MCP *MCPProviderConfig `json:"mcp,omitempty"`
}

// MCPProviderConfig MCP 供应商配置
type MCPProviderConfig struct {
	Transport string            `json:"transport"`            // stdio | sse | http
	Command   string            `json:"command,omitempty"`    // stdio: 可执行文件
	Args      []string          `json:"args,omitempty"`       // stdio: 命令参数
	Env       map[string]string `json:"env,omitempty"`        // stdio: 环境变量
	URL       string            `json:"url,omitempty"`        // sse/http: 端点 URL
	Headers   map[string]string `json:"headers,omitempty"`    // sse/http: 请求头
	Timeout   int               `json:"timeout,omitempty"`    // 超时秒数：用于连接初始化与工具调用，未配置时连接默认 30、调用兜底 300
}

// AuthConfig 认证配置
type AuthConfig struct {
	Type       string `json:"type"`        // bearer, api_key, basic
	TokenField string `json:"token_field"` // user_config 中的字段名
	Header     string `json:"header"`      // 自定义 header 名
	Prefix     string `json:"prefix"`      // header 值前缀
}
