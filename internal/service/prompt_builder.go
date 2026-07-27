package service

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/cloudwego/eino/schema"

	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/rag"
	agentpkg "solvify-agent/internal/agent"
	"solvify-agent/pkg/tokenutil"
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
type PromptBuilder struct {
	mode       PromptMode
	baseSystem string                // 快速 = quickModeSystemPrompt；深度 = ReAct 规则
	summary    *entity.ChatSummary   // 会话摘要
	memories   []entity.UserMemory   // 用户记忆
	userCtx    UserContext           // 用户基本信息 + 当前时间
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

// BuildQuickFinalUserMessage 快速检索模式：把 RAG 上下文 + 用户问题组装成最终 user 消息
func (b *PromptBuilder) BuildQuickFinalUserMessage(question string, retrieveResult rag.Result, retrievalBudget int) *schema.Message {
	if retrieveResult.Hit {
		contextText := BuildContextText(retrieveResult.Documents, retrievalBudget)
		questionText := fmt.Sprintf("%s---\n\n**问题**：%s", contextText, question)
		return schema.UserMessage(questionText)
	}
	questionText := fmt.Sprintf("**问题**：%s\n\n知识库中未找到相关内容。请先说明未命中，再按系统设定谨慎用通用知识回答；若是身份/能力类问题，按身份说明直接回答。", question)
	return schema.UserMessage(questionText)
}

// BuildMessagesQuick 快速检索模式一键组装完整消息数组 = System + History + 最终用户问题（带 RAG）
func (b *PromptBuilder) BuildMessagesQuick(history []entity.ChatMessage, question string, retrieveResult rag.Result, retrievalBudget int) []*schema.Message {
	systemPrompt := b.BuildSystem()
	messages := []*schema.Message{schema.SystemMessage(systemPrompt)}
	messages = append(messages, b.BuildHistory(history)...)
	messages = append(messages, b.BuildQuickFinalUserMessage(question, retrieveResult, retrievalBudget))
	return messages
}

// BuildAgentRequestFields 深度模式：把 builder 中的摘要 / 记忆 / 用户上下文填充到 agent.Request 对应字段
// 与快速模式调用 BuildMessagesQuick 等价，保证信息一致
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
	}
}

// 为避免 agent 循环导入 + 保留函数式调用接口，这里提供两个纯函数：
// BuildContextText / TruncateByTokens 对外暴露（旧代码仍可通过函数式调用）

const maxContextTokensBuilder = 3000

// BuildContextText 按 token 预算组装知识库上下文（按 score 先高分，截断优先低分 chunk）
func BuildContextText(docs []rag.Document, retrievalBudget int) string {
	if retrievalBudget <= 0 {
		retrievalBudget = maxContextTokensBuilder
	}
	header := "## 知识库检索结果\n\n"
	budget := retrievalBudget - tokenutil.Estimate(header)
	if budget < 200 {
		budget = 200
	}

	var body string
	used := 0
	for _, doc := range docs {
		chunk := fmt.Sprintf("### %s\n\n%s\n\n", doc.Title, doc.Content)
		cost := tokenutil.Estimate(chunk)
		if used+cost > budget {
			remain := budget - used
			if remain < 80 {
				break
			}
			truncated := truncateStringByTokens(chunk, remain)
			body += truncated + "\n\n（参考资料过长，已截断）\n\n"
			break
		}
		body += chunk
		used += cost
	}
	return header + body
}

// truncateStringByTokens 按估算 token 预算截断字符串（按 rune，避免半个 UTF-8）
func truncateStringByTokens(text string, maxTokens int) string {
	if maxTokens <= 0 {
		return ""
	}
	runes := []rune(text)
	var total int
	cut := 0
	for i, r := range runes {
		var w float64
		switch {
		case r >= 0x4e00 && r <= 0x9fff:
			w = 1.5
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			w = 0.25
		default:
			w = 0.5
		}
		if total+int(w) > maxTokens {
			break
		}
		total += int(w)
		cut = i + 1
	}
	if cut == 0 {
		return ""
	}
	return string(runes[:cut])
}

// ==============================
//  结构化改写 RewrittenQuery DTO
// ==============================

// RewrittenQuery 改写 + 意图识别 + 关键词扩展 统一输出结构
// 原 rewriteQuery 返回 string，改为返回此结构，下游三个组件全复用，避免三次独立逻辑
type RewrittenQuery struct {
	// MainQuery 改写后的主查询（独立完整的检索查询）
	MainQuery string `json:"main_query"`
	// ExpandedQueries 0~3 个同义扩展查询（多路并行检索，合并去重）
	ExpandedQueries []string `json:"expanded_queries"`
	// Keywords 同义词归一化后的关键词（供历史消息 SearchRecentByKeywords 做 ILIKE 召回）
	Keywords []string `json:"keywords"`
	// Intent 意图识别结果（分流：闲聊直接回复、通用跳过改写、知识库走检索）
	// 与 intent_analyzer.go 的 Intent 类型/常量保持一致（greeting/chat/general/knowledge 等）
	Intent Intent `json:"intent"`
	// Rewritten 本次是否做了改写（false 表示原问题独立完整，可直接用 MainQuery=原问题）
	Rewritten bool `json:"rewritten"`
	// Confidence 置信度 0~1（<0.6 回退为未改写，避免 LLM 胡改）
	Confidence float32 `json:"confidence"`
}

// rewriteStopwords 关键词后处理停用词（从 keywords 里剔除，避免 ILIKE 匹配大量无关）
var rewriteStopwords = map[string]struct{}{
	"的": {}, "了": {}, "吗": {}, "呢": {}, "啊": {}, "吧": {}, "呀": {}, "嗯": {},
	"是": {}, "有": {}, "在": {}, "和": {}, "与": {}, "或": {}, "给": {}, "把": {},
	"我": {}, "你": {}, "他": {}, "她": {}, "它": {}, "我们": {}, "你们": {}, "他们": {},
	"什么": {}, "怎么": {}, "如何": {}, "为什么": {}, "哪些": {}, "多少": {}, "几个": {}, "那个": {}, "这个": {},
	"可以": {}, "需要": {}, "是否": {}, "能否": {}, "应该": {}, "请": {}, "请问": {},
	"一个": {}, "一下": {}, "一点": {}, "这些": {}, "那些": {},
	"the": {}, "a": {}, "an": {}, "is": {}, "are": {}, "was": {}, "were": {},
	"what": {}, "how": {}, "why": {}, "when": {}, "where": {}, "which": {}, "who": {},
	"can": {}, "could": {}, "should": {}, "would": {}, "please": {},
}

// 提取 JSON 的最大匹配子串，兼容 LLM 偶尔吐出 ```json 代码块 / 前后废话
var jsonExtractRe = regexp.MustCompile(`\{[\s\S]*\}`)

// PostProcess 解析后做：JSON 兜底提取 + 停用词过滤 + 去重 + Confidence 回退
func (r *RewrittenQuery) PostProcess(rawLLMContent string, originalQuestion string) {
	content := strings.TrimSpace(rawLLMContent)

	// Step1: 直接 parse JSON；失败则用正则抽取最大 {...} 子串再 parse
	if err := json.Unmarshal([]byte(content), r); err != nil {
		if sub := jsonExtractRe.FindString(content); sub != "" {
			_ = json.Unmarshal([]byte(sub), r)
		}
	}

	// Step2: 任何解析异常都保证 MainQuery 至少是原问题
	if strings.TrimSpace(r.MainQuery) == "" {
		r.MainQuery = originalQuestion
		r.Rewritten = false
		r.Confidence = 1.0
	}

	// Step3: Intent 兜底（与 intent_analyzer.go 保持一致；LLM 说 knowledge/general 时统一归为 IntentQuestion）
	switch r.Intent {
	case IntentGreeting, IntentIdentity, IntentMeta, IntentChitchat, IntentListQuery, IntentQuestion:
	default:
		r.Intent = IntentQuestion
	}

	// Step4: keywords 去停用词 + 去重 + 空串过滤 + 最小长度 2 rune
	seen := map[string]struct{}{}
	cleaned := make([]string, 0, len(r.Keywords))
	for _, kw := range r.Keywords {
		k := strings.TrimSpace(kw)
		if k == "" {
			continue
		}
		if _, stop := rewriteStopwords[strings.ToLower(k)]; stop {
			continue
		}
		if len([]rune(k)) < 2 {
			continue
		}
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		cleaned = append(cleaned, k)
	}
	r.Keywords = cleaned

	// Step5: ExpandedQueries 去重去空（保留顺序）
	seenQ := map[string]struct{}{}
	cleanQ := make([]string, 0, len(r.ExpandedQueries))
	for _, q := range r.ExpandedQueries {
		q = strings.TrimSpace(q)
		if q == "" {
			continue
		}
		if _, ok := seenQ[q]; ok {
			continue
		}
		seenQ[q] = struct{}{}
		cleanQ = append(cleanQ, q)
	}
	r.ExpandedQueries = cleanQ

	// Step6: 置信度过低 -> 回退原问题，不改写
	if r.Confidence > 0 && r.Confidence < 0.6 {
		r.MainQuery = originalQuestion
		r.Rewritten = false
		r.ExpandedQueries = nil
	}
}

// FallbackOriginal 出错时快速回退（返回原问题标记未改写、知识库意图 = IntentQuestion）
func FallbackOriginalRewritten(question string) RewrittenQuery {
	kws := extractKeywordsFallback(question)
	return RewrittenQuery{
		MainQuery:  question,
		Keywords:   kws,
		Intent:     IntentQuestion,
		Rewritten:  false,
		Confidence: 1.0,
	}
}

// extractKeywordsFallback 当 LLM 结构化改写失败时的兜底关键词提取（与旧 extractKeywords 规则一致，但复用已有实现）
func extractKeywordsFallback(query string) []string {
	// 直接复用 context_service.go 的包级 tokenRegexp + 停用词表逻辑
	matches := tokenRegexp.FindAllString(query, -1)
	seen := map[string]struct{}{}
	var result []string
	for _, m := range matches {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		if _, stop := rewriteStopwords[strings.ToLower(m)]; stop {
			continue
		}
		if len([]rune(m)) < 2 {
			continue
		}
		if _, ok := seen[m]; ok {
			continue
		}
		seen[m] = struct{}{}
		result = append(result, m)
	}
	return result
}

// BuildRewritePrompt 统一构造改写 LLM 输入消息
// 如果传了 summary，会放「会话摘要」段；否则只喂最近 6 轮（处理长跨度指代）
func BuildRewritePrompt(history []entity.ChatMessage, question string, summary *entity.ChatSummary) []*schema.Message {
	var parts []string

	// 1) 摘要兜底（如果有）：解决长跨度指代 / 早期决策 / 跨主题回溯
	if summary != nil && strings.TrimSpace(summary.Summary) != "" {
		parts = append(parts, "【会话摘要】\n"+strings.TrimSpace(summary.Summary))
	}

	// 2) 最近对话：超过 6 轮就取最后 6 轮
	dialogue := history
	if len(dialogue) > 6 {
		dialogue = dialogue[len(dialogue)-6:]
	}
	if len(dialogue) > 0 {
		var sb strings.Builder
		sb.WriteString("【最近对话】\n")
		for _, m := range dialogue {
			switch m.Role {
			case "user":
				sb.WriteString("用户: ")
			case "assistant":
				sb.WriteString("助手: ")
			}
			sb.WriteString(strings.TrimSpace(m.Content))
			sb.WriteString("\n")
		}
		parts = append(parts, sb.String())
	}

	// 3) 当前问题
	parts = append(parts, fmt.Sprintf("【当前用户问题】\n%s", question))

	systemText := `你是「查询改写 + 意图识别 + 关键词扩展」助手。
根据【会话摘要】(如有) + 【最近对话】 + 【当前用户问题】，输出严格合法的 JSON：
{
  "main_query": "独立完整的检索查询。代词/省略要换成具体名词；已独立则=原问题",
  "expanded_queries": ["同义扩展0", "同义扩展1"],
  "keywords": ["关键词1", "关键词2", "同义词归一化后"],
  "intent": "knowledge | general | chat | greeting",
  "rewritten": true/false,
  "confidence": 0.95
}
意图分类规则：
- knowledge(知识库问答)：问公司制度/流程/产品/业务等内部知识，需检索
- general(通用问题)：科学常识/方法原理等，可不用知识库
- chat(闲聊)：非工作闲聊、吐槽
- greeting(问候)：你好/谢谢/再见/早 等
其他规则：
1. expanded_queries 0~3 个，用于多路检索；不要杜撰不存在的实体
2. keywords 做同义词归一：对话里说"签字角色"，问题说"审批人"，两个都放
3. 没改写 confidence 给 0.99；明确改写给 >0.9；不太确定给 0.5~0.7
4. 只输出 JSON，不要 markdown、代码块、解释文字`

	userContent := strings.Join(parts, "\n\n")
	return []*schema.Message{
		schema.SystemMessage(systemText),
		schema.UserMessage(userContent),
	}
}
