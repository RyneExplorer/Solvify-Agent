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

func parseGrepResponse(response string) ToolResponseData {
	return parseToolResponse(response)
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
		result := parseGrepResponse(response)
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

// formatToolError 根据组件类型和错误生成 EventError 的 title、detail、retryable
func formatToolError(component, name string, err error) (title, detail string, retryable bool) {
	errMsg := err.Error()

	switch component {
	case "Tool":
		if isWebSearchTool(name, "") {
			if strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "超时") {
				return "联网搜索超时", "搜索请求超时，请稍后重试", true
			}
			if strings.Contains(errMsg, "401") || strings.Contains(errMsg, "403") || strings.Contains(errMsg, "认证") || strings.Contains(errMsg, "api_key") {
				return "联网搜索认证失败", "请检查 API Key 配置是否正确", false
			}
			if strings.Contains(errMsg, "429") || strings.Contains(errMsg, "rate") || strings.Contains(errMsg, "限流") {
				return "联网搜索请求过多", "请求频率超限，请稍后重试", true
			}
			if strings.Contains(errMsg, "500") || strings.Contains(errMsg, "502") || strings.Contains(errMsg, "503") {
				return "联网搜索服务异常", "搜索服务暂时不可用，请稍后重试", true
			}
			return "联网搜索失败", "搜索请求失败，请稍后重试", true
		}

		if name == "knowledge_search" {
			return "知识库检索失败", "知识库查询异常，请稍后重试", true
		}
		if name == "grep_chunks" {
			return "关键词搜索失败", "关键词搜索异常，请稍后重试", true
		}
		if name == "get_document_info" {
			return "文档信息获取失败", "文档信息查询异常，请稍后重试", true
		}
		if name == "list_knowledge_chunks" {
			return "文档列表获取失败", "文档列表查询异常，请稍后重试", true
		}
		if name == "list_knowledge_bases" {
			return "知识库列表获取失败", "知识库列表查询异常，请稍后重试", true
		}

		return fmt.Sprintf("%s 执行失败", name), "工具调用失败，请稍后重试", true

	case "ChatModel":
		if strings.Contains(errMsg, "timeout") || strings.Contains(errMsg, "超时") {
			return "AI 服务响应超时", "请稍后重试", true
		}
		if strings.Contains(errMsg, "429") || strings.Contains(errMsg, "rate") {
			return "AI 服务请求过多", "请求频率超限，请稍后重试", true
		}
		return "AI 服务异常", "请稍后重试或选择其他模型", true

	case "Embedding":
		if strings.Contains(errMsg, "未安装") || strings.Contains(errMsg, "not found") {
			return "向量模型未安装", "请先安装或切换可用的 Embedding 模型", false
		}
		if strings.Contains(errMsg, "连接失败") || strings.Contains(errMsg, "connection refused") || strings.Contains(errMsg, "connectex") {
			return "向量服务未连接", "请检查 Embedding 服务是否已启动", true
		}
		return "向量服务异常", "请检查 Embedding 模型配置", true

	default:
		return fmt.Sprintf("%s 执行出错", component), "服务执行异常，请稍后重试", true
	}
}
