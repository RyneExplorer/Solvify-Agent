package response

import "time"

// ChatMessageSearchResult 历史对话语义搜索结果
type ChatMessageSearchResult struct {
	ID           string    `json:"id"`
	SessionID    string    `json:"session_id"`
	SessionTitle string    `json:"session_title"`
	Role         string    `json:"role"`
	Content      string    `json:"content"`
	Score        float64   `json:"score"`
	CreatedAt    time.Time `json:"created_at"`
}

// DocumentSearchResult 知识库文档关键字搜索结果
type DocumentSearchResult struct {
	ID              string  `json:"id"`
	KnowledgeBaseID string  `json:"knowledge_base_id"`
	DocumentID      string  `json:"document_id"`
	Title           string  `json:"title"`
	Content         string  `json:"content"`
	Score           float64 `json:"score"`
}

// SearchResponse 统一搜索响应
type SearchResponse struct {
	ChatMessages []ChatMessageSearchResult `json:"chat_messages"`
	Documents    []DocumentSearchResult    `json:"documents"`
}
