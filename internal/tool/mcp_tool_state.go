package tool

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// DisabledMCPTools 读取用户持久化偏好，非法权限配置必须拒绝，不能视为全部启用。
func DisabledMCPTools(raw []byte) (map[string]bool, error) {
	var fields struct {
		DisabledTools []string `json:"disabled_tools"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("MCP工具开关配置格式错误")
	}
	if len(fields.DisabledTools) > 128 {
		return nil, fmt.Errorf("MCP停用工具数不能超过128个")
	}
	out := make(map[string]bool, len(fields.DisabledTools))
	for _, name := range fields.DisabledTools {
		if strings.TrimSpace(name) == "" || len(name) > 256 || strings.ContainsFunc(name, unicode.IsControl) {
			return nil, fmt.Errorf("MCP工具开关配置格式错误")
		}
		out[name] = true
	}
	return out, nil
}
