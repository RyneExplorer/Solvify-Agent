package request

// SyncSourceConfigRequest 描述钉钉同步源非敏感配置
type SyncSourceConfigRequest struct {
	OperatorUnionID string `json:"operator_union_id"`
	WorkspaceID     string `json:"workspace_id" binding:"required"`
	RootNodeID      string `json:"root_node_id" binding:"required"`
	SyncMode        string `json:"sync_mode"`
}

// CreateSyncSourceRequest 创建同步源请求
type CreateSyncSourceRequest struct {
	KnowledgeBaseID string                  `json:"knowledge_base_id" binding:"required"`
	Name            string                  `json:"name" binding:"required,max=128"`
	Platform        string                  `json:"platform" binding:"required"`
	SourceConfig    SyncSourceConfigRequest `json:"source_config" binding:"required"`
}

// UpdateSyncSourceRequest 更新同步源请求
type UpdateSyncSourceRequest struct {
	Name         string                  `json:"name" binding:"required,max=128"`
	SourceConfig SyncSourceConfigRequest `json:"source_config" binding:"required"`
	Status       int                     `json:"status"`
}
