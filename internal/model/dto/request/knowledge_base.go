package request

// CreateKnowledgeBaseRequest 创建知识库请求
type CreateKnowledgeBaseRequest struct {
	Name        string `json:"name" binding:"required,max=128"`
	Category    string `json:"category" binding:"max=64"`
	Description string `json:"description"`
}

// UpdateKnowledgeBaseRequest 更新知识库请求
type UpdateKnowledgeBaseRequest struct {
	Name        string `json:"name" binding:"required,max=128"`
	Category    string `json:"category" binding:"max=64"`
	Description string `json:"description"`
}
