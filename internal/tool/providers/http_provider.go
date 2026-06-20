package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"solvify-agent/internal/tool"
)

// HTTPProvider 通用 HTTP 供应商
// 通过数据库配置实现不同供应商的功能
type HTTPProvider struct {
	client *http.Client
}

// NewHTTPProvider 创建 HTTP Provider
func NewHTTPProvider() *HTTPProvider {
	return &HTTPProvider{
		client: &http.Client{},
	}
}

func (p *HTTPProvider) Name() string { return "http" }

// Validate 验证执行配置
func (p *HTTPProvider) Validate(config *tool.ExecuteConfig) error {
	if config.ProviderConfig == nil {
		return fmt.Errorf("provider_config 不能为空")
	}
	if config.ProviderConfig.URL == "" {
		return fmt.Errorf("url 不能为空")
	}
	if config.ProviderConfig.Method == "" {
		return fmt.Errorf("method 不能为空")
	}
	return nil
}

// Execute 执行 HTTP 请求
func (p *HTTPProvider) Execute(ctx context.Context, config *tool.ExecuteConfig) (string, error) {
	pc := config.ProviderConfig

	// 1. 合并所有配置数据（用于模板渲染）
	templateData := make(map[string]interface{})
	for k, v := range config.UserConfig {
		templateData[k] = v
	}
	for k, v := range config.AdminConfig {
		templateData[k] = v
	}
	for k, v := range config.ToolInput {
		templateData[k] = v
	}

	// 2. 渲染 URL
	url, err := renderTemplate(pc.URL, templateData)
	if err != nil {
		return "", fmt.Errorf("渲染 URL 失败: %w", err)
	}
	url = sanitizeURL(url)

	// 3. 渲染 Headers
	headers := make(map[string]string)
	for k, v := range pc.Headers {
		rendered, err := renderTemplate(v, templateData)
		if err != nil {
			return "", fmt.Errorf("渲染 Header 失败: %w", err)
		}
		headers[k] = rendered
	}

	// 4. 设置认证
	if pc.Auth != nil {
		if err := p.setupAuth(headers, pc.Auth, config.UserConfig); err != nil {
			return "", fmt.Errorf("设置认证失败: %w", err)
		}
	}

	// 5. 渲染 Body
	var bodyBytes []byte
	if pc.BodyTemplate != nil {
		body := renderTemplateMap(pc.BodyTemplate, templateData)
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return "", fmt.Errorf("序列化请求体失败: %w", err)
		}
	}

	// 6. 创建请求
	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(pc.Method), url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("创建请求失败: %w", err)
	}

	// 7. 设置 Headers
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	// 8. 发送请求
	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("发送请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 9. 读取响应
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取响应失败: %w", err)
	}

	// 10. 检查 HTTP 状态码
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("HTTP 请求失败: %d %s", resp.StatusCode, resp.Status)
	}

	// 11. 解析响应（根据 response_mapping）
	if pc.ResponseMapping != nil && len(pc.ResponseMapping) > 0 {
		return p.mapResponse(respBody, pc.ResponseMapping)
	}

	return string(respBody), nil
}

// setupAuth 设置认证
func (p *HTTPProvider) setupAuth(headers map[string]string, auth *tool.AuthConfig, userConfig map[string]interface{}) error {
	switch auth.Type {
	case "bearer":
		token, ok := userConfig[auth.TokenField]
		if !ok {
			return fmt.Errorf("缺少认证字段: %s", auth.TokenField)
		}
		headers["Authorization"] = fmt.Sprintf("Bearer %v", token)

	case "api_key":
		apiKey, ok := userConfig["api_key"]
		if !ok {
			return fmt.Errorf("缺少 api_key")
		}
		headerName := auth.Header
		if headerName == "" {
			headerName = "Authorization"
		}
		prefix := auth.Prefix
		headers[headerName] = fmt.Sprintf("%s%v", prefix, apiKey)

	case "basic":
		username, _ := userConfig["username"].(string)
		password, _ := userConfig["password"].(string)
		if username == "" || password == "" {
			return fmt.Errorf("缺少 username 或 password")
		}
		headers["Authorization"] = fmt.Sprintf("Basic %s", basicAuth(username, password))

	default:
		return fmt.Errorf("不支持的认证类型: %s", auth.Type)
	}

	return nil
}

// mapResponse 映射响应
func (p *HTTPProvider) mapResponse(respBody []byte, mapping map[string]string) (string, error) {
	var data interface{}
	if err := json.Unmarshal(respBody, &data); err != nil {
		// 如果不是 JSON，直接返回原文
		return string(respBody), nil
	}

	result := make(map[string]interface{})
	for key, jsonPath := range mapping {
		value, err := getJSONPath(data, jsonPath)
		if err != nil {
			// 路径不存在，跳过该字段
			continue
		}
		result[key] = value
	}

	bytes, err := json.Marshal(result)
	if err != nil {
		return string(respBody), nil
	}

	return string(bytes), nil
}

// renderTemplate 渲染模板字符串
func renderTemplate(template string, data map[string]interface{}) (string, error) {
	result := template
	for key, value := range data {
		placeholder := fmt.Sprintf("{{%s}}", key)
		if strings.Contains(result, placeholder) {
			result = strings.ReplaceAll(result, placeholder, fmt.Sprintf("%v", value))
		}
	}
	return result, nil
}

func sanitizeURL(rawURL string) string {
	return strings.Trim(strings.TrimSpace(rawURL), "` \t\r\n")
}

// renderTemplateMap 渲染模板 Map
func renderTemplateMap(template map[string]interface{}, data map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range template {
		switch val := v.(type) {
		case string:
			rendered, _ := renderTemplate(val, data)
			result[k] = rendered
		case map[string]interface{}:
			result[k] = renderTemplateMap(val, data)
		case []interface{}:
			result[k] = renderTemplateArray(val, data)
		default:
			result[k] = v
		}
	}
	return result
}

// renderTemplateArray 渲染模板数组
func renderTemplateArray(template []interface{}, data map[string]interface{}) []interface{} {
	result := make([]interface{}, len(template))
	for i, v := range template {
		switch val := v.(type) {
		case string:
			rendered, _ := renderTemplate(val, data)
			result[i] = rendered
		case map[string]interface{}:
			result[i] = renderTemplateMap(val, data)
		case []interface{}:
			result[i] = renderTemplateArray(val, data)
		default:
			result[i] = v
		}
	}
	return result
}

// getJSONPath 获取 JSON 路径值
// 支持简单的路径如: $.data.results, $.choices[0].message.content
func getJSONPath(data interface{}, path string) (interface{}, error) {
	// 移除 $ 前缀
	path = strings.TrimPrefix(path, "$")
	path = strings.TrimPrefix(path, ".")

	if path == "" {
		return data, nil
	}

	parts := parseJSONPath(path)
	current := data

	for _, part := range parts {
		switch v := current.(type) {
		case map[string]interface{}:
			val, ok := v[part.Key]
			if !ok {
				return nil, fmt.Errorf("key not found: %s", part.Key)
			}
			current = val

		case []interface{}:
			if part.Index < 0 || part.Index >= len(v) {
				return nil, fmt.Errorf("index out of range: %d", part.Index)
			}
			current = v[part.Index]

		default:
			return nil, fmt.Errorf("invalid path: %s", part.Key)
		}
	}

	return current, nil
}

// jsonPathPart JSON 路径部分
type jsonPathPart struct {
	Key   string
	Index int
}

// parseJSONPath 解析 JSON 路径
// 支持 data.webPages.value[0].name 这类点号分隔路径
func parseJSONPath(path string) []jsonPathPart {
	var parts []jsonPathPart
	remaining := path

	for remaining != "" {
		dotIdx := strings.Index(remaining, ".")
		bracketIdx := strings.Index(remaining, "[")

		// 当前 token 包含索引，如 key[0]
		if bracketIdx != -1 && (dotIdx == -1 || bracketIdx < dotIdx) {
			key := remaining[:bracketIdx]
			if key != "" {
				parts = append(parts, jsonPathPart{Key: key})
			}

			bracketEndIdx := strings.Index(remaining[bracketIdx:], "]")
			if bracketEndIdx == -1 {
				break
			}

			indexStr := remaining[bracketIdx+1 : bracketIdx+bracketEndIdx]
			index := 0
			fmt.Sscanf(indexStr, "%d", &index)
			parts = append(parts, jsonPathPart{Index: index})

			remaining = remaining[bracketIdx+bracketEndIdx+1:]
			remaining = strings.TrimPrefix(remaining, ".")
			continue
		}

		// 普通点号分隔
		if dotIdx != -1 {
			key := remaining[:dotIdx]
			if key != "" {
				parts = append(parts, jsonPathPart{Key: key})
			}
			remaining = remaining[dotIdx+1:]
			continue
		}

		// 最后一段
		if remaining != "" {
			parts = append(parts, jsonPathPart{Key: remaining})
		}
		break
	}

	return parts
}

// basicAuth 生成 Basic Auth
func basicAuth(username, password string) string {
	auth := username + ":" + password
	return base64Encode(auth)
}

// base64Encode Base64 编码
func base64Encode(s string) string {
	const base64Table = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

	result := make([]byte, 0, (len(s)/3+1)*4)
	for i := 0; i < len(s); i += 3 {
		end := i + 3
		if end > len(s) {
			end = len(s)
		}
		chunk := s[i:end]

		var b0, b1, b2 byte
		b0 = chunk[0]
		if len(chunk) > 1 {
			b1 = chunk[1]
		}
		if len(chunk) > 2 {
			b2 = chunk[2]
		}

		result = append(result, base64Table[b0>>2])
		result = append(result, base64Table[(b0&0x03)<<4|b1>>4])
		if len(chunk) > 1 {
			result = append(result, base64Table[(b1&0x0F)<<2|b2>>6])
		} else {
			result = append(result, '=')
		}
		if len(chunk) > 2 {
			result = append(result, base64Table[b2&0x3F])
		} else {
			result = append(result, '=')
		}
	}

	return string(result)
}
