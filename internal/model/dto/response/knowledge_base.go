package response

import "time"

// KnowledgeBaseResponse 描述知识库响应
type KnowledgeBaseResponse struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Category       string    `json:"category"`
	Description    string    `json:"description"`
	SourceType     string    `json:"source_type"`
	SourcePlatform string    `json:"source_platform"`
	DocumentCount  int       `json:"document_count"`
	StorageBytes   int64     `json:"storage_bytes"`
	Status         int       `json:"status"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// KnowledgeBaseStatsResponse 描述知识库统计响应
type KnowledgeBaseStatsResponse struct {
	KnowledgeBaseID       string `json:"knowledge_base_id"`
	DocumentCount         int64  `json:"document_count"`
	StorageBytes          int64  `json:"storage_bytes"`
	RetrievableChunkCount int64  `json:"retrievable_chunk_count"`
}
