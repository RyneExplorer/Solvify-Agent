package agent

import (
	"solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
)

// PromptUserContext 描述注入到系统提示词中的用户上下文信息
type PromptUserContext struct {
	ID                 string
	Username           string
	Role               string
	TimeStr            string
	Department         string
	Position           string
	Expertise          string
	Language           string
	Timezone           string
	AnswerStyle        string
	TableFirst         bool
	CitationStyle      string
	RoleTemplatePrompt string
}

// Request 描述 Agent 执行请求
type Request struct {
	CheckpointID     string               // 恢复执行时使用的 checkpointID（恢复场景必填）
	SessionID        string               // 会话 ID（用于 checkpoint 关联）
	UserID           string               // 用户 ID（用于知识库检索权限）
	Query            string               // 原始用户问题
	History          []entity.ChatMessage // 历史对话
	KnowledgeBaseIDs []string             // 知识库 ID 列表
	ModelID          string               // 模型 ID
	ModelType        string               // 模型类型（user/system）
	Summary          *entity.ChatSummary  // 会话摘要（长对话压缩内容）— 保留给调试/日志，System Prompt 注入统一走 SystemPrompt 字段
	Memories         []entity.UserMemory  // 用户长期记忆 — 保留给调试/日志
	UserCtx          PromptUserContext    // 用户基本信息 + 当前时间 — 保留给调试/日志，System Prompt 注入统一走 SystemPrompt 字段
	SystemPrompt     string               // 统一入口注入的完整 System Prompt。不为空时 runAgent 完全信任它，不再内部二次拼接摘要/记忆
	ResumeData       map[string]any       // 恢复执行时的审批数据（key=interruptID, value=用户审批结果）；为 nil 时首次执行
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
	// interrupt 事件字段
	CheckpointID  string         `json:"checkpoint_id,omitempty"`
	InterruptID   string         `json:"interrupt_id,omitempty"`
	InterruptInfo map[string]any `json:"interrupt_info,omitempty"`
	// clarify 事件字段（ask_clarify 触发的中断）
	IsClarify       bool     `json:"is_clarify,omitempty"`
	ClarifyQuestion string   `json:"clarify_question,omitempty"`
	ClarifyOptions  []string `json:"clarify_options,omitempty"`
	ClarifyContext  string   `json:"clarify_context,omitempty"`
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
	EventInterrupt  = "interrupt"   // 执行中断，等待用户审批确认
)
