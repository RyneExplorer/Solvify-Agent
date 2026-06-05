package response

// ModelInfo 描述系统模型信息
type ModelInfo struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Provider  string `json:"provider"`
	ModelID   string `json:"model_id"`
	BaseURL   string `json:"base_url,omitempty"`
	APIKey    string `json:"api_key"` // 脱敏后的 APIKey
	IsEnabled bool   `json:"is_enabled"`
}

// ListModelsResponse 描述系统模型列表响应
type ListModelsResponse struct {
	Models []ModelInfo `json:"models"`
}
