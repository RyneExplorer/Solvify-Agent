package service

import (
	"sort"
	"strings"
)

// ErrorMessage 用户友好的错误消息
type ErrorMessage struct {
	Title     string // 错误标题
	Detail    string // 详细说明
	Retryable bool   // 是否可重试
}

// errorMessages 错误消息映射表
// key 用于模糊匹配：getFriendlyError 会检查 err.Error() 和 rawError 是否包含 key
var errorMessages = map[string]ErrorMessage{
	// 模型配置相关
	"模型配置无效或无权访问": {
		Title:     "模型加载失败",
		Detail:    "请在设置中检查模型配置，或选择其他模型",
		Retryable: false,
	},
	"查询用户模型配置失败": {
		Title:     "模型配置查询失败",
		Detail:    "无法获取您的模型配置，请检查配置是否正确",
		Retryable: false,
	},
	"查询系统模型失败": {
		Title:     "系统模型不可用",
		Detail:    "系统模型配置异常，请联系管理员",
		Retryable: false,
	},
	"不支持的模型类型": {
		Title:     "模型类型错误",
		Detail:    "请选择有效的模型类型",
		Retryable: false,
	},

	// 知识库相关
	"知识库检索失败": {
		Title:     "知识库查询失败",
		Detail:    "请检查知识库是否正常，或稍后重试",
		Retryable: true,
	},
	"加载历史对话失败": {
		Title:     "历史记录加载失败",
		Detail:    "无法加载历史对话，将使用空对话继续",
		Retryable: false,
	},

	// LLM 服务端错误（HTTP 状态码 + 关键词）
	"503": {
		Title:     "AI 服务暂时不可用",
		Detail:    "模型端点暂时无法响应，请稍后重试",
		Retryable: true,
	},
	"Service Unavailable": {
		Title:     "AI 服务暂时不可用",
		Detail:    "模型端点暂时无法响应，请稍后重试",
		Retryable: true,
	},
	"Service temporarily unavailable": {
		Title:     "AI 服务暂时不可用",
		Detail:    "模型端点暂时无法响应，请稍后重试",
		Retryable: true,
	},
	"429": {
		Title:     "AI 服务请求过于频繁",
		Detail:    "模型端点限流了，请稍等一会儿再试",
		Retryable: true,
	},
	"Too Many Requests": {
		Title:     "AI 服务请求过于频繁",
		Detail:    "模型端点限流了，请稍等一会儿再试",
		Retryable: true,
	},
	"context length": {
		Title:     "对话太长了",
		Detail:    "当前对话超出了模型的上下文限制，请开启新对话继续",
		Retryable: false,
	},
	"token limit": {
		Title:     "对话太长了",
		Detail:    "当前对话超出了模型的上下文限制，请开启新对话继续",
		Retryable: false,
	},
	"超时": {
		Title:     "AI 服务响应超时",
		Detail:    "模型端点响应过慢，请稍后重试或切换更快的模型",
		Retryable: true,
	},
	"timeout": {
		Title:     "AI 服务响应超时",
		Detail:    "模型端点响应过慢，请稍后重试或切换更快的模型",
		Retryable: true,
	},
	"context deadline exceeded": {
		Title:     "AI 服务响应超时",
		Detail:    "模型端点响应过慢，请稍后重试或切换更快的模型",
		Retryable: true,
	},

	// LLM 调用通用错误
	"LLM 调用失败": {
		Title:     "AI 服务异常",
		Detail:    "AI 服务暂时不可用，请稍后重试",
		Retryable: true,
	},
	"LLM 流式生成错误": {
		Title:     "AI 生成中断",
		Detail:    "回答生成过程中断，请重试",
		Retryable: true,
	},

	// Agent 相关
	"Agent 初始化失败": {
		Title:     "深度模式启动失败",
		Detail:    "请尝试切换到快速模式，或稍后重试",
		Retryable: true,
	},
	"Agent 调用失败": {
		Title:     "深度推理失败",
		Detail:    "深度思考模式执行异常，请重试或使用快速模式",
		Retryable: true,
	},
	"Agent 流读取失败": {
		Title:     "推理过程中断",
		Detail:    "深度推理过程中断，请重试",
		Retryable: true,
	},

	// Graph 执行错误（快速模式）
	"快速检索执行失败": {
		Title:     "快速检索链路异常",
		Detail:    "请稍后重试或切换到深度模式",
		Retryable: true,
	},
	"快速检索流式生成失败": {
		Title:     "AI 生成中断",
		Detail:    "回答生成过程中断，请重试",
		Retryable: true,
	},

	// 会话相关
	"会话不存在": {
		Title:     "会话已失效",
		Detail:    "请返回首页重新开始对话",
		Retryable: false,
	},
	"会话已关闭": {
		Title:     "会话已结束",
		Detail:    "请创建新的会话继续对话",
		Retryable: false,
	},

	// 工具相关
	"工具加载失败": {
		Title:     "工具加载异常",
		Detail:    "部分工具可能不可用，将使用基础功能继续",
		Retryable: false,
	},
	"工具调用失败": {
		Title:     "工具调用失败",
		Detail:    "外部工具请求失败，请稍后重试",
		Retryable: true,
	},
	"联网搜索失败": {
		Title:     "联网搜索失败",
		Detail:    "搜索请求失败，请稍后重试",
		Retryable: true,
	},
	"联网搜索超时": {
		Title:     "联网搜索超时",
		Detail:    "搜索请求超时，请稍后重试",
		Retryable: true,
	},
	"联网搜索认证失败": {
		Title:     "联网搜索认证失败",
		Detail:    "请检查 API Key 配置是否正确",
		Retryable: false,
	},
	"HTTP 请求失败": {
		Title:     "外部服务请求失败",
		Detail:    "请稍后重试",
		Retryable: true,
	},
}

// getFriendlyError 获取用户友好的错误消息。
//
// 匹配规则（两级，结果完全确定，不依赖 map 遍历顺序）：
//  1. 底层错误详情优先：先在 err.Error() 里找命中，再在 rawError 里找。
//     err.Error() 通常带着上游真实原因（503 / 429 / timeout），rawError 往往是我们自己的
//     兜底话术（"LLM 调用失败"），让前者胜出才不会被后者掩盖；
//  2. 同一片段内取最具体的命中：见 errorKeysBySpecificity。
//
// 旧实现直接 for range map 找第一个命中的 key，而 Go 的 map 遍历顺序是随机的：
// 同一个错误在多次请求里会随机命中不同规则。典型如被包装成
// "LLM 调用失败: 429 Too Many Requests" 的限流错误，可能命中 "429"、"Too Many Requests"
// 或 "LLM 调用失败" 三条规则之一，用户看到的提示时好时坏 ——
// 具体可操作的「请求过于频繁」会被随机替换成笼统的「AI 服务异常」。
func getFriendlyError(err error, rawError string) ErrorMessage {
	errText := ""
	if err != nil {
		errText = err.Error()
	}

	if msg, ok := matchErrorMessage(errText); ok {
		return msg
	}
	if msg, ok := matchErrorMessage(rawError); ok {
		return msg
	}

	return ErrorMessage{
		Title:     "操作失败",
		Detail:    "请稍后重试，如问题持续请联系管理员",
		Retryable: true,
	}
}

// matchErrorMessage 在一个文本片段里找「最具体」的命中特征串，按
// errorKeysBySpecificity 的顺序查找，先命中先返回。
func matchErrorMessage(text string) (ErrorMessage, bool) {
	if text == "" {
		return ErrorMessage{}, false
	}
	for _, key := range errorKeysBySpecificity {
		if strings.Contains(text, key) {
			return errorMessages[key], true
		}
	}
	return ErrorMessage{}, false
}

// errorKeysBySpecificity 是 errorMessages 的 key 按「具体 → 通用」排好的查找顺序。
//
// 用 key 的字节长度作为具体程度的代理（UTF-8 下越长的特征串越具体，例如
// "Too Many Requests" 比 "429" 具体、"Service Unavailable" 比 "超时" 具体），
// 长度相同再按字典序，保证任何输入下命中的都是同一条规则。
//
// 该顺序由 errorMessages 派生，新增 key 时无需同步维护第二张表。
var errorKeysBySpecificity = buildErrorKeysBySpecificity()

func buildErrorKeysBySpecificity() []string {
	keys := make([]string, 0, len(errorMessages))
	for key := range errorMessages {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if len(keys[i]) != len(keys[j]) {
			return len(keys[i]) > len(keys[j])
		}
		return keys[i] < keys[j]
	})
	return keys
}
