package agent

// SourceInfo 是 Agent 收集的文档证据，由 Service 转换为 HTTP 响应 DTO。
type SourceInfo struct {
	DocumentID      string        `json:"document_id"`
	KnowledgeBaseID string        `json:"knowledge_base_id"`
	Title           string        `json:"title"`
	Score           float64       `json:"score"`
	Chunks          []ChunkSource `json:"chunks"`
}

type ChunkSource struct {
	ID      string  `json:"id"`
	Quote   string  `json:"quote,omitempty"`
	Content string  `json:"content"`
	Score   float64 `json:"score"`
}
