package agent

import (
	"solvify-agent/internal/model/entity"
	apperrors "solvify-agent/pkg/errors"
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

// Profile 决定问答走哪条 Agent 编排路径。
type Profile string

const (
	// ProfileQuick 快速模式：单个 ChatModelAgent，只挂知识检索域工具 + ask_clarify
	ProfileQuick Profile = "quick"
	// ProfileDeep 深度模式：多 Agent 编排（主 Agent + 按工具域拆分的子 Agent）
	ProfileDeep Profile = "deep"
)

// Request 描述 Agent 执行请求
type Request struct {
	citations   *citationRegistry
	ContextData []ContextMaterial `json:",omitempty"`
	RunID       string            // 由程序生成并随checkpoint冻结
	// Profile 问答模式；空值等同 ProfileDeep（保持既有行为不变），判定一律走 effectiveProfile
	Profile           Profile
	CheckpointID      string               // 恢复执行时使用的 checkpointID（恢复场景必填）
	SessionID         string               // 会话 ID（用于 checkpoint 关联）
	UserID            string               // 用户 ID（用于知识库检索权限）
	Query             string               // 原始用户问题
	History           []entity.ChatMessage // 历史对话
	KnowledgeBaseIDs  []string             // 知识库 ID 列表
	UserToolConfigIDs []string             // 用户工具配置 ID 白名单（对话级 MCP 选择）
	// MCPDisabled 表示「用户显式选择了不使用任何 MCP 工具」。
	// 与 UserToolConfigIDs 组合成完整语义（三态），不能只看后者 ——
	// UserToolConfigIDs 为空切片与 nil 都是 len==0，无法区分「没选」与「选了空」：
	//   MCPDisabled=false, ids=nil/空  ⇒ 沿用历史行为，挂全部已启用 MCP
	//   MCPDisabled=false, ids=[a,b]   ⇒ 只挂 a、b
	//   MCPDisabled=true               ⇒ 一个 MCP 都不挂（ids 必须是空）
	MCPDisabled bool
	ModelID     string // 模型 ID
	ModelType   string // 模型类型（user/system）
	// ModelName 是 ModelID 解析后的真实模型名（如 "deepseek-chat"，不是 uuid）。
	// budgetGuard 据此选 token 编码 —— 传 ModelID 会一路 fallback 到 cl100k_base。
	ModelName string
	// MaxContextLength 是用户确认的总窗口，不按模型名或错误猜测。
	InputLimit       int // 模型声明的独立输入上限，纳入快照及压缩水位
	OutputLimit      int // 本次实际默认输出上限，调用级覆盖由最终模型保护处理
	MaxContextLength int
	Summary          *entity.ChatSummary
	Memories         []entity.UserMemory // 用户长期记忆 — 保留给调试/日志
	UserCtx          PromptUserContext   // 用户基本信息 + 当前时间 — 保留给调试/日志，System Prompt 注入统一走 SystemPrompt 字段
	SystemPrompt     string
	ResumeData       map[string]any // 恢复执行时的审批数据（key=interruptID, value=用户审批结果）；为 nil 时首次执行
	// TaskID/AgentName 用于受控深度编排中的任务归属和事件追踪。
	TaskID      string
	AgentName   string
	ToolDomains []ToolDomain
}

type ContextMaterial struct {
	Kind     string
	Content  string
	SourceID string `json:",omitempty"`
}

// effectiveProfile 归一化 Profile：空值按 ProfileDeep 处理。
// 调用点一律用它，禁止直接读 req.Profile —— 那会把「空=深度」这条约定散成第二份来源。
func (r Request) effectiveProfile() Profile {
	if r.Profile == ProfileQuick {
		return ProfileQuick
	}
	return ProfileDeep
}

// toolDomains 该模式应当挂载的工具域。
// 未知 Profile 落全域 —— 与 effectiveProfile 同一口径：拼错值不会静默少给工具。
func (p Profile) toolDomains() []ToolDomain {
	if p == ProfileQuick {
		return QuickToolDomains
	}
	return AllToolDomains
}

// Event 描述 Agent SSE 事件
type Event struct {
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
	Error            string `json:"error,omitempty"`
	// Kind 是失败种类。**仅内部使用**（json:"-" 保证不外泄）：
	// 面向用户的 Title/Detail 一律由 service 层按 Kind × 模式翻译，
	// agent 层不写文案 —— 否则同一句话会有两个来源，且快速/深度共用的路径
	// 必然把深度措辞带进快速模式（缺陷形态，已发生过）。
	Kind      apperrors.Kind `json:"-"`
	Done      bool           `json:"done"`
	Retryable bool           `json:"retryable,omitempty"`
	// ToolResult 保留原始结果，供内部结果判定与来源处理。
	ToolResult string `json:"tool_result,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	ToolName   string `json:"tool_name,omitempty"`
	// interrupt 事件字段
	CheckpointID  string         `json:"checkpoint_id,omitempty"`
	InterruptID   string         `json:"interrupt_id,omitempty"`
	InterruptInfo map[string]any `json:"interrupt_info,omitempty"`
	// clarify 事件字段（ask_clarify 触发的中断）
	IsClarify       bool           `json:"is_clarify,omitempty"`
	ClarifyQuestion string         `json:"clarify_question,omitempty"`
	ClarifyOptions  []string       `json:"clarify_options,omitempty"`
	ClarifyContext  string         `json:"clarify_context,omitempty"`
	PlanID          string         `json:"plan_id,omitempty"`
	PlanVersion     int            `json:"plan_version,omitempty"`
	Phase           string         `json:"phase,omitempty"`
	ReplanReason    string         `json:"replan_reason,omitempty"`
	Round           int            `json:"round,omitempty"`
	TaskID          string         `json:"task_id,omitempty"`
	ParentTaskID    string         `json:"parent_task_id,omitempty"`
	AgentName       string         `json:"agent_name,omitempty"`
	TaskStatus      string         `json:"task_status,omitempty"`
	TaskIndex       int            `json:"task_index,omitempty"`
	TaskTotal       int            `json:"task_total,omitempty"`
	Plan            *ExecutionPlan `json:"plan,omitempty"`
	DependsOn       []string       `json:"depends_on,omitempty"`
}

// 事件类型常量
const (
	EventThinking    = "thinking"    // 思考/分析阶段
	EventToolCall    = "tool_call"   // 工具调用
	EventToolResult  = "tool_result" // 工具结果
	EventWarning     = "warning"     // 警告
	EventError       = "error"       // 错误
	EventAnswer      = "answer"      // 最终答案（纯文本，不含引用标记）
	EventCitation    = "citation"    // 单个引用（后端流式解析时实时发送）
	EventSources     = "sources"     // 来源信息
	EventDone        = "done"        // 完成
	EventInterrupt   = "interrupt"   // 执行中断，等待用户审批确认
	EventPlan        = "plan"        // 结构化任务计划
	EventTaskCreated = "task_created"
	EventTaskUpdated = "task_updated"
	EventTaskResult  = "task_result"
)

const (
	// deepAgentName / quickAgentName 是 Agent 在 eino 里的身份名，会出现在
	// Langfuse 的 trace 上 —— 两个模式同名的话，平台上分不出是哪条链路。
	deepAgentName  = "SolvifyDeepAgent"
	quickAgentName = "SolvifyQuickAgent"
)
