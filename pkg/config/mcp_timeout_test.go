package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSystemMCPIndependentTimeoutConfig(t *testing.T) {
	previous := globalConfig
	t.Cleanup(func() { globalConfig = previous })
	for _, tc := range []struct {
		connect, call string
		invalid       bool
	}{
		{"90", "120", false}, {"0", "120", true}, {"90", "86401", true}, {"-1", "120", true},
	} {
		path := filepath.Join(t.TempDir(), "config.yaml")
		raw := "tools:\n  mcp_servers:\n    - name: test\n      transport: http\n      url: https://example.invalid\n      connect_timeout: " + tc.connect + "\n      call_timeout: " + tc.call + "\n"
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		cfg, err := Load(path)
		if tc.invalid {
			if err == nil {
				t.Fatal("非法系统预置超时未拒绝", raw)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if *cfg.Tools.MCPServers[0].ConnectTimeout != 90 || *cfg.Tools.MCPServers[0].CallTimeout != 120 {
			t.Fatal("YAML拆分时限未读取")
		}
	}
}
