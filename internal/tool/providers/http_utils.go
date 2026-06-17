package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

func httpGet(ctx context.Context, rawURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	return http.DefaultClient.Do(req)
}

func httpPost(ctx context.Context, rawURL string, body map[string]interface{}) (*http.Response, error) {
	return httpPostWithHeaders(ctx, rawURL, body, nil)
}

// httpPostWithHeaders 支持自定义请求头的 HTTP POST
func httpPostWithHeaders(ctx context.Context, rawURL string, body map[string]interface{}, headers map[string]string) (*http.Response, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, bytes.NewReader(jsonBody))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return http.DefaultClient.Do(req)
}

// mergeAll 合并 userConfig + adminConfig + toolInput（优先级：toolInput > adminConfig > userConfig）
func mergeAll(userConfig, adminConfig, toolInput map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range userConfig {
		result[k] = v
	}
	for k, v := range adminConfig {
		result[k] = v
	}
	for k, v := range toolInput {
		result[k] = v
	}
	return result
}

func buildQueryString(baseURL string, payload map[string]interface{}) string {
	params := url.Values{}
	for k, v := range payload {
		params.Set(k, fmt.Sprintf("%v", v))
	}
	return baseURL + "?" + params.Encode()
}

func readAndFormat(resp *http.Response) (string, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取响应失败: %w", err)
	}
	var raw interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return string(body), nil
	}
	pretty, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return string(body), nil
	}
	result := string(pretty)
	if len(result) > 8000 {
		result = result[:8000] + "\n...（已截断）"
	}
	return result, nil
}
