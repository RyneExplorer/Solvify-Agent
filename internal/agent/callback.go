package agent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components/model"
	toolComp "github.com/cloudwego/eino/components/tool"

	"solvify-agent/pkg/logger"
)

// agentCallbackHandler 实现 eino callbacks.Handler，捕获 Agent 中间事件
type agentCallbackHandler struct {
	eventCh   chan<- Event
	callCount int // 记录 LLM 调用轮次
}

// newAgentCallbackHandler 创建 Agent 回调处理器
func newAgentCallbackHandler(eventCh chan<- Event) callbacks.Handler {
	h := &agentCallbackHandler{eventCh: eventCh}
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
		// 第一轮：正在理解问题；后续轮次：正在分析检索结果
		if h.callCount == 1 {
			h.emit(Event{
				Type:   EventThinking,
				Title:  "正在理解问题",
				Detail: "分析用户问题，决定是否需要检索知识库",
				Status: "running",
			})
		} else {
			h.emit(Event{
				Type:   EventThinking,
				Title:  "正在分析检索结果",
				Detail: "根据检索到的内容，判断信息是否充分",
				Status: "running",
			})
		}

	case "Tools":
		// 工具开始执行，展示具体搜索内容
		toolInput := toolComp.ConvCallbackInput(input)
		toolName := info.Name
		query := ""
		if toolInput != nil {
			query = extractQueryFromArgs(toolInput.ArgumentsInJSON)
		}
		title, detail := formatToolStart(toolName, query)
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
				// LLM 决定调用工具，展示调用计划
				for _, tc := range modelOutput.Message.ToolCalls {
					query := extractQueryFromArgs(tc.Function.Arguments)
					logger.Infof("[Callback] LLM 决定调用: %s(%q)", tc.Function.Name, query)
				}
			}
		}

	case "Tools":
		// 工具执行完成，展示结果摘要
		toolOutput := toolComp.ConvCallbackOutput(output)
		toolName := info.Name
		title, detail := formatToolEnd(toolName, toolOutput)
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
func formatToolStart(toolName, query string) (title, detail string) {
	switch toolName {
	case "knowledge_search":
		if query != "" {
			return "正在检索知识库", fmt.Sprintf("搜索关键词: %s", query)
		}
		return "正在检索知识库", "语义搜索知识库中的相关文档"
	case "web_search":
		if query != "" {
			return "正在联网搜索", fmt.Sprintf("搜索关键词: %s", query)
		}
		return "正在联网搜索", "搜索互联网获取最新信息"
	default:
		return fmt.Sprintf("正在执行 %s", toolName), query
	}
}

// formatToolEnd 格式化工具完成事件
func formatToolEnd(toolName string, output *toolComp.CallbackOutput) (title, detail string) {
	response := ""
	if output != nil {
		response = output.Response
	}

	switch toolName {
	case "knowledge_search":
		// 解析搜索结果，返回找到的文档数
		count := countSearchResults(response)
		if count > 0 {
			return "知识库检索完成", fmt.Sprintf("找到 %d 条相关资料", count)
		}
		return "知识库检索完成", "未找到相关内容"
	case "web_search":
		if response != "" && response != "暂未配置" {
			return "联网搜索完成", "已获取相关信息"
		}
		return "联网搜索不可用", "继续使用知识库信息回答"
	default:
		return fmt.Sprintf("%s 执行完成", toolName), ""
	}
}

// countSearchResults 解析搜索结果 JSON，返回来源数量
func countSearchResults(response string) int {
	var result struct {
		Sources []struct{} `json:"sources"`
	}
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		return 0
	}
	return len(result.Sources)
}

// emit 发送事件（非阻塞）
func (h *agentCallbackHandler) emit(e Event) {
	select {
	case h.eventCh <- e:
	default:
		logger.Warnf("[Callback] 事件通道已满，丢弃事件: type=%s", e.Type)
	}
}
