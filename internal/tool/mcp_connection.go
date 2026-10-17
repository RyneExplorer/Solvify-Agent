package tool

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"solvify-agent/internal/model/entity"
)

var mcpPlaceholder = regexp.MustCompile(`\{\{\s*([a-zA-Z_][a-zA-Z0-9_]*)\s*\}\}`)

// ResolveMCPConnection 从可信归属和模板生成运行配置，不改写共享模板。
func ResolveMCPConnection(c *entity.MCPConnection) (*MCPProviderConfig, string, error) {
	var cfg MCPProviderConfig
	prefix := mcpToolPrefix("mcp", c.ID)
	if c.TemplateID != nil {
		p := c.Template
		if p == nil || !p.IsEnabled || p.ProviderType != "mcp" || !p.ToolType.IsEnabled {
			return nil, "", fmt.Errorf("MCP预置不可用")
		}
		var pc ProviderConfig
		if err := json.Unmarshal(p.ProviderConfig, &pc); err != nil || pc.MCP == nil {
			return nil, "", fmt.Errorf("MCP预置配置错误")
		}
		cfg = *pc.MCP
		values := map[string]any{}
		if err := json.Unmarshal(c.Config, &values); err != nil || values == nil {
			return nil, "", fmt.Errorf("MCP配置必须为对象")
		}
		if err := validateMCPFields(p.ConfigSchema, values); err != nil {
			return nil, "", err
		}
		headers := map[string]string{}
		for k, v := range cfg.Headers {
			missing := false
			headers[k] = mcpPlaceholder.ReplaceAllStringFunc(v, func(token string) string {
				key := mcpPlaceholder.FindStringSubmatch(token)[1]
				value, ok := values[key]
				if !ok || value == nil {
					missing = true
					return ""
				}
				return fmt.Sprint(value)
			})
			if missing || strings.Contains(headers[k], "{{") {
				return nil, "", fmt.Errorf("MCP认证字段未填写")
			}
		}
		cfg.Headers = headers
		// URL保持固定，用户字段只能绑定认证Headers，避免把系统凭据发往用户控制的目标。
		if strings.Contains(cfg.URL, "{{") {
			return nil, "", fmt.Errorf("MCP预置地址不允许包含用户变量")
		}
		key := p.ToolType.ToolKey
		if key == "" {
			key = "mcp"
		}
		prefix = mcpToolPrefix(key, p.ProviderKey) // 保留历史checkpoint中的工具名。
	} else {
		if err := json.Unmarshal(c.Connection, &cfg); err != nil {
			return nil, "", fmt.Errorf("MCP连接配置格式错误")
		}
		cfg.RestrictNetwork = true
		if cfg.Transport != "http" {
			return nil, "", fmt.Errorf("个人MCP仅支持Streamable HTTP")
		}
	}
	if cfg.Transport != "http" && cfg.Transport != "sse" {
		return nil, "", fmt.Errorf("MCP仅支持HTTP或SSE")
	}
	if err := ValidateMCPURL(cfg.URL); err != nil {
		return nil, "", err
	}
	if err := ValidateMCPHeaders(cfg.Headers); err != nil {
		return nil, "", err
	}
	if err := cfg.ValidateTimeouts(); err != nil {
		return nil, "", err
	}
	cfg.ScopeKey = c.UserID + ":" + c.ID
	return &cfg, prefix, nil
}

func ValidateMCPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return fmt.Errorf("MCP地址必须为有效HTTP(S)端点，不能包含用户名或片段")
	}
	if len(raw) > 4096 {
		return fmt.Errorf("MCP地址过长")
	}
	return nil
}
func ValidateMCPHeaders(headers map[string]string) error {
	if len(headers) > 32 {
		return fmt.Errorf("MCP请求头过多")
	}
	for key, value := range headers {
		canonical := http.CanonicalHeaderKey(key)
		if canonical == "" || strings.ContainsAny(key, " \t\r\n:") || strings.ContainsAny(value, "\r\n") || len(value) > 8192 {
			return fmt.Errorf("MCP请求头格式错误")
		}
		switch strings.ToLower(key) {
		case "host", "content-length", "connection", "transfer-encoding", "mcp-session-id", "mcp-protocol-version":
			return fmt.Errorf("不允许覆盖MCP协议请求头")
		}
	}
	return nil
}
func validateMCPFields(raw []byte, values map[string]interface{}) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var s struct {
		Type       string `json:"type"`
		Properties map[string]struct {
			Type string   `json:"type"`
			Enum []string `json:"enum"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if json.Unmarshal(raw, &s) != nil || s.Type != "object" {
		return fmt.Errorf("MCP预置字段定义错误")
	}
	for _, key := range s.Required {
		v, ok := values[key]
		if !ok || v == nil || strings.TrimSpace(fmt.Sprint(v)) == "" {
			return fmt.Errorf("请填写MCP必填字段：%s", key)
		}
	}
	for key, value := range values {
		if key == "disabled_tools" {
			continue
		} // 仅保留旧配置的有效期内工具选择。
		field, ok := s.Properties[key]
		if !ok {
			return fmt.Errorf("未知MCP配置字段：%s", key)
		}
		valid := false
		switch field.Type {
		case "string":
			_, valid = value.(string)
		case "number":
			_, valid = value.(float64)
		case "integer":
			n, ok := value.(float64)
			valid = ok && n == float64(int64(n))
		case "boolean":
			_, valid = value.(bool)
		}
		if !valid {
			return fmt.Errorf("MCP配置字段类型错误：%s", key)
		}
		if len(field.Enum) > 0 {
			found := false
			for _, v := range field.Enum {
				if value == v {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("MCP配置字段选项错误：%s", key)
			}
		}
	}
	return nil
}
