package response

import "time"

// SessionResponse 描述聊天会话响应
type SessionResponse struct {
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	ModelID   string    `json:"model_id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// MessageResponse 描述聊天消息响应
type MessageResponse struct {
	ID               string       `json:"id"`
	SessionID        string       `json:"session_id"`
	Role             string       `json:"role"`
	Content          string       `json:"content"`
	ModelID          string       `json:"model_id,omitempty"`
	SearchMode       string       `json:"search_mode,omitempty"`
	KnowledgeBaseIDs []string     `json:"knowledge_base_ids,omitempty"`
	Sources          []SourceInfo `json:"sources,omitempty"`
	CreatedAt        time.Time    `json:"created_at"`
}

// SourceInfo 描述引用来源信息（按文档分组）
type SourceInfo struct {
	DocumentID      string        `json:"document_id"`
	KnowledgeBaseID string        `json:"knowledge_base_id"`
	Title           string        `json:"title"`
	Score           float64       `json:"score"`
	Chunks          []ChunkSource `json:"chunks"`
}

// ChunkSource 描述单个分块的引用信息
type ChunkSource struct {
	ID      string  `json:"id"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
}

// StreamEvent 描述 SSE 流式事件
type StreamEvent struct {
	Type       string          `json:"type"`
	Content    string          `json:"content,omitempty"`
	Sources    []SourceInfo    `json:"sources,omitempty"`
	ToolCalls  []ToolCallInfo  `json:"tool_calls,omitempty"`
	ToolResult *ToolResultInfo `json:"tool_result,omitempty"`
	MessageID  string          `json:"message_id,omitempty"`
	Done       bool            `json:"done"`
	Error      string          `json:"error,omitempty"`
}

// ToolCallInfo 描述工具调用信息
type ToolCallInfo struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ToolResultInfo 描述工具执行结果
type ToolResultInfo struct {
	Name    string `json:"name"`
	Content string `json:"content,omitempty"`
	Error   string `json:"error,omitempty"`
}

// ListSessionsResponse 描述会话列表响应
type ListSessionsResponse struct {
	Sessions []SessionResponse `json:"sessions"`
}

// ListMessagesResponse 描述消息列表响应
type ListMessagesResponse struct {
	Messages []MessageResponse `json:"messages"`
}
