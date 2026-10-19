package service

import (
	"encoding/json"
	"fmt"
	"time"

	"solvify-agent/internal/tool"
)

// 系统预置只接收时限字段，合并保留地址、凭据及未知扩展。
func mergeSystemMCPTimeouts(existing []byte, incoming json.RawMessage) (json.RawMessage, error) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(incoming, &fields) != nil || fields == nil {
		return nil, fmt.Errorf("MCP超时配置格式错误")
	}
	for key := range fields {
		if key != "connect_timeout" && key != "call_timeout" {
			return nil, fmt.Errorf("部署预置仅允许修改MCP超时时间")
		}
	}
	var timeout tool.MCPConfig
	if json.Unmarshal(incoming, &timeout) != nil {
		return nil, fmt.Errorf("MCP超时配置须为整数")
	}
	if err := timeout.ValidateTimeouts(); err != nil {
		return nil, err
	}
	var stored map[string]json.RawMessage
	if json.Unmarshal(existing, &stored) != nil || stored == nil {
		return nil, fmt.Errorf("部署预置配置格式错误")
	}
	delete(stored, "timeout")
	stored["connect_timeout"], _ = json.Marshal(int(timeout.ConnectionTimeout() / time.Second))
	stored["call_timeout"], _ = json.Marshal(int(timeout.ToolCallTimeout() / time.Second))
	stored["admin_timeouts"] = json.RawMessage("true")
	return json.Marshal(stored)
}
