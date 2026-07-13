package request

// CreateSessionRequest 创建聊天会话请求
type CreateSessionRequest struct {
	Title   string `json:"title" binding:"max=200"`
	ModelID string `json:"model_id" binding:"required"`
}

// UpdateSessionRequest 更新会话标题请求
type UpdateSessionRequest struct {
	Title string `json:"title" binding:"required,max=200"`
}

// SendMessageRequest 发送消息请求
type SendMessageRequest struct {
	Content          string   `json:"content" binding:"required"`
	KnowledgeBaseIDs []string `json:"knowledge_base_ids" binding:"required,min=1"`
	SearchMode       string   `json:"search_mode" binding:"required,oneof=quick smart-reasoning"`
	ModelID          string   `json:"model_id" binding:"required"`
	ModelType        string   `json:"model_type" binding:"required,oneof=system user"`
}
