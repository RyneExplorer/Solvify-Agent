package tool

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMCPIndependentTimeoutCompatibility(t *testing.T) {
	for _, tc := range []struct {
		raw           string
		connect, call int
	}{
		{`{}`, 30, 300}, {`{"timeout":45}`, 45, 45},
		{`{"timeout":45,"connect_timeout":90}`, 90, 45},
		{`{"timeout":45,"call_timeout":120}`, 45, 120},
		{`{"connect_timeout":90,"call_timeout":120}`, 90, 120},
	} {
		var cfg MCPConfig
		if err := json.Unmarshal([]byte(tc.raw), &cfg); err != nil {
			t.Fatal(err)
		}
		if err := cfg.ValidateTimeouts(); err != nil {
			t.Fatal(err)
		}
		if cfg.ConnectionTimeout() != time.Duration(tc.connect)*time.Second || cfg.ToolCallTimeout() != time.Duration(tc.call)*time.Second {
			t.Fatal(tc.raw, cfg.ConnectionTimeout(), cfg.ToolCallTimeout())
		}
	}
	for _, raw := range []string{`{"connect_timeout":0}`, `{"call_timeout":-1}`, `{"call_timeout":86401}`, `{"connect_timeout":1.5}`, `{"call_timeout":"30"}`} {
		var cfg MCPConfig
		if err := json.Unmarshal([]byte(raw), &cfg); err == nil && cfg.ValidateTimeouts() == nil {
			t.Fatal("非法配置被接受", raw)
		}
	}
}
