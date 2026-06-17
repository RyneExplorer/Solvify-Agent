package providers

import (
	"context"
	"fmt"
)

// HTTPGetProvider 通用 HTTP GET 供应商
type HTTPGetProvider struct{}

func NewHTTPGetProvider() *HTTPGetProvider { return &HTTPGetProvider{} }

func (p *HTTPGetProvider) Name() string { return "HTTP GET" }

func (p *HTTPGetProvider) GetInputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{"type": "string", "description": "搜索查询关键词"},
		},
		"required": []string{"query"},
	}
}

func (p *HTTPGetProvider) GetConfigSchema() map[string]interface{} {
	return map[string]interface{}{
		"fields": []map[string]interface{}{
			{"key": "api_key", "label": "API Key", "type": "password", "required": true},
		},
	}
}

func (p *HTTPGetProvider) Validate(c map[string]interface{}) error { return nil }

func (p *HTTPGetProvider) Execute(ctx context.Context, toolInput, userConfig, adminConfig map[string]interface{}) (string, error) {
	baseURL, _ := adminConfig["_base_url"].(string)
	if baseURL == "" {
		return "", fmt.Errorf("admin_config._base_url 未配置")
	}
	payload := mergeAll(userConfig, adminConfig, toolInput)
	resp, err := httpGet(ctx, buildQueryString(baseURL, payload))
	if err != nil {
		return "", fmt.Errorf("请求失败: %w", err)
	}
	defer resp.Body.Close()
	return readAndFormat(resp)
}
