package request

// CreateModelRequest 描述创建系统模型请求
type CreateModelRequest struct {
	Provider         string                 `json:"provider" binding:"required"`
	ModelID          string                 `json:"model_id" binding:"required"`
	BaseURL          string                 `json:"base_url"`
	APIKey           string                 `json:"api_key"`
	Config           map[string]interface{} `json:"config"`
	MaxContextLength int                    `json:"max_context_length" binding:"min=0,max=2000000"`
}

// UpdateModelRequest 描述更新系统模型请求（指针字段，nil 表示不更新）
type UpdateModelRequest struct {
	ReconfirmCapabilities bool                   `json:"reconfirm_capabilities"`
	Provider              *string                `json:"provider"`
	ModelID               *string                `json:"model_id"`
	BaseURL               *string                `json:"base_url"`
	APIKey                *string                `json:"api_key"`
	IsEnabled             *bool                  `json:"is_enabled"`
	Config                map[string]interface{} `json:"config"`
	MaxContextLength      *int                   `json:"max_context_length" binding:"omitempty,min=0,max=2000000"`
}
