package tool

import (
	"context"
	"encoding/json"
	"fmt"

	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
)

// WebSearchTool 网络搜索工具
// 直接实现 eino tool.InvokableTool 接口
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

// Info 返回工具元数据
func (t *WebSearchTool) Info(ctx context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "web_search",
		Desc: "搜索互联网获取最新信息。当知识库中没有相关信息，或需要最新数据时使用。",
		ParamsOneOf: schema.NewParamsOneOfByJSONSchema(&jsonschema.Schema{
			Type:       "object",
			Properties: buildProperties("query", "string", "搜索关键词"),
			Required:   []string{"query"},
		}),
	}, nil
}

// InvokableRun 执行网络搜索
func (t *WebSearchTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...einoTool.Option) (string, error) {
	var params struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(argumentsInJSON), &params); err != nil {
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
