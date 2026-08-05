package service

// ErrorMessage 用户友好的错误消息
type ErrorMessage struct {
	Title     string // 错误标题
	Detail    string // 详细说明
	Retryable bool   // 是否可重试
}

// errorMessages 错误消息映射表
var errorMessages = map[string]ErrorMessage{
	// 模型相关
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

	// LLM 相关
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
func getFriendlyError(errMsg string) ErrorMessage {
	// 精确匹配
	if msg, ok := errorMessages[errMsg]; ok {
		return msg
	}

	// 模糊匹配
	for key, msg := range errorMessages {
		if contains(errMsg, key) {
			return msg
		}
	}

	// 默认错误消息
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
