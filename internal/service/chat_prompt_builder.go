package service

import (
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"

	agentpkg "solvify-agent/internal/agent"
	"solvify-agent/internal/model/entity"
)

// PromptMode Prompt Builder 的模式（影响 System Prompt 基础内容）
type PromptMode int

const (
	// PromptModeQuick 快速检索模式（quickModeSystemPrompt）
	PromptModeQuick PromptMode = iota
	// PromptModeDeep 深度思考模式（ReAct 规则作为 base，外部传入）
	PromptModeDeep
)

// PromptBuilder 统一构建 LLM 消息和 System Prompt
// 所有模式（快速检索 / 深度思考）必须通过 Builder 注入 System Prompt 和历史消息，
// 避免两处各写各的导致摘要 / 记忆 / 用户上下文注入行为不一致。
// 阶段二精简：只保留 profile（用户画像 entity.User）、preference（用户偏好 entity.UserPreference）
type PromptBuilder struct {
	mode       PromptMode
	baseSystem string                 // 快速 = quickModeSystemPrompt；深度 = ReAct 规则
	summary    *entity.ChatSummary    // 会话摘要
	memories   []entity.UserMemory    // 用户记忆
	userCtx    UserContext            // 用户基本信息 + 当前时间（保留已有结构）
	profile    *entity.User           // 用户画像实体（扩展字段来源）
	preference *entity.UserPreference // 用户偏好（来源：UserPreference）
}

// NewPromptBuilder 快速模式创建（baseSystem 自动使用 quickModeSystemPrompt）
func NewPromptBuilder(mode PromptMode, baseSystem string, summary *entity.ChatSummary, memories []entity.UserMemory, userCtx UserContext) *PromptBuilder {
	return &PromptBuilder{
		mode:       mode,
		baseSystem: baseSystem,
		summary:    summary,
		memories:   memories,
		userCtx:    userCtx,
	}
}

// WithProfile 绑定用户画像实体（可用于 System Prompt 注入）
func (b *PromptBuilder) WithProfile(u *entity.User) *PromptBuilder {
	b.profile = u
	if u != nil {
		if b.userCtx.Department == "" {
			b.userCtx.Department = u.Department
		}
		if b.userCtx.Position == "" {
			b.userCtx.Position = u.Position
		}
		if b.userCtx.Expertise == "" {
			b.userCtx.Expertise = u.Expertise
		}
		if b.userCtx.Language == "" {
			b.userCtx.Language = u.PreferredLanguage
		}
		if b.userCtx.Timezone == "" {
			b.userCtx.Timezone = u.Timezone
		}
	}
	return b
}

// WithPreference 绑定用户偏好实体
func (b *PromptBuilder) WithPreference(p *entity.UserPreference) *PromptBuilder {
	b.preference = p
	if p != nil {
		if b.userCtx.AnswerStyle == "" {
			b.userCtx.AnswerStyle = p.AnswerStyle
		}
		if !b.userCtx.TableFirst {
			b.userCtx.TableFirst = p.UseMarkdownTable
		}
		if b.userCtx.CitationStyle == "" {
			b.userCtx.CitationStyle = p.CitationStyle
		}
		b.userCtx.AutoDeepMode = p.AutoDeepMode
	}
	return b
}

// BuildSystem 构建统一的增强 System Prompt（基础 + 当前信息 + 摘要 + 记忆）
// 快速 / 深度模式都走这里，双模式结构 100% 一致
func (b *PromptBuilder) BuildSystem() string {
	var extras []string

	userInfo := "## 当前信息\n"
	if b.userCtx.TimeStr != "" {
		userInfo += "- 当前时间：" + b.userCtx.TimeStr + "\n"
	}
	if b.userCtx.Username != "" {
		userInfo += "- 用户：" + b.userCtx.Username + "\n"
	}
	if b.userCtx.Role != "" {
		userInfo += "- 角色：" + b.userCtx.Role + "\n"
	}
	// 至少有时间就加
	if b.userCtx.TimeStr != "" {
		extras = append(extras, userInfo)
	} else if b.userCtx.Username != "" || b.userCtx.Role != "" {
		extras = append(extras, userInfo)
	}

	if b.summary != nil && b.summary.Summary != "" {
		extras = append(extras, "## 本次对话摘要\n"+b.summary.Summary)
	}

	if len(b.memories) > 0 {
		var memoryText strings.Builder
		memoryText.WriteString("## 关于用户的已知信息\n")
		for _, m := range b.memories {
			memoryText.WriteString("- ")
			memoryText.WriteString(m.Content)
			memoryText.WriteString("\n")
		}
		extras = append(extras, memoryText.String())
	}

	if len(extras) == 0 {
		return b.baseSystem
	}
	return b.baseSystem + "\n\n" + strings.Join(extras, "\n\n")
}

// BuildHistory 将 ChatMessage 实体数组转为 eino schema.Message
// 快速 / 深度模式都走这里，role 映射逻辑统一
func (b *PromptBuilder) BuildHistory(history []entity.ChatMessage) []*schema.Message {
	msgs := make([]*schema.Message, 0, len(history))
	for _, msg := range history {
		switch msg.Role {
		case "user":
			msgs = append(msgs, schema.UserMessage(msg.Content))
		case "assistant":
			msgs = append(msgs, schema.AssistantMessage(msg.Content, nil))
		}
	}
	return msgs
}

// BuildHistoryForAgent agent.Request.History 深度模式专用（复用 BuildHistory，语义更清晰）
func (b *PromptBuilder) BuildHistoryForAgent(history []entity.ChatMessage) []entity.ChatMessage {
	return history
}

// BuildAgentRequestFields 深度模式：把 builder 中的摘要 / 记忆 / 用户上下文填充到 agent.Request 对应字段
// 与快速模式调用 BuildMessagesQuick 等价，保证信息一致
func (b *PromptBuilder) BuildAgentRequestFields(userID, query, modelID, modelType string, kbIDs []string, history []entity.ChatMessage) agentpkg.Request {
	profile := b.profile
	pref := b.preference
	return agentpkg.Request{
		UserID:           userID,
		Query:            query,
		History:          history,
		KnowledgeBaseIDs: kbIDs,
		ModelID:          modelID,
		ModelType:        modelType,
		Summary:          b.summary,
		Memories:         b.memories,
		UserCtx: agentpkg.PromptUserContext{
			ID:            b.userCtx.ID,
			Username:      b.userCtx.Username,
			Role:          b.userCtx.Role,
			TimeStr:       b.userCtx.TimeStr,
			Department:    takeStringOrProfile(profile, "Department"),
			Position:      takeStringOrProfile(profile, "Position"),
			Expertise:     takeStringOrProfile(profile, "Expertise"),
			Language:      takeStringOrProfile(profile, "PreferredLanguage"),
			Timezone:      takeStringOrProfile(profile, "Timezone"),
			AnswerStyle:   takeAnswerStyle(pref),
			TableFirst:    takeTableFirst(pref),
			CitationStyle: takeCitationStyle(pref),
		},
	}
}

// takeStringOrProfile 从 Profile entity 取出字段值（或空）
func takeStringOrProfile(u *entity.User, field string) string {
	if u == nil {
		return ""
	}
	switch field {
	case "Department":
		return u.Department
	case "Position":
		return u.Position
	case "Expertise":
		return u.Expertise
	case "PreferredLanguage":
		return u.PreferredLanguage
	case "Timezone":
		return u.Timezone
	}
	return ""
}

// UserContext 注入到 System Prompt 的用户上下文信息
type UserContext struct {
	ID            string
	Username      string
	Role          string
	TimeStr       string
	Department    string
	Position      string
	Expertise     string
	Language      string
	Timezone      string
	AnswerStyle   string
	AutoDeepMode  bool
	TableFirst    bool
	CitationStyle string
}

// NewUserContext 创建用户上下文，TimeStr 使用当前时间
func NewUserContext(user entity.User) UserContext {
	roleText := "普通用户"
	if user.Role == 2 {
		roleText = "管理员"
	}
	return UserContext{
		ID:         user.ID,
		Username:   user.Username,
		Role:       roleText,
		TimeStr:    time.Now().Format("2006-01-02 15:04:05（Monday）"),
		Department: user.Department,
		Position:   user.Position,
		Expertise:  user.Expertise,
		Language:   user.PreferredLanguage,
		Timezone:   user.Timezone,
	}
}

// WithPreference 把用户偏好填充到 UserContext
func (u UserContext) WithPreference(p *entity.UserPreference) UserContext {
	if p == nil {
		return u
	}
	u.AnswerStyle = p.AnswerStyle
	u.AutoDeepMode = p.AutoDeepMode
	u.TableFirst = p.UseMarkdownTable
	u.CitationStyle = p.CitationStyle
	return u
}

func takeAnswerStyle(p *entity.UserPreference) string {
	if p == nil {
		return ""
	}
	return p.AnswerStyle
}
func takeTableFirst(p *entity.UserPreference) bool {
	if p == nil {
		return true
	}
	return p.UseMarkdownTable
}
func takeCitationStyle(p *entity.UserPreference) string {
	if p == nil {
		return "section_title"
	}
	return p.CitationStyle
}
