package providers

import (
	"context"
	"fmt"
)

// BochaProvider 博查 AI 搜索（POST JSON + Bearer 鉴权）
//
//	接口文档：https://open.bocha.cn
//	Endpoint：POST https://api.bocha.cn/v1/web-search
type BochaProvider struct{}

func NewBochaProvider() *BochaProvider { return &BochaProvider{} }

func (p *BochaProvider) Name() string { return "BochaAI" }

// GetInputSchema Agent 调用时传给 LLM 的参数定义
// 业务参数（freshness、count、summary）由管理员 admin_config 统一管控，
// LLM 可根据对话上下文通过 input_schema 动态覆盖
func (p *BochaProvider) GetInputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "搜索查询关键词",
			},
			"freshness": map[string]interface{}{
				"type":        "string",
				"description": "搜索时间范围：noLimit（不限）、oneDay（一天内）、oneWeek（一周内）、oneMonth（一个月内）、oneYear（一年内）",
			},
			"count": map[string]interface{}{
				"type":        "integer",
				"description": "返回结果条数，范围 1-50",
			},
			"summary": map[string]interface{}{
				"type":        "boolean",
				"description": "是否返回文本摘要，默认 false",
			},
		},
		"required": []string{"query"},
	}
}

// GetConfigSchema 用户只需填 API Key，业务参数由管理员统一管控
func (p *BochaProvider) GetConfigSchema() map[string]interface{} {
	return map[string]interface{}{
		"fields": []map[string]interface{}{
			{
				"key":         "api_key",
				"label":       "API Key",
				"type":        "password",
				"required":    true,
				"placeholder": "在 open.bocha.cn 获取 API Key",
			},
		},
	}
}

// Validate 校验用户配置——api_key 必填
func (p *BochaProvider) Validate(c map[string]interface{}) error {
	if v, _ := c["api_key"].(string); v == "" {
		return fmt.Errorf("api_key 不能为空，请在 open.bocha.cn 获取")
	}
	return nil
}

// Execute 执行博查搜索
//
//	参数合并优先级：toolInput > adminConfig > userConfig
//	Authorization 头使用用户提供的 api_key
func (p *BochaProvider) Execute(ctx context.Context, toolInput, userConfig, adminConfig map[string]interface{}) (string, error) {
	// 取出 api_key，不放入请求 body
	apiKey, _ := userConfig["api_key"].(string)
	if apiKey == "" {
		return "", fmt.Errorf("api_key 未配置")
	}

	// 构建请求体（三层参数合并，但不含 api_key）
	payload := make(map[string]interface{})
	for k, v := range userConfig {
		if k != "api_key" {
			payload[k] = v
		}
	}
	for k, v := range adminConfig {
		payload[k] = v
	}
	for k, v := range toolInput {
		payload[k] = v
	}

	// 至少要有 query
	if _, ok := payload["query"]; !ok {
		return "", fmt.Errorf("query 参数缺失")
	}

	headers := map[string]string{
		"Authorization": "Bearer " + apiKey,
	}

	resp, err := httpPostWithHeaders(ctx, "https://api.bocha.cn/v1/web-search", payload, headers)
	if err != nil {
		return "", fmt.Errorf("博查搜索请求失败: %w", err)
	}
	defer resp.Body.Close()
	return readAndFormat(resp)
}
