package request

// TestModelRequest 测试模型连接请求
type TestModelRequest struct {
	ConfigID         string                 `json:"config_id,omitempty" binding:"omitempty,uuid"`
	Operation        string                 `json:"operation,omitempty" binding:"omitempty,oneof=test capabilities"`
	Provider         string                 `json:"provider" binding:"required"`
	ModelID          string                 `json:"model_id" binding:"required"`
	BaseURL          string                 `json:"base_url" binding:"required"`
	APIKey           string                 `json:"api_key"`
	Config           map[string]interface{} `json:"config"`
	MaxContextLength int                    `json:"max_context_length" binding:"omitempty,min=1024,max=2000000"`
}
