package request

// CreateUserModelConfigRequest 描述创建用户模型配置请求
type CreateUserModelConfigRequest struct {
	APIFormat string                 `json:"api_format" binding:"required,oneof=openai anthropic"`
	BaseURL   string                 `json:"base_url" binding:"required,max=500"`
	ModelID   string                 `json:"model_id" binding:"required,max=100"`
	APIKey    string                 `json:"api_key"` // 可选，本地模型不需要
	Config    map[string]interface{} `json:"config"`
}

// UpdateUserModelConfigRequest 描述更新用户模型配置请求
type UpdateUserModelConfigRequest struct {
	APIFormat *string                `json:"api_format" binding:"omitempty,oneof=openai anthropic"`
	BaseURL   *string                `json:"base_url" binding:"omitempty,max=500"`
	ModelID   *string                `json:"model_id" binding:"omitempty,max=100"`
	APIKey    *string                `json:"api_key"`
	Config    map[string]interface{} `json:"config"`
}
