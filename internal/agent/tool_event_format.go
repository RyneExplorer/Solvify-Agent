package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	toolComp "github.com/cloudwego/eino/components/tool"
)

// ToolResponseData 描述工具返回的 JSON 结构
type ToolResponseData struct {
	Success bool        `json:"success"`
	Message string      `json:"message"`
	Data    interface{} `json:"data"`
}

func parseToolResponse(response string) ToolResponseData {
	var result ToolResponseData
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		return ToolResponseData{Success: false, Message: response}
	}
	return result
}

func parseKnowledgeSearchResult(response string) (titles []string, count int) {
	var result struct {
		Sources []struct {
			Title string `json:"title"`
		} `json:"sources"`
	}
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		return nil, 0
	}

	count = len(result.Sources)
	seen := make(map[string]bool, count)
	for _, s := range result.Sources {
		if s.Title != "" && !seen[s.Title] {
			seen[s.Title] = true
			titles = append(titles, s.Title)
		}
	}
	return titles, count
}

func isWebSearchTool(name, desc string) bool {
	combined := strings.ToLower(name + " " + desc)
	for _, kw := range []string{"web", "search", "搜索", "联网", "tavily", "serp", "bocha", "sogou", "bing"} {
		if strings.Contains(combined, kw) {
			return true
		}
	}
	return false
}

// formatToolStart 根据工具名和查询内容生成 EventToolCall 的 title 和 detail
func formatToolStart(toolName, query string, kbIDs []string, toolDescMap map[string]string) (title, detail string) {
	switch toolName {
	case "knowledge_search":
		if query != "" {
			detail = fmt.Sprintf("查询：%s", query)
		} else {
			detail = "语义搜索知识库"
		}
		if len(kbIDs) > 0 {
			detail += fmt.Sprintf(" | 知识库数：%d", len(kbIDs))
		}
		return "正在检索知识库", detail
	case "grep_chunks":
		if query != "" {
			return "正在关键词搜索", fmt.Sprintf("关键词：%s", query)
		}
		return "正在关键词搜索", "精确匹配文档内容"
	case "get_document_info":
		if query != "" {
			return "正在获取文档信息", fmt.Sprintf("文档ID：%s", query)
		}
		return "正在获取文档信息", "查询文档元数据"
	case "list_knowledge_chunks":
		return "正在列出文档", "获取知识库文档列表"
	case "list_knowledge_bases":
		return "正在列出知识库", "获取用户知识库列表"
	}

	desc := toolDescMap[toolName]
	if isWebSearchTool(toolName, desc) {
		if query != "" {
			return "正在联网搜索", fmt.Sprintf("查询：%s", query)
		}
		return "正在联网搜索", "搜索互联网获取最新信息"
	}

	label := toolName
	if desc != "" {
		label = desc
	}
	if query != "" {
		return fmt.Sprintf("正在执行 %s", label), query
	}
	return fmt.Sprintf("正在执行 %s", label), ""
}

// formatToolEnd 根据工具名和输出生成 EventToolResult 的 title、detail、toolResult
func formatToolEnd(toolName string, output *toolComp.CallbackOutput, toolDescMap map[string]string) (title, detail, toolResult string) {
	response := ""
	if output != nil {
		response = output.Response
		toolResult = response
	}

	switch toolName {
	case "knowledge_search":
		titles, count := parseKnowledgeSearchResult(response)
		if count > 0 {
			detail = fmt.Sprintf("找到 %d 条相关资料", count)
			if len(titles) > 0 {
				if len(titles) > 3 {
					detail += "：" + strings.Join(titles[:3], "、") + " 等"
				} else {
					detail += "：" + strings.Join(titles, "、")
				}
			}
			return "知识库检索完成", detail, toolResult
		}
		return "知识库检索完成", "未找到相关内容", toolResult
	case "grep_chunks":
		result := parseToolResponse(response)
		if result.Success && result.Data != nil {
			if dataList, ok := result.Data.([]interface{}); ok && len(dataList) > 0 {
				return "关键词搜索完成", fmt.Sprintf("找到 %d 条匹配结果", len(dataList)), toolResult
			}
		}
		return "关键词搜索完成", "未找到匹配内容", toolResult
	case "get_document_info":
		result := parseToolResponse(response)
		if result.Success && result.Data != nil {
			return "文档信息获取完成", "已获取文档详细信息", toolResult
		}
		return "文档信息获取完成", result.Message, toolResult
	case "list_knowledge_chunks":
		result := parseToolResponse(response)
		if result.Success && result.Data != nil {
			dataList, ok := result.Data.([]interface{})
			if ok && len(dataList) > 0 {
				return "文档列表获取完成", fmt.Sprintf("找到 %d 个文档", len(dataList)), toolResult
			}
		}
		return "文档列表获取完成", result.Message, toolResult
	case "list_knowledge_bases":
		result := parseToolResponse(response)
		if result.Success && result.Data != nil {
			dataList, ok := result.Data.([]interface{})
			if ok && len(dataList) > 0 {
				return "知识库列表获取完成", fmt.Sprintf("找到 %d 个知识库", len(dataList)), toolResult
			}
		}
		return "知识库列表获取完成", result.Message, toolResult
	}

	desc := toolDescMap[toolName]
	if isWebSearchTool(toolName, desc) {
		if response != "" && response != "暂未配置" {
			return "联网搜索完成", "已获取相关信息", toolResult
		}
		return "联网搜索不可用", "继续使用知识库信息回答", toolResult
	}

	return fmt.Sprintf("%s 执行完成", toolName), "", toolResult
}

func extractQueryFromArgs(args string) string {
	var params struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(args), &params); err == nil {
		return params.Query
	}
	return args
}
