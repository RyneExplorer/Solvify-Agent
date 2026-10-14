package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// ModelListItem 描述服务商明确返回的元数据；不推断能力。
type ModelListItem struct {
	ID              string `json:"id"`
	Object          string `json:"object"`
	Created         int64  `json:"created"`
	OwnedBy         string `json:"owned_by"`
	ContextWindow   int    `json:"context_window,omitempty"`
	MaxTokens       int    `json:"max_tokens,omitempty"`
	MaxInputTokens  int    `json:"max_input_tokens,omitempty"`
	MaxOutputTokens int    `json:"max_output_tokens,omitempty"`
}

// ModelsListResponse 描述 /v1/models 返回结构
type ModelsListResponse struct {
	Object string          `json:"object"`
	Data   []ModelListItem `json:"data"`
}

// DetectContextLengthViaAPI 尝试通过 /v1/models 接口探测指定模型的上下文窗口。
// 如果接口不可用、未返回该模型或未包含上下文信息，返回 0 和错误。
func DetectContextLengthViaAPI(ctx context.Context, baseURL, apiKey, modelID string) (int, error) {
	item, err := DetectModelMetadata(ctx, baseURL, apiKey, modelID)
	if err != nil {
		return 0, err
	}
	if item.ContextWindow <= 0 {
		return 0, fmt.Errorf("服务商未提供明确的context_window")
	}
	return item.ContextWindow, nil
}

// DetectModelMetadata 只采用含义明确的字段，max_tokens 不推断为窗口。
func DetectModelMetadata(ctx context.Context, baseURL, apiKey, modelID string) (ModelListItem, error) {
	if baseURL == "" || modelID == "" {
		return ModelListItem{}, fmt.Errorf("服务地址或模型ID为空")
	}

	url := strings.TrimRight(baseURL, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ModelListItem{}, fmt.Errorf("创建能力探测请求失败")
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := getSharedHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return ModelListItem{}, fmt.Errorf("服务商能力探测连接失败")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ModelListItem{}, fmt.Errorf("服务商能力探测返回HTTP %d", resp.StatusCode)
	}

	var list ModelsListResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&list); err != nil {
		return ModelListItem{}, fmt.Errorf("服务商能力元数据无效或过大")
	}

	for _, m := range list.Data {
		if m.ID == modelID {
			return m, nil
		}
	}

	return ModelListItem{}, fmt.Errorf("服务商未返回指定模型")
}
