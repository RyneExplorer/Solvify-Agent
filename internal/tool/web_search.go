package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// WebSearchTool 网络搜索工具
type WebSearchTool struct {
	apiKey  string
	baseURL string
}

// NewWebSearchTool 创建网络搜索工具
func NewWebSearchTool(apiKey, baseURL string) *WebSearchTool {
	return &WebSearchTool{
		apiKey:  apiKey,
		baseURL: baseURL,
	}
}

func (t *WebSearchTool) Name() string {
	return "web_search"
}

func (t *WebSearchTool) Description() string {
	return "搜索互联网获取最新信息。当知识库中没有相关信息，或需要最新数据时使用。"
}

func (t *WebSearchTool) Parameters() map[string]any {
	return map[string]any{
		"query": map[string]any{
			"type":        "string",
			"description": "搜索关键词",
			"required":    true,
		},
	}
}

func (t *WebSearchTool) StartReport(args string) ProgressReport {
	var params struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(args), &params); err == nil && params.Query != "" {
		return ProgressReport{Title: "正在联网搜索", Detail: params.Query, Status: "running"}
	}
	return ProgressReport{Title: "正在联网搜索", Status: "running"}
}

func (t *WebSearchTool) ResultReport(result string, execErr error) ProgressReport {
	if execErr != nil {
		return ProgressReport{Title: "网络搜索失败", Detail: "继续使用知识库信息回答", Status: "warning"}
	}
	if strings.Contains(result, "暂未配置") || strings.Contains(result, "暂未实现") {
		return ProgressReport{Title: "网络搜索不可用", Detail: "继续使用知识库信息回答", Status: "warning"}
	}
	return ProgressReport{Title: "联网搜索完成", Detail: "已获取相关信息", Status: "success"}
}

func (t *WebSearchTool) Execute(_ context.Context, args string) (string, error) {
	var params struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(args), &params); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}
	if params.Query == "" {
		return "", fmt.Errorf("query 参数不能为空")
	}

	if t.apiKey == "" {
		return "网络搜索功能暂未配置，请联系管理员配置搜索 API。", nil
	}

	// TODO: 对接实际搜索 API（SerpAPI / Bing API）
	return fmt.Sprintf("搜索 %q 的结果暂未实现，请等待后续版本。", params.Query), nil
}
