package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
	toolComp "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/pkg/logger"
)

// agentCallbackHandler 实现 eino callbacks.Handler，捕获 Agent 中间事件
type agentCallbackHandler struct {
	eventCh              chan<- Event
	callCount            int               // 记录 LLM 调用轮次
	pendingThinkingTitle string            // 当前正在运行的思考阶段标题（用于后续标记完成）
	kbIDs                []string          // 当前请求的知识库 ID 列表（用于展示检索上下文）
	toolDescMap          map[string]string // 工具名 → 描述（用于 formatToolStart 判断工具类别）
}

// newAgentCallbackHandler 创建 Agent 回调处理器
func newAgentCallbackHandler(eventCh chan<- Event, kbIDs []string, toolDescMap map[string]string) callbacks.Handler {
	h := &agentCallbackHandler{eventCh: eventCh, kbIDs: kbIDs, toolDescMap: toolDescMap}
	return callbacks.NewHandlerBuilder().
		OnStartFn(h.onStart).
		OnEndFn(h.onEnd).
		OnErrorFn(h.onError).
		Build()
}

// onStart 组件开始执行时的回调
func (h *agentCallbackHandler) onStart(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
	if info == nil {
		return ctx
	}

	switch info.Component {
	case "ChatModel":
		h.callCount++
		// 完成上一个思考阶段（如果存在），实现 running → success 生命周期
		h.completeThinking()

		if h.callCount == 1 {
			h.pendingThinkingTitle = "分析问题"
			h.emit(Event{
				Type:   EventThinking,
				Title:  "分析问题",
				Detail: "理解用户意图，确定检索方向",
				Status: "running",
			})
		} else {
			h.pendingThinkingTitle = "分析检索结果"
			h.emit(Event{
				Type:   EventThinking,
				Title:  "分析检索结果",
				Detail: "评估检索内容，决定下一步行动",
				Status: "running",
			})
		}

	case "Tool":
		// 工具开始执行，展示具体搜索内容
		toolInput := toolComp.ConvCallbackInput(input)
		toolName := info.Name
		query := ""
		if toolInput != nil {
			query = extractQueryFromArgs(toolInput.ArgumentsInJSON)
		}
		title, detail := formatToolStart(toolName, query, h.kbIDs, h.toolDescMap)
		h.emit(Event{
			Type:   EventToolCall,
			Title:  title,
			Detail: detail,
			Status: "running",
		})
	}

	return ctx
}

// onEnd 组件执行完成时的回调
func (h *agentCallbackHandler) onEnd(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
	if info == nil {
		return ctx
	}

	switch info.Component {
	case "ChatModel":
		modelOutput := model.ConvCallbackOutput(output)
		if modelOutput != nil && modelOutput.Message != nil {
			if len(modelOutput.Message.ToolCalls) > 0 {
				// LLM 决定调用工具 —— 思考阶段结束，展示执行计划
				for _, tc := range modelOutput.Message.ToolCalls {
					query := extractQueryFromArgs(tc.Function.Arguments)
					logger.Infof("[Callback] LLM 决定调用: %s(%q)", tc.Function.Name, query)
				}
				h.completeThinking()
				h.emitPlan(modelOutput.Message.ToolCalls)
			} else {
				// LLM 未产生工具调用，说明已准备生成最终答案
				h.completeThinking()
				h.pendingThinkingTitle = "正在生成答案"
				h.emit(Event{
					Type:   EventThinking,
					Title:  "正在生成答案",
					Status: "running",
				})
			}
		}

	case "Tool":
		// 工具执行完成，展示结果摘要
		toolOutput := toolComp.ConvCallbackOutput(output)
		toolName := info.Name
		title, detail := formatToolEnd(toolName, toolOutput, h.toolDescMap)
		h.emit(Event{
			Type:   EventToolResult,
			Title:  title,
			Detail: detail,
			Status: "success",
		})
	}

	return ctx
}

// onError 组件执行出错时的回调
func (h *agentCallbackHandler) onError(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
	if info == nil {
		return ctx
	}
	logger.Errorf("[Callback] 组件出错: node=%s, component=%s, err=%v", info.Name, info.Component, err)
	h.emit(Event{
		Type:   EventError,
		Title:  fmt.Sprintf("%s 执行出错", info.Component),
		Detail: err.Error(),
		Status: "error",
	})
	return ctx
}

// formatToolStart 格式化工具开始事件
//
// toolDescMap 为可选参数（用户工具名 → 描述），用于识别工具类别（搜索/联网/HTTP 等）
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
	}

	// 用户配置的外部工具：根据描述判断是否为联网搜索类
	desc := toolDescMap[toolName]
	if isWebSearchTool(toolName, desc) {
		if query != "" {
			return "正在联网搜索", fmt.Sprintf("查询：%s", query)
		}
		return "正在联网搜索", "搜索互联网获取最新信息"
	}

	// 兜底：显示工具名 + 简短描述
	label := toolName
	if desc != "" {
		label = desc
	}
	if query != "" {
		return fmt.Sprintf("正在执行 %s", label), query
	}
	return fmt.Sprintf("正在执行 %s", label), ""
}

// isWebSearchTool 判断工具是否为联网搜索类（基于工具名或描述关键词）
func isWebSearchTool(name, desc string) bool {
	combined := strings.ToLower(name + " " + desc)
	for _, kw := range []string{"web", "search", "搜索", "联网", "tavily", "serp", "bocha", "sogou", "bing"} {
		if strings.Contains(combined, kw) {
			return true
		}
	}
	return false
}

// formatToolEnd 格式化工具完成事件
func formatToolEnd(toolName string, output *toolComp.CallbackOutput, toolDescMap map[string]string) (title, detail string) {
	response := ""
	if output != nil {
		response = output.Response
	}

	switch toolName {
	case "knowledge_search":
		titles, count := parseSearchResultTitles(response)
		if count > 0 {
			detail = fmt.Sprintf("找到 %d 条相关资料", count)
			if len(titles) > 0 {
				if len(titles) > 3 {
					detail += "：" + strings.Join(titles[:3], "、") + " 等"
				} else {
					detail += "：" + strings.Join(titles, "、")
				}
			}
			return "知识库检索完成", detail
		}
		return "知识库检索完成", "未找到相关内容"
	}

	// 用户配置的外部工具：根据描述判断类别
	desc := toolDescMap[toolName]
	if isWebSearchTool(toolName, desc) {
		if response != "" && response != "暂未配置" {
			return "联网搜索完成", "已获取相关信息"
		}
		return "联网搜索不可用", "继续使用知识库信息回答"
	}

	return fmt.Sprintf("%s 执行完成", toolName), ""
}

// parseSearchResultTitles 解析搜索结果 JSON，返回文档标题列表和总数
func parseSearchResultTitles(response string) (titles []string, count int) {
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

// emitPlan 根据模型决定的工具调用生成执行计划事件
func (h *agentCallbackHandler) emitPlan(toolCalls []schema.ToolCall) {
	var parts []string
	for _, tc := range toolCalls {
		query := extractQueryFromArgs(tc.Function.Arguments)
		switch tc.Function.Name {
		case "knowledge_search":
			kbInfo := ""
			if len(h.kbIDs) > 0 {
				kbInfo = fmt.Sprintf("（共 %d 个知识库）", len(h.kbIDs))
			}
			if query != "" {
				parts = append(parts, fmt.Sprintf("检索知识库：%s %s", query, kbInfo))
			} else {
				parts = append(parts, fmt.Sprintf("语义搜索知识库 %s", kbInfo))
			}
		default:
			// 用户工具：同样用 isWebSearchTool 判断类别
			desc := h.toolDescMap[tc.Function.Name]
			if isWebSearchTool(tc.Function.Name, desc) {
				if query != "" {
					parts = append(parts, fmt.Sprintf("联网搜索：%s", query))
				} else {
					parts = append(parts, "联网搜索获取最新信息")
				}
			} else if query != "" {
				parts = append(parts, fmt.Sprintf("调用 %s：%s", tc.Function.Name, query))
			} else {
				parts = append(parts, fmt.Sprintf("调用 %s", tc.Function.Name))
			}
		}
	}
	detail := strings.Join(parts, "；")
	if detail == "" {
		detail = "准备执行工具调用"
	}
	h.emit(Event{
		Type:   EventPlan,
		Title:  "制定执行计划",
		Detail: detail,
		Status: "success",
	})
}

// completeThinking 将当前运行的思考阶段标记为完成（running → success）
func (h *agentCallbackHandler) completeThinking() {
	if h.pendingThinkingTitle == "" {
		return
	}
	h.emit(Event{
		Type:   EventThinking,
		Title:  h.pendingThinkingTitle,
		Status: "success",
	})
	h.pendingThinkingTitle = ""
}

// emit 发送事件（非阻塞）
func (h *agentCallbackHandler) emit(e Event) {
	logger.Infof("[Callback] 发送事件: type=%s, title=%s, status=%s, detail=%s",
		e.Type, e.Title, e.Status, truncateStr(e.Detail, 60))
	select {
	case h.eventCh <- e:
	default:
		logger.Warnf("[Callback] ⚠️ 事件通道已满，丢弃事件: type=%s, title=%s", e.Type, e.Title)
	}
}
