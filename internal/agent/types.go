package agent

import (
	"strings"

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

// Plan Planner 生成的执行计划
type Plan struct {
	Goal  string   `json:"goal"`
	Steps []string `json:"steps"`
}

// Memory 搜索记忆（请求级生命周期，避免重复搜索）
type Memory struct {
	Searches []string // 已搜索的查询
	Findings []string // 已发现的关键信息摘要
}

// AddSearch 记录一次搜索
func (m *Memory) AddSearch(query string) {
	m.Searches = append(m.Searches, query)
}

// AddFinding 记录一条发现
func (m *Memory) AddFinding(finding string) {
	m.Findings = append(m.Findings, finding)
}

// Summary 返回记忆摘要，用于注入 prompt
func (m *Memory) Summary() string {
	if len(m.Searches) == 0 && len(m.Findings) == 0 {
		return ""
	}
	var sb strings.Builder
	if len(m.Searches) > 0 {
		sb.WriteString("已搜索：" + strings.Join(m.Searches, "、") + "\n")
	}
	if len(m.Findings) > 0 {
		sb.WriteString("已发现：" + strings.Join(m.Findings, "；") + "\n")
	}
	sb.WriteString("\n请避免重复搜索，基于已有信息继续推理。")
	return sb.String()
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
	EventStatus     = "status"       // 推理摘要（替代 EventThought）
	EventToolCall   = "tool_call"    // 工具调用
	EventToolResult = "tool_result"  // 工具结果
	EventAnswer     = "answer"       // 最终答案
	EventSources    = "sources"      // 来源信息
	EventDone       = "done"
	EventError      = "error"
)
