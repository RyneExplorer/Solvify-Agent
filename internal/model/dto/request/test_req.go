package request

// TestModelRequest 测试模型连接请求
type TestModelRequest struct {
	Provider string                 `json:"provider" binding:"required"`
	ModelID  string                 `json:"model_id" binding:"required"`
	BaseURL  string                 `json:"base_url" binding:"required"`
	APIKey   string                 `json:"api_key"`
	Config   map[string]interface{} `json:"config"`
}

// TestToolRequest 测试工具连接请求
// ProviderID 用于用户测试场景：从数据库查询 provider_config，避免前端传递敏感配置
// ProviderConfig 用于管理员测试场景：直接使用表单填写的配置
type TestToolRequest struct {
	ProviderType   string                 `json:"provider_type" binding:"required"`
	ProviderID     string                 `json:"provider_id"`
	ProviderConfig map[string]interface{} `json:"provider_config"`
	UserConfig     map[string]interface{} `json:"user_config"`
	AdminConfig    map[string]interface{} `json:"admin_config"`
	ToolInput      map[string]interface{} `json:"tool_input"`
}
