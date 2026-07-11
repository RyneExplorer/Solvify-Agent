package request

// CreateDocumentVersionRequest 创建文档版本请求
type CreateDocumentVersionRequest struct {
	Content       string `json:"content" binding:"required"`
	ChangeSummary string `json:"change_summary"`
}

// CreateNoteRequest 保存笔记到知识库请求
type CreateNoteRequest struct {
	Title   string `json:"title" binding:"required,max=255"`
	Content string `json:"content" binding:"required"`
}
