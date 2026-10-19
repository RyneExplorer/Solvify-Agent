package tool

// MCPConfig MCP连接配置
type MCPConfig struct {
	// 仅由可信配置解析设置，不能由用户JSON覆盖。
	ScopeKey        string            `json:"-"`
	RestrictNetwork bool              `json:"-"`
	Transport       string            `json:"transport"`                 // sse | http
	URL             string            `json:"url,omitempty"`             // sse/http: 端点 URL
	Headers         map[string]string `json:"headers,omitempty"`         // sse/http: 请求头
	Timeout         int               `json:"timeout,omitempty"`         // 兼容旧配置，新字段分别优先
	ConnectTimeout  *int              `json:"connect_timeout,omitempty"` // 建连和初始化的完整时限（秒）
	CallTimeout     *int              `json:"call_timeout,omitempty"`    // tools/list和单次工具调用时限（秒）
	AdminTimeouts   bool              `json:"admin_timeouts,omitempty"`  // 系统预置由管理员保存的时限优先于启动YAML
}
