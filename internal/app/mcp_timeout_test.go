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
		{`{"connect_timeout":90,"call_timeout":120}`, 20, 60},
		{`{"admin_timeouts":true,"connect_timeout":90,"call_timeout":120,"url":"https://old.invalid"}`, 90, 120},
		{`{"admin_timeouts":true,"connect_timeout":30,"call_timeout":300}`, 30, 300},
	} {
		raw, err := systemMCPConnection(srv, []byte(tc.existing))
		if err != nil {
			t.Fatal(err)
		}
		var pc tool.MCPConfig
		if err = json.Unmarshal(raw, &pc); err != nil {
			t.Fatal(err)
		}
		if pc.URL != srv.URL || pc.ConnectionTimeout() != time.Duration(tc.connect)*time.Second || pc.ToolCallTimeout() != time.Duration(tc.call)*time.Second {
			t.Fatal(string(raw))
		}
	}
}
