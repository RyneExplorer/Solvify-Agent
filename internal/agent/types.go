package agent

import (
	"solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
)

type PromptUserContext struct {
	ID       string
	Username string
	Role     string
	TimeStr  string
}

// Request 描述 Agent 执行请求
type Request struct {
	UserID           string               // 用户 ID（用于知识库检索权限）
	Query            string               // 原始用户问题
	History          []entity.ChatMessage // 历史对话
	KnowledgeBaseIDs []string             // 知识库 ID 列表
	ModelID          string               // 模型 ID
	ModelType        string               // 模型类型（user/system）
	Summary          *entity.ChatSummary  // 会话摘要（长对话压缩内容）
	Memories         []entity.UserMemory  // 用户长期记忆（偏好/事实/约束/决策）
	UserCtx          PromptUserContext    // 用户基本信息 + 当前时间
}

// Event 描述 Agent SSE 事件
type Event struct {
	Type    string                `json:"type"`
	Title   string                `json:"title,omitempty"`
	Detail  string                `json:"detail,omitempty"`
	Status  string                `json:"status,omitempty"`
	Content string                `json:"content,omitempty"`
	Sources []response.SourceInfo `json:"sources,omitempty"`
	// citation 事件字段
	CitationID       string `json:"citation_id,omitempty"`
	CitationChunkID  string `json:"chunk_id,omitempty"`
	CitationFileName string `json:"file_name,omitempty"`
	CitationContent  string `json:"citation_content,omitempty"`
	MessageID        string `json:"message_id,omitempty"`
	Error            string `json:"error,omitempty"`
	Done             bool   `json:"done"`
	Retryable        bool   `json:"retryable,omitempty"`
	// ToolResult 工具调用结果（完整内容，供前端展示）
	ToolResult string `json:"tool_result,omitempty"`
}

// 事件类型常量
const (
	EventThinking   = "thinking"    // 思考/分析阶段
	EventToolCall   = "tool_call"   // 工具调用
	EventToolResult = "tool_result" // 工具结果
	EventWarning    = "warning"     // 警告
	EventError      = "error"       // 错误
	EventAnswer     = "answer"      // 最终答案（纯文本，不含引用标记）
	EventCitation   = "citation"    // 单个引用（后端流式解析时实时发送）
	EventSources    = "sources"     // 来源信息
	EventDone       = "done"        // 完成
)
