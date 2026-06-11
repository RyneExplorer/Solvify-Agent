package request

// CreateDocumentVersionRequest 创建文档版本请求
type CreateDocumentVersionRequest struct {
	Content       string `json:"content" binding:"required"`
	ChangeSummary string `json:"change_summary"`
}
