package errors

// 错误码定义
const (
	// 成功
	CodeSuccess = 0

	// 通用错误 4xx
	CodeBadRequest       = 400
	CodeUnauthorized     = 401
	CodeForbidden        = 403
	CodeNotFound         = 404
	CodeMethodNotAllowed = 405
	CodeRequestTimeout   = 408
	CodeConflict         = 409

	// 服务器错误 5xx
	CodeInternalError      = 500
	CodeServiceUnavailable = 503

	// 业务错误 1xxx
	CodeUserNotFound       = 1001
	CodeUserAlreadyExists  = 1002
	CodeInvalidCredentials = 1003
	CodeUserDisabled       = 1004
	CodeInvalidToken       = 1005
	CodeTokenExpired       = 1006

	CodeInvalidCaptcha = 1007

	// 参数错误
	CodeInvalidParam = 1008
	CodeMissingParam = 1009

	CodeQuestionRequired = 2001
	CodeParamFormatError = 2002

	CodeRAGFailed = 3001
	CodeRAGMissed = 3002

	CodeToolNotFound    = 4001
	CodeToolInvalidArgs = 4002
	CodeToolCallFailed  = 4003

	CodeLLMCallFailed   = 5001
	CodeAgentRunFailed  = 5002
	CodeAgentRunTimeout = 5003

	CodeKnowledgeBaseNotFound   = 6001
	CodeKnowledgeBaseDuplicated = 6002

	CodeModelConfigExists   = 7001
	CodeModelConfigNotFound = 7002

	CodeModelExists = 7003

	CodeSessionNotFound = 8001
	CodeSessionClosed   = 8002

	CodeDocumentNotFound        = 9001
	CodeDocumentFileTooLarge    = 9002
	CodeDocumentFileTypeInvalid = 9003
	CodeDocumentFileDuplicated  = 9004
	CodeStorageQuotaExceeded    = 9005
	CodeKnowledgeBaseReadonly   = 9006
	CodeDocumentStatusInvalid   = 9007
	CodeDocumentJobNotFound     = 9008
	CodeSyncSourceNotFound      = 9009
	CodeSyncSourceStatusInvalid = 9010
	CodeSyncJobNotFound         = 9011

	// 工具管理错误 10xxx
	CodeToolTypeExists       = 10001
	CodeToolTypeNotFound     = 10002
	CodeToolProviderExists   = 10003
	CodeToolProviderNotFound = 10004

	// 钉钉集成错误 11xxx
	CodeDingTalkConfigMissing     = 11001
	CodeDingTalkAccessTokenFailed = 11002
	CodeDingTalkAPICallFailed     = 11003

	// 模型额度错误 12xxx
	CodeModelQuotaExceeded = 12001

	// 重排序配置错误 13xxx
	CodeRerankerConfigNotFound = 13001
	CodeRerankerTestFailed     = 13002
)

var codeMessages = map[int]string{
	CodeSuccess:            "成功",
	CodeBadRequest:         "请求参数错误",
	CodeUnauthorized:       "未授权",
	CodeForbidden:          "禁止访问",
	CodeNotFound:           "资源不存在",
	CodeMethodNotAllowed:   "方法不允许",
	CodeRequestTimeout:     "请求超时",
	CodeConflict:           "资源冲突",
	CodeInternalError:      "服务内部错误",
	CodeServiceUnavailable: "服务不可用",
	CodeUserNotFound:       "用户不存在",
	CodeUserAlreadyExists:  "用户已存在",
	CodeInvalidCredentials: "用户名或密码错误",
	CodeUserDisabled:       "用户已被禁用",
	CodeInvalidToken:       "无效的令牌",
	CodeTokenExpired:       "令牌已过期",
	CodeInvalidCaptcha:     "验证码无效",
	CodeInvalidParam:       "参数错误",
	CodeMissingParam:       "缺少必要参数",
	CodeQuestionRequired:   "问题不能为空",
	CodeParamFormatError:   "参数格式错误",
	CodeRAGFailed:          "RAG 检索失败",
	CodeRAGMissed:          "RAG 未命中",
	CodeToolNotFound:       "工具不存在",
	CodeToolInvalidArgs:    "工具参数错误",
	CodeToolCallFailed:     "工具调用失败",
	CodeLLMCallFailed:      "LLM 调用失败",
	CodeAgentRunFailed:     "Agent 执行失败",
	CodeAgentRunTimeout:    "Agent 执行超时",

	CodeKnowledgeBaseNotFound:   "知识库不存在",
	CodeKnowledgeBaseDuplicated: "同名知识库已存在",

	CodeDocumentNotFound:        "文档不存在",
	CodeDocumentFileTooLarge:    "文件大小超过限制",
	CodeDocumentFileTypeInvalid: "文件类型不支持",
	CodeDocumentFileDuplicated:  "同名文档已存在",
	CodeStorageQuotaExceeded:    "存储配额不足",
	CodeKnowledgeBaseReadonly:   "当前知识库不允许上传文档",
	CodeDocumentStatusInvalid:   "当前文档状态不允许处理",
	CodeDocumentJobNotFound:     "文档处理任务不存在",
	CodeSyncSourceNotFound:      "同步源不存在",
	CodeSyncSourceStatusInvalid: "当前同步源状态不允许同步",
	CodeSyncJobNotFound:         "同步任务不存在",

	CodeSessionNotFound:     "会话不存在",
	CodeSessionClosed:       "会话已关闭",
	CodeModelConfigExists:   "模型配置已存在",
	CodeModelConfigNotFound: "模型配置不存在",
	CodeModelExists:         "系统模型已存在",

	CodeToolTypeExists:       "工具类型已存在",
	CodeToolTypeNotFound:     "工具类型不存在",
	CodeToolProviderExists:   "工具供应商已存在",
	CodeToolProviderNotFound: "工具供应商不存在",

	CodeDingTalkConfigMissing:     "钉钉应用配置缺失",
	CodeDingTalkAccessTokenFailed: "获取钉钉 access_token 失败",
	CodeDingTalkAPICallFailed:     "调用钉钉接口失败",

	CodeModelQuotaExceeded: "模型调用次数已达本月上限",

	CodeRerankerConfigNotFound: "重排序配置不存在",
	CodeRerankerTestFailed:     "重排序服务连接测试失败",
}

// GetMessage 获取错误码对应的文本消息
func GetMessage(code int) string {
	if msg, ok := codeMessages[code]; ok {
		return msg
	}
	return "未知错误"
}
