package request

// SearchRequest 统一搜索请求
type SearchRequest struct {
	Query     string `form:"q" binding:"required,max=500"`
	TopK      int    `form:"top_k" binding:"min=1,max=50"`
	MaxTokens int    `form:"max_tokens" binding:"min=0,max=10000"`
}
