package agent

import (
	"solvify-agent/internal/llm"
	"solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
)

// Request 描述 Agent 执行请求（Service 只做业务编排，Agent 自主决定工具调用）
type Request struct {
	UserID           string               // 用户 ID（用于知识库检索权限）
	Query            string               // 原始用户问题
	History          []entity.ChatMessage // 历史对话
	KnowledgeBaseIDs []string             // 知识库 ID 列表
	LLMClient        llm.Client           // LLM 客户端
}

// Event 描述 Agent SSE 事件
type Event struct {
	Type       string                `json:"type"`
	Content    string                `json:"content,omitempty"`
	ToolCalls  []llm.ToolCall        `json:"tool_calls,omitempty"`
	ToolResult *ToolResult           `json:"tool_result,omitempty"`
	Sources    []response.SourceInfo `json:"sources,omitempty"`
	MessageID  string                `json:"message_id,omitempty"`
	Error      string                `json:"error,omitempty"`
	Done       bool                  `json:"done"`
}

// ToolResult 描述工具执行结果
type ToolResult struct {
	Name    string `json:"name"`
	Content string `json:"content"`
	Error   string `json:"error,omitempty"`
}

// 事件类型常量
const (
	EventThought    = "thought"
	EventToolCall   = "tool_call"
	EventToolResult = "tool_result"
	EventAnswer     = "answer"
	EventSources    = "sources"
	EventProgress   = "progress"
	EventDone       = "done"
	EventError      = "error"
)
