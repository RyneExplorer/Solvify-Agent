package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestToolTimeoutConfiguration(t *testing.T) {
	previous := globalConfig
	t.Cleanup(func() { globalConfig = previous })
	globalConfig = nil
	if ToolExecutionTimeout() != 30*time.Second {
		t.Fatal("默认时限不一致")
	}
	for _, tc := range []struct {
		name, yaml, env string
		want            int
		invalid         bool
	}{
		{name: "default", want: 30}, {name: "yaml", yaml: "tools:\n  default_timeout: 45\n", want: 45},
		{name: "env", yaml: "tools:\n  default_timeout: 45\n", env: "60", want: 60},
		{name: "zero", env: "0", invalid: true}, {name: "negative", env: "-1", invalid: true}, {name: "huge", env: "86401", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("TOOLS_DEFAULT_TIMEOUT", tc.env)
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if tc.invalid {
				if err == nil || !strings.Contains(err.Error(), "tools.default_timeout") {
					t.Fatal(err)
				}
				return
			}
			if err != nil || ToolExecutionTimeout() != time.Duration(tc.want)*time.Second {
				t.Fatalf("%v %v", ToolExecutionTimeout(), err)
			}
		})
	}
}
