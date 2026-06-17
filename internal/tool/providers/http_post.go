package providers

import (
	"context"
	"fmt"
)

// HTTPPostProvider 通用 HTTP POST 供应商
type HTTPPostProvider struct{}

func NewHTTPPostProvider() *HTTPPostProvider { return &HTTPPostProvider{} }

func (p *HTTPPostProvider) Name() string { return "HTTP POST" }

func (p *HTTPPostProvider) GetInputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{"type": "string", "description": "搜索查询关键词"},
		},
		"required": []string{"query"},
	}
}

func (p *HTTPPostProvider) GetConfigSchema() map[string]interface{} {
	return map[string]interface{}{
		"fields": []map[string]interface{}{
			{"key": "api_key", "label": "API Key", "type": "password", "required": true},
		},
	}
}

func (p *HTTPPostProvider) Validate(c map[string]interface{}) error { return nil }

func (p *HTTPPostProvider) Execute(ctx context.Context, toolInput, userConfig, adminConfig map[string]interface{}) (string, error) {
	baseURL, _ := adminConfig["_base_url"].(string)
	if baseURL == "" {
		return "", fmt.Errorf("admin_config._base_url 未配置")
	}
	payload := mergeAll(userConfig, adminConfig, toolInput)
	resp, err := httpPost(ctx, baseURL, payload)
	if err != nil {
		return "", fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()
	return readAndFormat(resp)
}
