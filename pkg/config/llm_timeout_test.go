package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestChatModelTimeoutConfiguration(t *testing.T) {
	previous := globalConfig
	t.Cleanup(func() { globalConfig = previous })
	globalConfig = nil
	if got := ChatModelTimeout(); got != time.Duration(Default().LLM.Timeout)*time.Second {
		t.Fatalf("加载前默认值不一致：%v", got)
	}
	for _, tc := range []struct {
		name, yaml, env string
		want            int
		invalid         bool
	}{
		{name: "default", want: 120},
		{name: "yaml", yaml: "llm:\n  timeout: 7\n", want: 7},
		{name: "environment", yaml: "llm:\n  timeout: 7\n", env: "2", want: 2},
		{name: "zero", yaml: "llm:\n  timeout: 0\n", invalid: true},
		{name: "negative", env: "-1", invalid: true},
		{name: "overflow", env: "9223372037", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LLM_TIMEOUT", tc.env)
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0600); err != nil {
				t.Fatal(err)
			}
			_, err := Load(path)
			if tc.invalid {
				if err == nil || !strings.Contains(err.Error(), "llm.timeout") {
					t.Fatalf("非法时限未在加载时拒绝：%v", err)
				}
				return
			}
			if err != nil || ChatModelTimeout() != time.Duration(tc.want)*time.Second {
				t.Fatalf("超时配置未生效：值=%v 错误=%v", ChatModelTimeout(), err)
			}
		})
	}
}

func TestChatModelStreamTimeoutConfiguration(t *testing.T) {
	previous := globalConfig
	t.Cleanup(func() { globalConfig = previous })
	for _, field := range []struct {
		yaml, env string
		first     bool
	}{
		{"stream_first_output_timeout", "LLM_STREAM_FIRST_OUTPUT_TIMEOUT", true},
		{"stream_idle_timeout", "LLM_STREAM_IDLE_TIMEOUT", false},
	} {
		t.Run(field.yaml, func(t *testing.T) {
			for _, value := range []int{7, 0, -1, 9223372037} {
				path := filepath.Join(t.TempDir(), "config.yaml")
				if err := os.WriteFile(path, []byte(fmt.Sprintf("llm:\n  %s: %d\n", field.yaml, value)), 0600); err != nil {
					t.Fatal(err)
				}
				_, err := Load(path)
				if value != 7 {
					if err == nil || !strings.Contains(err.Error(), field.yaml) {
						t.Fatalf("非法流时限未拒绝: %v", err)
					}
					continue
				}
				if err != nil {
					t.Fatal(err)
				}
				first, idle := ChatModelStreamTimeouts()
				if (field.first && first != 7*time.Second) || (!field.first && idle != 7*time.Second) {
					t.Fatalf("YAML未生效: %v/%v", first, idle)
				}
				t.Setenv(field.env, "3")
				if _, err := Load(path); err != nil {
					t.Fatal(err)
				}
				first, idle = ChatModelStreamTimeouts()
				if (field.first && first != 3*time.Second) || (!field.first && idle != 3*time.Second) {
					t.Fatalf("环境变量未生效: %v/%v", first, idle)
				}
				t.Setenv(field.env, "")
			}
		})
	}
	globalConfig = nil
	first, idle := ChatModelStreamTimeouts()
	if first != 60*time.Second || idle != 30*time.Second {
		t.Fatalf("流默认值错误: %v/%v", first, idle)
	}
}
