package service

import (
	"encoding/json"
	"fmt"
	"time"

	"solvify-agent/internal/tool"
)

// 系统预置只接收超时字段，合并时保留地址、凭据与未知扩展字段。
func mergeSystemMCPTimeouts(existing []byte, incoming json.RawMessage) (json.RawMessage, error) {
	var patch map[string]json.RawMessage
	if err := json.Unmarshal(incoming, &patch); err != nil || len(patch) != 1 || patch["mcp"] == nil {
		return nil, fmt.Errorf("系统预置仅允许修改MCP超时时间")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(patch["mcp"], &fields); err != nil || fields == nil {
		return nil, fmt.Errorf("MCP超时配置格式错误")
	}
	for key := range fields {
		if key != "connect_timeout" && key != "call_timeout" {
			return nil, fmt.Errorf("系统预置仅允许修改MCP超时时间")
		}
	}
	var timeout tool.MCPProviderConfig
	if err := json.Unmarshal(patch["mcp"], &timeout); err != nil {
		return nil, fmt.Errorf("MCP超时配置须为整数")
	}
	if err := timeout.ValidateTimeouts(); err != nil {
		return nil, err
	}
	var stored map[string]json.RawMessage
	if err := json.Unmarshal(existing, &stored); err != nil || stored == nil {
		return nil, fmt.Errorf("系统预置配置格式错误")
	}
	var mcp map[string]json.RawMessage
	if err := json.Unmarshal(stored["mcp"], &mcp); err != nil || mcp == nil {
		return nil, fmt.Errorf("系统预置MCP配置格式错误")
	}
	delete(mcp, "timeout")
	// 将留空物化为默认值，标记管理员归属，避免重启时被YAML重置。
	mcp["connect_timeout"], _ = json.Marshal(int(timeout.ConnectionTimeout() / time.Second))
	mcp["call_timeout"], _ = json.Marshal(int(timeout.ToolCallTimeout() / time.Second))
	mcp["admin_timeouts"] = json.RawMessage("true")
	stored["mcp"], _ = json.Marshal(mcp)
	return json.Marshal(stored)
}
