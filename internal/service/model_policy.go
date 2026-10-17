package service

import (
	"context"
	requestdto "solvify-agent/internal/model/dto/request"
)

func prepareAdminModelSettings(ctx context.Context, req requestdto.TestModelRequest) (map[string]interface{}, error) {
	next := managedModelSettings(req.Config)
	if _, exists := next["max_output_tokens"]; !exists {
		result, err := acquireModelCapabilities(ctx, req)
		if err != nil {
			return nil, err
		}
		if result.Success {
			for key, value := range result.SuggestedConfig {
				if _, supplied := req.Config[key]; !supplied {
					next[key] = value
				}
			}
			if result.DetectedMaxOutputTokens > 0 {
				next["max_output_tokens"] = result.DetectedMaxOutputTokens
			}
			if result.DetectedMaxInputTokens > 0 {
				next["max_input_tokens"] = result.DetectedMaxInputTokens
			}
		}
	}
	return next, nil
}

// managedModelSettings 复制候选配置，防止修改请求或持久化旧记录。
// 工具标志表达本产品要求，保存前仍必须完成真实工具协议验证。
func managedModelSettings(settings map[string]interface{}) map[string]interface{} {
	next := make(map[string]interface{}, len(settings)+5)
	for key, value := range settings {
		next[key] = value
	}
	for _, key := range []string{"max_completion_tokens", "temperature", "timeout", "reasoning_effort"} {
		delete(next, key)
	}
	next["execution_policy"] = "managed_v1"
	if _, exists := next["tool_calling"]; !exists {
		next["tool_calling"] = true
	}
	if _, exists := next["output_token_field"]; !exists {
		next["output_token_field"] = "max_tokens"
	}
	if _, exists := next["reasoning_capability"]; !exists {
		next["reasoning_capability"] = "unknown"
	}
	if _, exists := next["reasoning_mode"]; !exists {
		next["reasoning_mode"] = "default"
	}
	if _, exists := next["tools_with_reasoning"]; !exists {
		next["tools_with_reasoning"] = true
	}
	return next
}
