package observability

import (
	"context"
	"testing"

	"solvify-agent/pkg/config"
)

// TestBuildOTelExporter 覆盖 exporter 类型分发。
//
// 回归价值：把已经移除的 otlp 重新放回 switch（或让它返回非 nil 而不报错），
// 「otlp 已移除必须报错」这条用例会变红 —— 它守的是「三方出口只能有一条」。
func TestBuildOTelExporter(t *testing.T) {
	cases := []struct {
		name    string
		cfg     config.ObservabilityConfig
		wantNil bool
		wantErr bool
	}{
		{
			name:    "空字符串等同 noop",
			cfg:     config.ObservabilityConfig{},
			wantNil: true,
		},
		{
			name:    "noop 不导出",
			cfg:     config.ObservabilityConfig{OTelExporter: "noop"},
			wantNil: true,
		},
		{
			name: "stdout 调试输出",
			cfg:  config.ObservabilityConfig{OTelExporter: "stdout"},
		},
		{
			name:    "otlp 已移除，必须报错（三方链路改走官方 Langfuse callback）",
			cfg:     config.ObservabilityConfig{OTelExporter: "otlp"},
			wantErr: true,
		},
		{
			name:    "未知 exporter 报错",
			cfg:     config.ObservabilityConfig{OTelExporter: "jaeger"},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			exp, err := buildOTelExporter(context.Background(), tc.cfg)
			if tc.wantErr {
				if err == nil {
					t.Fatal("期望返回错误，实际为 nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("创建 exporter 失败: %v", err)
			}
			if tc.wantNil {
				if exp != nil {
					t.Fatal("期望空 exporter，实际为非 nil")
				}
				return
			}
			if exp == nil {
				t.Fatal("期望非空 exporter，实际为 nil")
			}
			// 关闭 exporter 内部连接，避免测试进程残留 goroutine
			if shutdownErr := exp.Shutdown(context.Background()); shutdownErr != nil {
				t.Fatalf("关闭 exporter 失败: %v", shutdownErr)
			}
		})
	}
}
