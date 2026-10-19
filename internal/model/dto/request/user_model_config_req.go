package request

// CreateUserModelConfigRequest 描述创建用户模型配置请求
type CreateUserModelConfigRequest struct {
	APIFormat        string                 `json:"api_format" binding:"required,oneof=openai"`
	BaseURL          string                 `json:"base_url" binding:"required,max=500"`
	ModelID          string                 `json:"model_id" binding:"required,max=100"`
	APIKey           string                 `json:"api_key"` // 可选，本地模型不需要
	Config           map[string]interface{} `json:"config"`
	MaxContextLength int                    `json:"max_context_length" binding:"min=0,max=2000000"`
}

// UpdateUserModelConfigRequest 描述更新用户模型配置请求
type UpdateUserModelConfigRequest struct {
	ReconfirmCapabilities bool                   `json:"reconfirm_capabilities"`
	APIFormat             *string                `json:"api_format" binding:"omitempty,oneof=openai"`
	BaseURL               *string                `json:"base_url" binding:"omitempty,max=500"`
	ModelID               *string                `json:"model_id" binding:"omitempty,max=100"`
	APIKey                *string                `json:"api_key"`
	Config                map[string]interface{} `json:"config"`
	MaxContextLength      *int                   `json:"max_context_length" binding:"omitempty,min=0,max=2000000"`
}
