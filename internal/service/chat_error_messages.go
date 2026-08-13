package service

import "strings"

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

// getFriendlyError 获取用户友好的错误消息
// 匹配顺序：先检查 err.Error()（底层错误详情，如 503/429），再检查 rawError（业务层自定义描述）
// 这样像 "快速检索执行失败" 这种通用描述不会掩盖掉底层真正的错误原因
func getFriendlyError(err error, rawError string) ErrorMessage {
	var combined strings.Builder
	if err != nil {
		combined.WriteString(err.Error())
	}
	combined.WriteString("|")
	combined.WriteString(rawError)
	text := combined.String()

	if msg, ok := errorMessages[text]; ok {
		return msg
	}

	for key, msg := range errorMessages {
		if contains(text, key) {
			return msg
		}
	}

	return ErrorMessage{
		Title:     "操作失败",
		Detail:    "请稍后重试，如问题持续请联系管理员",
		Retryable: true,
	}
}

// contains 检查字符串是否包含子串
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && len(substr) > 0 && containsSubstring(s, substr))
}

func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
