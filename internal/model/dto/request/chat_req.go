package request

// CreateSessionRequest 创建聊天会话请求
type CreateSessionRequest struct {
	Title   string `json:"title" binding:"max=200"`
	ModelID string `json:"model_id" binding:"required"`
}

// UpdateSessionRequest 更新会话标题请求
type UpdateSessionRequest struct {
	Title string `json:"title" binding:"required,max=200"`
}

// SendMessageRequest 发送消息请求
type SendMessageRequest struct {
	Content          string   `json:"content" binding:"required"`
	KnowledgeBaseIDs []string `json:"knowledge_base_ids" binding:"required,min=1"`
	SearchMode       string   `json:"search_mode" binding:"required,oneof=quick smart-reasoning"`
	ModelID          string   `json:"model_id" binding:"required"`
	ModelType        string   `json:"model_type" binding:"required,oneof=system user"`
	// MCPUserConfigIDs 用户侧 MCP 工具配置 ID 白名单。
	// nil（字段缺省/null）= 未做选择，沿用历史行为：使用该用户全部已启用工具；
	// 非 nil（含空数组 []）= 用户显式做了选择，[] 表示「不使用任何 MCP 工具」。
	//
	// ⚠️ 必须用指针：Go 的 []string 无法区分「字段没传」与「显式传了空数组」，
	// 二者反序列化后都是 len==0，那样「全部不勾选」会被误判成「默认全选」。
	MCPUserConfigIDs *[]string `json:"mcp_user_config_ids,omitempty"`
}

// MCPIDs 返回要挂载的 MCP 配置 ID 白名单。
// nil（未选择）→ nil，表示沿用「全部已启用」；非 nil → 原样返回（可能是空切片）。
func (r SendMessageRequest) MCPIDs() []string {
	if r.MCPUserConfigIDs == nil {
		return nil
	}
	return *r.MCPUserConfigIDs
}

// MCPDisabled 客户端是否显式选择了「不使用任何 MCP 工具」。
//
// 判据唯一在此：只有「字段存在且为空数组」才算显式不用；
// 字段缺省/null 仍沿用历史行为（全部已启用），保证旧客户端不受影响。
func (r SendMessageRequest) MCPDisabled() bool {
	return r.MCPUserConfigIDs != nil && len(*r.MCPUserConfigIDs) == 0
}
