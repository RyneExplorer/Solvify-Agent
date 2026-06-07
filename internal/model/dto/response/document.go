package response

import "time"

// DocumentResponse 描述文档响应
type DocumentResponse struct {
	ID              string     `json:"id"`
	KnowledgeBaseID string     `json:"knowledge_base_id"`
	Title           string     `json:"title"`
	FileName        string     `json:"file_name"`
	FileType        string     `json:"file_type"`
	FileSize        int64      `json:"file_size"`
	StoragePath     string     `json:"storage_path"`
	FileHash        string     `json:"file_hash"`
	SourceType      string     `json:"source_type"`
	Status          int16      `json:"status"`
	ErrorMessage    string     `json:"error_message"`
	ReadyAt         *time.Time `json:"ready_at"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
	DeletedAt       *time.Time `json:"deleted_at"`
	DeleteExpiredAt *time.Time `json:"delete_expired_at"`
}
