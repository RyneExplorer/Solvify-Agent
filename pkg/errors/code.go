package errors

// 错误码定义
const (
	CodeSuccess = 0

	CodeBadRequest    = 400
	CodeInternalError = 500

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

	CodeKnowledgeBaseNotFound = 6001

	CodeDocumentNotFound        = 7001
	CodeDocumentFileTooLarge    = 7002
	CodeDocumentFileTypeInvalid = 7003
	CodeDocumentFileDuplicated  = 7004
	CodeStorageQuotaExceeded    = 7005
	CodeKnowledgeBaseReadonly   = 7006
	CodeDocumentStatusInvalid   = 7007
	CodeDocumentJobNotFound     = 7008
)

var codeMessages = map[int]string{
	CodeSuccess:          "成功",
	CodeBadRequest:       "请求参数错误",
	CodeInternalError:    "服务内部错误",
	CodeQuestionRequired: "问题不能为空",
	CodeParamFormatError: "参数格式错误",
	CodeRAGFailed:        "RAG 检索失败",
	CodeRAGMissed:        "RAG 未命中",
	CodeToolNotFound:     "工具不存在",
	CodeToolInvalidArgs:  "工具参数错误",
	CodeToolCallFailed:   "工具调用失败",
	CodeLLMCallFailed:    "LLM 调用失败",
	CodeAgentRunFailed:   "Agent 执行失败",
	CodeAgentRunTimeout:  "Agent 执行超时",

	CodeKnowledgeBaseNotFound: "知识库不存在",

	CodeDocumentNotFound:        "文档不存在",
	CodeDocumentFileTooLarge:    "文件大小超过限制",
	CodeDocumentFileTypeInvalid: "文件类型不支持",
	CodeDocumentFileDuplicated:  "同名文档已存在",
	CodeStorageQuotaExceeded:    "存储配额不足",
	CodeKnowledgeBaseReadonly:   "当前知识库不允许上传文档",
	CodeDocumentStatusInvalid:   "当前文档状态不允许处理",
	CodeDocumentJobNotFound:     "文档处理任务不存在",
}

// GetMessage 获取错误码对应的文本消息
func GetMessage(code int) string {
	if msg, ok := codeMessages[code]; ok {
		return msg
	}
	return "未知错误"
}
