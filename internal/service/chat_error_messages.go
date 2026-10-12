package service

import (
	"solvify-agent/internal/agent"
	apperrors "solvify-agent/pkg/errors"
)

// ErrorMessage 用户友好的错误消息
type ErrorMessage struct {
	Title     string // 错误标题
	Detail    string // 详细说明
	Retryable bool   // 是否可重试
}

// kindMessage 是「一个失败种类」对应的文案。
//
// ErrorMessage 是两种模式共用的措辞；只有措辞确实因模式而异的种类才填 Quick / Deep 覆盖。
// 需要覆盖的典型是「建议换另一个模式」这类反向建议：快速失败了建议切深度，反过来也一样。
type kindMessage struct {
	ErrorMessage
	Quick *ErrorMessage
	Deep  *ErrorMessage
}

// errorMessages 是「失败种类 → 面向用户的文案」的**唯一来源**。
//
// 为什么 key 是种类、而不是错误文本：此前 key 是特征串，而 agent 层另有 6 处内联文案，
// 同一个失败有两个来源、且都没有「模式」维度 —— 快速模式的失败因此显示了深度模式的措辞
// （「深度思考模式执行异常，请重试或使用快速模式」）。
// 现在 agent 层只报 Kind，文案只在这里按 Kind × 模式取。
//
// ⚠️ 新增种类时只改这一张表。判定在 pkg/errors（哨兵优先、文本兜底），
// 不要在 agent / controller 里另写用户可见文案。
var errorMessages = map[apperrors.Kind]kindMessage{
	// ── 模型配置 ──
	apperrors.KindModelConfig: {ErrorMessage: ErrorMessage{
		Title:  "模型加载失败",
		Detail: "请在设置中检查模型配置，或选择其他模型",
	}},
	apperrors.KindModelConfigQuery: {ErrorMessage: ErrorMessage{
		Title:  "模型配置查询失败",
		Detail: "无法获取您的模型配置，请检查配置是否正确",
	}},
	apperrors.KindSystemModelUnavailable: {ErrorMessage: ErrorMessage{
		Title:  "系统模型不可用",
		Detail: "系统模型配置异常，请联系管理员",
	}},
	apperrors.KindModelTypeUnsupported: {ErrorMessage: ErrorMessage{
		Title:  "模型类型错误",
		Detail: "请选择有效的模型类型",
	}},

	// ── 上下文装配 ──
	apperrors.KindKnowledgeSearch: {ErrorMessage: ErrorMessage{
		Title:     "知识库查询失败",
		Detail:    "请检查知识库是否正常，或稍后重试",
		Retryable: true,
	}},
	apperrors.KindHistoryLoad: {ErrorMessage: ErrorMessage{
		Title:  "历史记录加载失败",
		Detail: "无法加载历史对话，将使用空对话继续",
	}},

	// ── 模型服务端 ──
	apperrors.KindModelUnavailable: {ErrorMessage: ErrorMessage{
		Title:     "AI 服务暂时不可用",
		Detail:    "模型端点暂时无法响应，请稍后重试",
		Retryable: true,
	}},
	apperrors.KindRateLimited: {ErrorMessage: ErrorMessage{
		Title:     "AI 服务请求过于频繁",
		Detail:    "模型端点限流了，请稍等一会儿再试",
		Retryable: true,
	}},
	apperrors.KindContextLength: {ErrorMessage: ErrorMessage{
		Title:  "对话太长了",
		Detail: "当前对话超出了模型的上下文限制，请开启新对话继续",
	}},
	apperrors.KindTimeout: {ErrorMessage: ErrorMessage{
		Title:     "AI 服务响应超时",
		Detail:    "模型端点响应过慢，请稍后重试或切换更快的模型",
		Retryable: true,
	}},
	apperrors.KindLLMCallFailed: {ErrorMessage: ErrorMessage{
		Title:     "AI 服务异常",
		Detail:    "AI 服务暂时不可用，请稍后重试",
		Retryable: true,
	}},
	apperrors.KindLLMStreamFailed: {ErrorMessage: ErrorMessage{
		Title:     "AI 生成中断",
		Detail:    "回答生成过程中断，请重试",
		Retryable: true,
	}},

	// ── Agent 执行 ──
	apperrors.KindAgentInitFailed: {
		ErrorMessage: ErrorMessage{
			Title:     "深度模式启动失败",
			Detail:    "请尝试切换到快速模式，或稍后重试",
			Retryable: true,
		},
		Quick: &ErrorMessage{
			Title:     "快速模式启动失败",
			Detail:    "请稍后重试，或切换到深度模式",
			Retryable: true,
		},
	},
	apperrors.KindAgentRunFailed: {
		ErrorMessage: ErrorMessage{
			Title:     "深度推理失败",
			Detail:    "深度思考模式执行异常，请重试或使用快速模式",
			Retryable: true,
		},
		Quick: &ErrorMessage{
			Title:     "快速检索链路异常",
			Detail:    "请稍后重试或切换到深度模式",
			Retryable: true,
		},
	},
	apperrors.KindAgentStreamFailed: {ErrorMessage: ErrorMessage{
		Title:     "推理过程中断",
		Detail:    "深度推理过程中断，请重试",
		Retryable: true,
	}},
	// 迭代轮次用尽。这个种类此前没有专属文案，快速模式撞上时会显示上面那句深度措辞；
	// 现在两个模式各说各的，并把真实原因（轮次用尽）直接告诉用户。
	apperrors.KindMaxIterations: {
		ErrorMessage: ErrorMessage{
			Title:     "推理轮次用尽",
			Detail:    "本次执行用尽了推理轮次，请重试",
			Retryable: true,
		},
		Quick: &ErrorMessage{
			Title:     "快速检索轮次用尽",
			Detail:    "本次快速检索用尽了推理轮次，请重试，或切换到深度模式（可用轮次更多）",
			Retryable: true,
		},
		Deep: &ErrorMessage{
			Title:     "深度推理轮次用尽",
			Detail:    "本次深度推理用尽了推理轮次，请重试，或把问题拆小一些",
			Retryable: true,
		},
	},
	apperrors.KindToolUnsupported: {ErrorMessage: ErrorMessage{
		Title:  "当前模型不支持工具调用",
		Detail: "该模型不支持工具调用功能，无法使用联网搜索、天气查询等工具。建议切换到支持工具调用的模型（如通义千问、智谱清言、DeepSeek 等），或使用快速模式。",
	}},
	apperrors.KindToolNotInvoked: {ErrorMessage: ErrorMessage{
		Title:     "深度推理未完成",
		Detail:    "当前模型没有正确发起工具调用，请重试或切换支持工具调用的模型",
		Retryable: true,
	}},
	apperrors.KindResumeFailed: {ErrorMessage: ErrorMessage{
		Title:  "恢复执行失败",
		Detail: "无法从中断点恢复，请重新发起深度模式请求",
	}},
	apperrors.KindEmptyAnswer: {ErrorMessage: ErrorMessage{
		Title:     "AI 未返回内容",
		Detail:    "模型本次没有返回任何内容，请重试或切换其他模型",
		Retryable: true,
	}},
	apperrors.KindUnhandled: {ErrorMessage: ErrorMessage{
		Title:     "操作失败",
		Detail:    "处理过程中发生未预期错误",
		Retryable: true,
	}},

	// ── 会话 ──
	apperrors.KindSessionInvalid: {ErrorMessage: ErrorMessage{
		Title:  "会话已失效",
		Detail: "请返回首页重新开始对话",
	}},
	apperrors.KindSessionClosed: {ErrorMessage: ErrorMessage{
		Title:  "会话已结束",
		Detail: "请创建新的会话继续对话",
	}},

	// ── 工具 / 外部服务 ──
	apperrors.KindToolLoadFailed: {ErrorMessage: ErrorMessage{
		Title:  "工具加载异常",
		Detail: "部分工具可能不可用，将使用基础功能继续",
	}},
	apperrors.KindToolCallFailed: {ErrorMessage: ErrorMessage{
		Title:     "工具调用失败",
		Detail:    "外部工具请求失败，请稍后重试",
		Retryable: true,
	}},
	apperrors.KindWebSearchFailed: {ErrorMessage: ErrorMessage{
		Title:     "联网搜索失败",
		Detail:    "搜索请求失败，请稍后重试",
		Retryable: true,
	}},
	apperrors.KindWebSearchTimeout: {ErrorMessage: ErrorMessage{
		Title:     "联网搜索超时",
		Detail:    "搜索请求超时，请稍后重试",
		Retryable: true,
	}},
	apperrors.KindWebSearchAuthFailed: {ErrorMessage: ErrorMessage{
		Title:  "联网搜索认证失败",
		Detail: "请检查 API Key 配置是否正确",
	}},
	apperrors.KindExternalRequestFailed: {ErrorMessage: ErrorMessage{
		Title:     "外部服务请求失败",
		Detail:    "请稍后重试",
		Retryable: true,
	}},
}

// fallbackMessage 是认不出种类时的兜底文案。
func fallbackMessage() ErrorMessage {
	return ErrorMessage{
		Title:     "操作失败",
		Detail:    "请稍后重试，如问题持续请联系管理员",
		Retryable: true,
	}
}

// messageFor 按「失败种类 + 当前模式」取文案。**这是面向用户文案的唯一翻译点。**
func messageFor(kind apperrors.Kind, profile agent.Profile) ErrorMessage {
	entry, ok := errorMessages[kind]
	if !ok {
		return fallbackMessage()
	}
	switch profile {
	case agent.ProfileQuick:
		if entry.Quick != nil {
			return *entry.Quick
		}
	case agent.ProfileDeep:
		if entry.Deep != nil {
			return *entry.Deep
		}
	}
	return entry.ErrorMessage
}

// getFriendlyError 判定错误种类后取文案。
//
// 判定本身在 pkg/errors.Classify（结构化哨兵优先，文本只作兜底）——
// 本函数不再做任何文本匹配，因此不可能出现「同一句话在第二处被解释成另一种失败」。
func getFriendlyError(err error, profile agent.Profile) ErrorMessage {
	return messageFor(apperrors.Classify(err), profile)
}
