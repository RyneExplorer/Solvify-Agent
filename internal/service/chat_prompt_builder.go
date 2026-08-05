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

// quickModeAgentSystemPrompt 快速检索模式的 base system prompt。
// 快速模式不走 ReAct 工具循环，而是一次性：QueryRewrite → Retrieve → BuildPrompt → Generate，
// 所以这里只保留"身份/引用规则/回答原则"，不要写 Think/Act/Observe 相关指令。
const quickModeAgentSystemPrompt = `你是 Solvify 知识助理，专业的 AI 知识助手。

## 引用规则
1. 如果"参考资料"块出现在本次对话中（在你的用户问题前面），回答时只引用参考资料中明确陈述的内容。
2. 引用格式：在对应句末插入 <kb doc="文档名" chunk_id="chunk_id" />，chunk_id 必须和参考资料中 [chunk_id=xxx] 完全一致。
3. 引用只允许标签内的 chunk_id/文档名，不要把整段原文复制粘贴出来。
4. 若问题在参考资料中找不到依据，直接说"在当前知识库中没有找到相关信息"，禁止编造、禁止靠常识补全。

## 回答原则
- 先给结论，再展开依据；需要结构化表达时优先使用 Markdown 表格或编号列表。
- 必须使用用户偏好的回答语言，语言不确定时使用简体中文。
- 如果参考资料里有多份互斥或版本差异较大的信息，要在回答中显式标注"不同文档存在差异，请根据实际场景核实"。
- 回答尾部不要自行添加"希望对你有帮助"等客套语。`

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

// BuildSystem 构建统一的增强 System Prompt（基础 + 当前信息 + 摘要 + 记忆 + 角色 + 偏好）
// 快速 / 深度模式都走这里，双模式结构 100% 一致（P1-⑦ 统一 PromptBuilder 单入口）。
//
// 注意：摘要不再伪造一条 assistant 历史消息，改走这里的 System Prompt 注入（P0-① 修复配套），
// 避免模型误把摘要当"自己前一轮说过的话"产生幻觉。
func (b *PromptBuilder) BuildSystem() string {
	var extras []string

	userInfo := "## 当前信息\n"
	if b.userCtx.TimeStr != "" {
		userInfo += "- 当前时间：" + b.userCtx.TimeStr + "\n"
	}
	if b.userCtx.Timezone != "" {
		userInfo += "- 用户时区：" + b.userCtx.Timezone + "\n"
	}
	if b.userCtx.Username != "" {
		userInfo += "- 用户：" + b.userCtx.Username + "\n"
	}
	if b.userCtx.Role != "" {
		userInfo += "- 系统角色：" + b.userCtx.Role + "\n"
	}
	if b.userCtx.Department != "" {
		userInfo += "- 部门：" + b.userCtx.Department + "\n"
	}
	if b.userCtx.Position != "" {
		userInfo += "- 职位：" + b.userCtx.Position + "\n"
	}
	if b.userCtx.Expertise != "" {
		userInfo += "- 擅长/关注：" + b.userCtx.Expertise + "\n"
	}
	if b.userCtx.Language != "" {
		userInfo += "- 偏好语言：" + b.userCtx.Language + "\n"
	}
	if userInfo != "## 当前信息\n" {
		extras = append(extras, userInfo)
	}

	if b.userCtx.AnswerStyle != "" || b.userCtx.TableFirst || b.userCtx.CitationStyle != "" {
		var p strings.Builder
		p.WriteString("## 用户回答偏好\n")
		switch b.userCtx.AnswerStyle {
		case "concise":
			p.WriteString("- 回答风格：简洁凝练，直击要点，3~5 句说完，不过度展开\n")
		case "detailed":
			p.WriteString("- 回答风格：详细展开，先结论再分点论述，必要时给例子和注意事项\n")
		case "step_by_step":
			p.WriteString("- 回答风格：分步讲解，用 1/2/3…编号或小标题组织步骤\n")
		default:
			p.WriteString("- 回答风格：平衡简洁与完整，先结论再展开\n")
		}
		if b.userCtx.TableFirst {
			p.WriteString("- 结构化呈现：对比、列表、映射等数据优先用 Markdown 表格组织\n")
		}
		switch b.userCtx.CitationStyle {
		case "none":
			p.WriteString("- 引用格式：正文不标注引用，引用信息仅由消息底部来源区展示\n")
		case "doc_title_only":
			p.WriteString("- 引用格式：正文引用时只提「根据《文档名》」，不要章节\n")
		default:
			p.WriteString("- 引用格式：正文引用时以「根据《文档名》· 章节标题」形式说明来源\n")
		}
		extras = append(extras, p.String())
	}

	pref := b.preference
	_ = pref
	// 角色模板：优先取"用户画像 Expertise/Department/Position"拼接出角色设定，
	// 因为 UserPreference 和 User 表里没有 RoleTemplatePrompt 字段（属于未实现的阶段3扩展）。
	var rolePrompt string
	if b.profile != nil {
		parts := []string{}
		if strings.TrimSpace(b.profile.Department) != "" {
			parts = append(parts, "所在部门："+strings.TrimSpace(b.profile.Department))
		}
		if strings.TrimSpace(b.profile.Position) != "" {
			parts = append(parts, "职位："+strings.TrimSpace(b.profile.Position))
		}
		if strings.TrimSpace(b.profile.Expertise) != "" {
			parts = append(parts, "擅长领域："+strings.TrimSpace(b.profile.Expertise))
		}
		if len(parts) > 0 {
			rolePrompt = "## 角色定位\n- " + strings.Join(parts, "\n- ")
		}
	}
	if rolePrompt != "" {
		extras = append(extras, rolePrompt)
	}

	if b.userCtx.Language != "" {
		langHint := "## 回答语言\n"
		switch b.userCtx.Language {
		case "en-US":
			langHint += "- 请使用英文回答（美式英语）。\n"
		case "ja-JP":
			langHint += "- 请使用日语回答。\n"
		case "ko-KR":
			langHint += "- 请使用韩语回答。\n"
		case "fr-FR":
			langHint += "- 请使用法语回答。\n"
		case "de-DE":
			langHint += "- 请使用德语回答。\n"
		case "es-ES":
			langHint += "- 请使用西班牙语回答。\n"
		default:
			langHint += "- 请使用简体中文回答。\n"
		}
		extras = append(extras, langHint)
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
// P1-⑦（PromptBuilder 单入口）：System Prompt 由 PromptBuilder.BuildSystem() 统一产出后直接塞到
// agent.Request.SystemPrompt，agent.runAgent 不再二次拼接，保证快速/深度两模式的摘要/记忆/偏好注入完全一致。
func (b *PromptBuilder) BuildAgentRequestFields(userID, query, modelID, modelType string, kbIDs []string, history []entity.ChatMessage) agentpkg.Request {
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
			ID:       b.userCtx.ID,
			Username: b.userCtx.Username,
			Role:     b.userCtx.Role,
			TimeStr:  b.userCtx.TimeStr,
		},
		SystemPrompt: b.BuildSystem(),
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

// takeAnswerStyle 取用户回答风格，空对象返回空串
func takeAnswerStyle(p *entity.UserPreference) string {
	if p == nil {
		return ""
	}
	return p.AnswerStyle
}

// takeTableFirst 取是否优先表格呈现，空对象默认 true
func takeTableFirst(p *entity.UserPreference) bool {
	if p == nil {
		return true
	}
	return p.UseMarkdownTable
}

// takeCitationStyle 取引用格式，空对象默认 section_title
func takeCitationStyle(p *entity.UserPreference) string {
	if p == nil {
		return "section_title"
	}
	return p.CitationStyle
}
