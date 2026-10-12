package response

import "time"

// PendingCheckpointInfo 前端恢复审批/澄清状态用
type PendingCheckpointInfo struct {
	CheckpointID  string         `json:"checkpoint_id"`
	InterruptID   string         `json:"interrupt_id"`
	Question      string         `json:"question,omitempty"`
	ToolName      string         `json:"tool_name,omitempty"`
	IsClarify     bool           `json:"is_clarify,omitempty"`
	Options       []string       `json:"options,omitempty"`
	SetAt         time.Time      `json:"set_at"`
	ExecutionPlan *ExecutionPlan `json:"execution_plan,omitempty"`
}

// SessionResponse 描述聊天会话响应
type SessionResponse struct {
	ID                string                 `json:"id"`
	Title             string                 `json:"title"`
	ModelID           string                 `json:"model_id"`
	Status            string                 `json:"status"`
	PendingCheckpoint *PendingCheckpointInfo `json:"pending_checkpoint,omitempty"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
}

// MessageResponse 描述聊天消息响应
type MessageResponse struct {
	ID               string          `json:"id"`
	SessionID        string          `json:"session_id"`
	Role             string          `json:"role"`
	Content          string          `json:"content"`
	ModelID          string          `json:"model_id,omitempty"`
	SearchMode       string          `json:"search_mode,omitempty"`
	KnowledgeBaseIDs []string        `json:"knowledge_base_ids,omitempty"`
	Sources          []SourceInfo    `json:"sources,omitempty"`
	ReasoningSteps   []ReasoningStep `json:"reasoning_steps,omitempty"`
	ExecutionPlan    *ExecutionPlan  `json:"execution_plan,omitempty"`
	CreatedAt        time.Time       `json:"created_at"`
}

type TaskProgress struct {
	ID           string   `json:"id"`
	Agent        string   `json:"agent"`
	Title        string   `json:"title"`
	DependsOn    []string `json:"depends_on,omitempty"`
	Status       string   `json:"status"`
	Summary      string   `json:"summary,omitempty"`
	AddedVersion int      `json:"added_version,omitempty"`
}

type ExecutionPlan struct {
	ID       string         `json:"id"`
	Version  int            `json:"version,omitempty"`
	Phase    string         `json:"phase,omitempty"`
	Reason   string         `json:"replan_reason,omitempty"`
	Round    int            `json:"round,omitempty"`
	Strategy string         `json:"strategy"`
	Status   string         `json:"status"`
	Tasks    []TaskProgress `json:"tasks"`
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
	Quote   string  `json:"quote,omitempty"` // LLM 指出的精确引用句子
	Content string  `json:"content"`
	Score   float64 `json:"score"`
}

// StreamEvent 描述 SSE 流式事件
type StreamEvent struct {
	Type    string       `json:"type"`
	Title   string       `json:"title,omitempty"`
	Detail  string       `json:"detail,omitempty"`
	Status  string       `json:"status,omitempty"`
	Content string       `json:"content,omitempty"`
	Sources []SourceInfo `json:"sources,omitempty"`
	// citation 事件字段
	CitationID       string `json:"citation_id,omitempty"`
	CitationChunkID  string `json:"chunk_id,omitempty"`
	CitationFileName string `json:"file_name,omitempty"`
	CitationContent  string `json:"citation_content,omitempty"`
	MessageID        string `json:"message_id,omitempty"`
	Done             bool   `json:"done"`
	Error            string `json:"error,omitempty"`
	Retryable        bool   `json:"retryable"` // 是否可重试；false 也必须传给前端
	// clarify 事件字段：追问
	Clarify *ClarifyPayload `json:"clarify,omitempty"`
	// interrupt 事件字段：中断等待用户审批
	CheckpointID  string         `json:"checkpoint_id,omitempty"`
	InterruptID   string         `json:"interrupt_id,omitempty"`
	InterruptInfo map[string]any `json:"interrupt_info,omitempty"`
	PlanID        string         `json:"plan_id,omitempty"`
	PlanVersion   int            `json:"plan_version,omitempty"`
	Phase         string         `json:"phase,omitempty"`
	ReplanReason  string         `json:"replan_reason,omitempty"`
	Round         int            `json:"round,omitempty"`
	TaskID        string         `json:"task_id,omitempty"`
	ParentTaskID  string         `json:"parent_task_id,omitempty"`
	AgentName     string         `json:"agent_name,omitempty"`
	TaskStatus    string         `json:"task_status,omitempty"`
	TaskIndex     int            `json:"task_index,omitempty"`
	TaskTotal     int            `json:"task_total,omitempty"`
	Plan          *ExecutionPlan `json:"plan,omitempty"`
	Task          *TaskProgress  `json:"task,omitempty"`
}

// ClarifyPayload 追问事件载体（need 用户补充后才能继续回答）
type ClarifyPayload struct {
	Question string   `json:"question"`
	Options  []string `json:"options,omitempty"`
}

// CitationInfo 描述引用信息（前端 hover 用）
type CitationInfo struct {
	ChunkID  string `json:"chunk_id"`
	FileName string `json:"file_name"`
	Content  string `json:"content"`
}

// ReasoningStep 描述 Agent 推理步骤（用于持久化和历史回显）
type ReasoningStep struct {
	Type    string `json:"type"`              // "thinking" / "tool_call" / "tool_result" / "plan" / "warning"
	Content string `json:"content,omitempty"` // 步骤标题
	Detail  string `json:"detail,omitempty"`  // 步骤详情（如工具查询关键词、检索结果摘要、计划描述等）
	Status  string `json:"status,omitempty"`  // 步骤状态 "running" / "success" / "error"
}

// ListSessionsResponse 描述会话列表响应
type ListSessionsResponse struct {
	Sessions []SessionResponse `json:"sessions"`
}

// ListMessagesResponse 描述消息列表响应
type ListMessagesResponse struct {
	Messages []MessageResponse `json:"messages"`
}
