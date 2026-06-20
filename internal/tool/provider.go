package tool

import (
	"context"
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
}

// AuthConfig 认证配置
type AuthConfig struct {
	Type       string `json:"type"`        // bearer, api_key, basic
	TokenField string `json:"token_field"` // user_config 中的字段名
	Header     string `json:"header"`      // 自定义 header 名
	Prefix     string `json:"prefix"`      // header 值前缀
}
