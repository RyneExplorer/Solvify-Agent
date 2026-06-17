package request

// CreateToolTypeRequest 创建工具类型请求
type CreateToolTypeRequest struct {
	Name          string `json:"name" binding:"required,max=100"`
	ToolKey       string `json:"tool_key" binding:"required,max=50"`
	Description   string `json:"description"`
	ExecutionMode string `json:"execution_mode" binding:"max=20"`
}

// UpdateToolTypeRequest 更新工具类型请求
type UpdateToolTypeRequest struct {
	Name          *string `json:"name,omitempty" binding:"omitempty,max=100"`
	Description   *string `json:"description,omitempty"`
	ExecutionMode *string `json:"execution_mode,omitempty" binding:"omitempty,max=20"`
	IsEnabled     *bool   `json:"is_enabled,omitempty"`
}
