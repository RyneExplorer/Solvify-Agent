package errors

import (
	stderrors "errors"
	"sort"
	"strings"
)

// Kind 描述「一次失败属于哪一类」。**这是判定失败原因的唯一出口。**
//
// 为什么需要它：此前判定失败靠两处互不相干的字符串黑名单，而面向用户的文案另有 6 处
// 内联写在 agent 层。三个后果都真实发生过：
//
//	文案两个来源：agent 层写死的那句与 service 层映射表里的那句各说各话，
//	              且都没有「模式」维度 ⇒ 快速模式的失败显示了深度模式的措辞
//	裸词误伤：同一个 "exceeds" 既被当成上下文超长、又被 eino 用来报迭代超限
//	          ⇒ 迭代超限被误判成上下文超长，模型有效窗口被从 64000 悄悄砍到 8192
//
// 把「这是什么失败」变成可判定的事实（Kind），文本匹配退居兜底，
// 文案统一由 service 层按「Kind × 模式」取 —— 本包不含任何面向用户的文案。
type Kind string

const (
	// KindUnknown 表示没能判定出原因。调用方给出的兜底文案。
	KindUnknown Kind = "unknown"

	// ── 模型配置 ──
	KindModelConfig            Kind = "model_config"
	KindModelConfigQuery       Kind = "model_config_query"
	KindSystemModelUnavailable Kind = "system_model_unavailable"
	KindModelTypeUnsupported   Kind = "model_type_unsupported"

	// ── 上下文装配 ──
	KindKnowledgeSearch Kind = "knowledge_search"
	KindHistoryLoad     Kind = "history_load"
	KindContextBudget   Kind = "context_budget"

	// ── 模型服务端（上游 HTTP 状态码 / 响应正文）──
	KindModelUnavailable Kind = "model_unavailable"
	KindRateLimited      Kind = "rate_limited"
	KindContextLength    Kind = "context_length"
	KindTimeout          Kind = "timeout"
	KindLLMCallFailed    Kind = "llm_call_failed"
	KindLLMStreamFailed  Kind = "llm_stream_failed"

	// ── Agent 执行 ──
	KindAgentInitFailed   Kind = "agent_init_failed"
	KindAgentRunFailed    Kind = "agent_run_failed"
	KindAgentStreamFailed Kind = "agent_stream_failed"
	// KindMaxIterations 达到迭代轮次上限。由 eino 的 adk.ErrExceedMaxIterations 结构化判定，
	// 不靠文本 —— 这个错误曾经掉进 KindContextLength，代价是模型窗口被砍。
	KindMaxIterations   Kind = "max_iterations"
	KindToolUnsupported Kind = "tool_unsupported"
	// KindToolNotInvoked 模型没有真正发起工具调用（只回了一段执行计划）。
	KindToolNotInvoked Kind = "tool_not_invoked"
	KindResumeFailed   Kind = "resume_failed"
	KindEmptyAnswer    Kind = "empty_answer"
	KindUnhandled      Kind = "unhandled"

	// ── 会话 ──
	KindSessionInvalid Kind = "session_invalid"
	KindSessionClosed  Kind = "session_closed"

	// ── 工具 / 外部服务 ──
	KindToolLoadFailed        Kind = "tool_load_failed"
	KindToolCallFailed        Kind = "tool_call_failed"
	KindWebSearchFailed       Kind = "web_search_failed"
	KindWebSearchTimeout      Kind = "web_search_timeout"
	KindWebSearchAuthFailed   Kind = "web_search_auth_failed"
	KindExternalRequestFailed Kind = "external_request_failed"
)

// kindSentinelEntry 把「一个哨兵错误」绑定到它所属的种类。
type kindSentinelEntry struct {
	kind     Kind
	sentinel error
}

// kindSentinels 收集「结构化可判定」的失败种类。
//
// 由持有该哨兵的层在 init 阶段登记（例：internal/agent 登记 eino 的 adk.ErrExceedMaxIterations），
// 本包因此不必依赖任何框架，与 notFoundSentinels 同一分层约定。
//
// ⚠️ 结构化判定永远优先于文本特征。同一个错误既能被哨兵命中、又能被某个宽泛的特征串
// 命中时，一律以哨兵为准 —— 这正是「裸词误伤」不再可能的原因。
var kindSentinels []kindSentinelEntry

// RegisterKindSentinel 登记「属于某失败种类」的哨兵错误。仅应在 init 阶段调用。
func RegisterKindSentinel(kind Kind, sentinels ...error) {
	for _, sentinel := range sentinels {
		if sentinel == nil {
			continue
		}
		kindSentinels = append(kindSentinels, kindSentinelEntry{kind: kind, sentinel: sentinel})
	}
}

// kindTextFeature 把「一个特征串」绑定到它所属的种类。
type kindTextFeature struct {
	kind   Kind
	needle string
}

// kindTextFeatures 是「文本 → 种类」的兜底表，只在没有任何哨兵命中时使用。
//
// 上游很多失败没有可判定的哨兵（模型服务端只回一句 HTTP 400/429/503 的正文），
// 只能按特征串识别。此前这些特征串散在两处（service 的文案映射表、llm 的上下文超长
// 关键词表），同一个失败在两处可以得出不同结论 —— 现在只有这一处。
//
// ⚠️ 不要往里加裸词。`"exceeds"` 这类词会同时命中「exceeds max iterations」，
// 把迭代超限误判成上下文超长。加特征前先问：它会不会命中别的种类的报错？
// 能用哨兵判定的（如 eino 的迭代超限）一律走 RegisterKindSentinel，不要写在这里。
var kindTextFeatures = []kindTextFeature{
	// 模型配置
	{KindModelConfig, "模型配置无效或无权访问"},
	{KindModelConfigQuery, "查询用户模型配置失败"},
	{KindSystemModelUnavailable, "查询系统模型失败"},
	{KindModelTypeUnsupported, "不支持的模型类型"},

	// 上下文装配
	{KindKnowledgeSearch, "知识库检索失败"},
	{KindHistoryLoad, "加载历史对话失败"},

	// 模型服务端
	{KindModelUnavailable, "Service temporarily unavailable"},
	{KindModelUnavailable, "Service Unavailable"},
	{KindModelUnavailable, "503"},
	{KindRateLimited, "Too Many Requests"},
	{KindRateLimited, "429"},
	{KindContextLength, "maximum context length"},
	{KindContextLength, "prompt is too long"},
	{KindContextLength, "request too large"},
	{KindContextLength, "context_length"},
	{KindContextLength, "context window"},
	{KindContextLength, "context length"},
	{KindContextLength, "token_limit"},
	{KindContextLength, "tokens limit"},
	{KindContextLength, "token limit"},
	{KindContextLength, "input length"},
	{KindContextLength, "too long"},
	{KindContextLength, "上下文长度"},
	{KindContextLength, "token 数超过"},
	{KindContextLength, "超出最大长度"},
	{KindTimeout, "context deadline exceeded"},
	{KindTimeout, "timeout"},
	{KindTimeout, "超时"},
	{KindLLMCallFailed, "LLM 调用失败"},
	{KindLLMStreamFailed, "LLM 流式生成错误"},

	// Agent 执行
	{KindAgentInitFailed, "Agent 初始化失败"},
	{KindAgentStreamFailed, "Agent 流读取失败"},
	{KindResumeFailed, "恢复执行失败"},

	// 会话
	{KindSessionInvalid, "会话不存在"},
	{KindSessionClosed, "会话已关闭"},

	// 工具 / 外部服务
	{KindWebSearchAuthFailed, "联网搜索认证失败"},
	{KindWebSearchTimeout, "联网搜索超时"},
	{KindWebSearchFailed, "联网搜索失败"},
	{KindToolLoadFailed, "工具加载失败"},
	{KindToolCallFailed, "工具调用失败"},
	{KindExternalRequestFailed, "HTTP 请求失败"},
}

// kindFeaturesBySpecificity 把特征表按「具体 → 通用」排好查找顺序。
//
// 用特征串的字节长度作为具体程度的代理（UTF-8 下越长的串越具体，例如
// "联网搜索超时" 比 "超时" 具体），长度相同再按字典序，保证任何输入下
// 命中的都是同一条规则 —— 不依赖 map 遍历顺序。
//
// 该顺序由 kindTextFeatures 派生，新增特征时无需同步维护第二张表。
var kindFeaturesBySpecificity = buildKindFeaturesBySpecificity()

func buildKindFeaturesBySpecificity() []kindTextFeature {
	features := make([]kindTextFeature, len(kindTextFeatures))
	copy(features, kindTextFeatures)
	sort.Slice(features, func(i, j int) bool {
		if len(features[i].needle) != len(features[j].needle) {
			return len(features[i].needle) > len(features[j].needle)
		}
		return features[i].needle < features[j].needle
	})
	return features
}

// Classify 判定一次失败的种类。**这是判定的唯一入口。**
//
// 两级，结果完全确定：
//  1. 结构化：错误链里是否含已登记的哨兵（errors.Is），命中即返回对应种类；
//  2. 文本兜底：按 kindFeaturesBySpecificity 的顺序找「最具体」的特征串。
//
// 底层细节优先：err.Error() 通常带着上游真实原因（503 / 429 / timeout），
// 让它胜出才不会被调用方自己的兜底话术掩盖。
func Classify(err error) Kind {
	if err == nil {
		return KindUnknown
	}

	for _, entry := range kindSentinels {
		if stderrors.Is(err, entry.sentinel) {
			return entry.kind
		}
	}

	text := err.Error()
	for _, feature := range kindFeaturesBySpecificity {
		if strings.Contains(text, feature.needle) {
			return feature.kind
		}
	}
	return KindUnknown
}
