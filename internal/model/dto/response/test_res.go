package response

// MCPToolInfo MCP 工具信息（测试连接成功后返回）
type MCPToolInfo struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// TestResult 测试结果
type TestResult struct {
	CapabilityURL            string                 `json:"capability_url,omitempty"`
	CapabilityCheckedAt      string                 `json:"capability_checked_at,omitempty"`
	SuggestedConfig          map[string]interface{} `json:"suggested_config,omitempty"`
	Success                  bool                   `json:"success"`
	Message                  string                 `json:"message"`
	Error                    string                 `json:"error,omitempty"`
	ResponseTime             int64                  `json:"response_time_ms"`
	Details                  string                 `json:"details,omitempty"`
	DetectedMaxContextLength int                    `json:"detected_max_context_length,omitempty"`
	DetectedMaxInputTokens   int                    `json:"detected_max_input_tokens,omitempty"`
	DetectedMaxOutputTokens  int                    `json:"detected_max_output_tokens,omitempty"`
	ConnectionStatus         string                 `json:"connection_status,omitempty"`
	ToolCallingStatus        string                 `json:"tool_calling_status,omitempty"`
	StreamingStatus          string                 `json:"streaming_status,omitempty"`
	CapabilitySource         string                 `json:"capability_source,omitempty"`
	// MCP 场景专用：MCP 工具清单
	MCPToolCount int           `json:"mcp_tool_count,omitempty"`
	MCPTools     []MCPToolInfo `json:"mcp_tools,omitempty"`
}
