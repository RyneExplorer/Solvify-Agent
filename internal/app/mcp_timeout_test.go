package app

import (
	"encoding/json"
	"testing"
	"time"

	"solvify-agent/internal/tool"
	"solvify-agent/pkg/config"
)

func TestSystemMCPTimeoutsSurviveStartupSync(t *testing.T) {
	connect, call := 20, 60
	srv := &config.SystemMCPServerConfig{Transport: "http", URL: "https://new.invalid", ConnectTimeout: &connect, CallTimeout: &call}
	for _, tc := range []struct {
		existing      string
		connect, call int
	}{
		{"", 20, 60},
		{`{"mcp":{"connect_timeout":90,"call_timeout":120}}`, 20, 60},
		{`{"mcp":{"admin_timeouts":true,"connect_timeout":90,"call_timeout":120,"url":"https://old.invalid"}}`, 90, 120},
		{`{"mcp":{"admin_timeouts":true,"connect_timeout":30,"call_timeout":300}}`, 30, 300},
	} {
		raw, err := systemMCPProviderConfig(srv, []byte(tc.existing))
		if err != nil {
			t.Fatal(err)
		}
		var pc tool.ProviderConfig
		if err = json.Unmarshal(raw, &pc); err != nil {
			t.Fatal(err)
		}
		if pc.MCP.URL != srv.URL || pc.MCP.ConnectionTimeout() != time.Duration(tc.connect)*time.Second || pc.MCP.ToolCallTimeout() != time.Duration(tc.call)*time.Second {
			t.Fatal(string(raw))
		}
	}
}
