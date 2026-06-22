package response

import "time"

// SyncSourceConfigResponse 描述同步源非敏感配置响应
type SyncSourceConfigResponse struct {
	OperatorUnionID string `json:"operator_union_id"`
	WorkspaceID     string `json:"workspace_id"`
	RootNodeID      string `json:"root_node_id"`
	SyncMode        string `json:"sync_mode"`
}

// SyncSourceResponse 描述同步源响应
type SyncSourceResponse struct {
	ID               string                   `json:"id"`
	KnowledgeBaseID  string                   `json:"knowledge_base_id"`
	Name             string                   `json:"name"`
	Platform         string                   `json:"platform"`
	SourceConfig     SyncSourceConfigResponse `json:"source_config"`
	Status           int                      `json:"status"`
	LastSyncAt       *time.Time               `json:"last_sync_at"`
	LastErrorMessage string                   `json:"last_error_message"`
	CreatedAt        time.Time                `json:"created_at"`
	UpdatedAt        time.Time                `json:"updated_at"`
	DeletedAt        *time.Time               `json:"deleted_at"`
}

// SyncJobResponse 描述同步任务响应
type SyncJobResponse struct {
	ID              string     `json:"id"`
	SyncSourceID    string     `json:"sync_source_id"`
	KnowledgeBaseID string     `json:"knowledge_base_id"`
	JobType         string     `json:"job_type"`
	Status          int        `json:"status"`
	TotalCount      int        `json:"total_count"`
	SuccessCount    int        `json:"success_count"`
	FailedCount     int        `json:"failed_count"`
	ErrorMessage    string     `json:"error_message"`
	StartedAt       *time.Time `json:"started_at"`
	FinishedAt      *time.Time `json:"finished_at"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

// SyncItemResponse 描述外部同步文件目录项响应
type SyncItemResponse struct {
	ID               string     `json:"id"`
	SyncSourceID     string     `json:"sync_source_id"`
	KnowledgeBaseID  string     `json:"knowledge_base_id"`
	ExternalID       string     `json:"external_id"`
	ParentExternalID string     `json:"parent_external_id"`
	Name             string     `json:"name"`
	ItemType         string     `json:"item_type"`
	Category         string     `json:"category"`
	Extension        string     `json:"extension"`
	ExternalURL      string     `json:"external_url"`
	FileSize         int64      `json:"file_size"`
	HasChildren      bool       `json:"has_children"`
	SourceUpdatedAt  *time.Time `json:"source_updated_at"`
	LocalDocumentID  string     `json:"local_document_id"`
	ImportStatus     int        `json:"import_status"`
	ErrorMessage     string     `json:"error_message"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}
