package observability

import (
	"context"
	"testing"

	"solvify-agent/pkg/config"
)

// TestBuildOTelExporter 覆盖 exporter 类型分发与两种传输安全模式。
// otlptracegrpc.New 内部用 grpc.NewClient 惰性建连，因此这里不需要真实服务端。
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
			name: "otlp 明文连接（内网或本机 Collector）",
			cfg: config.ObservabilityConfig{
				OTelExporter:     "otlp",
				OTelOTLPEndpoint: "127.0.0.1:4317",
				OTelInsecure:     true,
			},
		},
		{
			name: "otlp TLS 加鉴权头（SaaS 后端）",
			cfg: config.ObservabilityConfig{
				OTelExporter:     "otlp",
				OTelOTLPEndpoint: "otlp.example.com:443",
				OTelInsecure:     false,
				OTelHeaders:      map[string]string{"Authorization": "Bearer test-token"},
			},
		},
		{
			name: "otlp 不带 endpoint 时交由 SDK 默认值或标准环境变量决定",
			cfg: config.ObservabilityConfig{
				OTelExporter: "otlp",
				OTelInsecure: true,
			},
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

// TestOtelHeaderKeys 确认日志只输出鉴权头的键名，不泄露键值。
func TestOtelHeaderKeys(t *testing.T) {
	if got := otelHeaderKeys(nil); got != nil {
		t.Fatalf("空 headers 应返回 nil，实际为 %v", got)
	}
	if got := otelHeaderKeys(map[string]string{}); got != nil {
		t.Fatalf("空 map 应返回 nil，实际为 %v", got)
	}

	got := otelHeaderKeys(map[string]string{
		"x-byteapm-appkey": "secret-app-key",
		"Authorization":    "Bearer secret-token",
	})
	want := []string{"Authorization", "x-byteapm-appkey"}
	if len(got) != len(want) {
		t.Fatalf("键数量不符: got=%v want=%v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("键名或字典序不符: got=%v want=%v", got, want)
		}
	}
}
