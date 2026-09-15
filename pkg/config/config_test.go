package config

import (
	"os"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/mitchellh/mapstructure"
)

// TestParseHeaderList 覆盖 OTEL_HEADERS 环境变量的解析，重点是含 '=' 的取值不被截断。
func TestParseHeaderList(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want map[string]string
	}{
		{
			name: "空字符串返回 nil",
			raw:  "",
			want: nil,
		},
		{
			name: "单个键值",
			raw:  "Authorization=Bearer token",
			want: map[string]string{"Authorization": "Bearer token"},
		},
		{
			name: "多个键值并去除两端空格",
			raw:  " Authorization = Bearer token , x-byteapm-appkey = abc ",
			want: map[string]string{"Authorization": "Bearer token", "x-byteapm-appkey": "abc"},
		},
		{
			name: "取值里的等号不被截断",
			raw:  "Authorization=Basic dXNlcjpwYXNz==",
			want: map[string]string{"Authorization": "Basic dXNlcjpwYXNz=="},
		},
		{
			name: "缺少等号的条目被忽略",
			raw:  "no-equals,Authorization=ok",
			want: map[string]string{"Authorization": "ok"},
		},
		{
			name: "等号开头视为非法",
			raw:  "=value",
			want: nil,
		},
		{
			name: "全部非法时返回 nil",
			raw:  ",,=",
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseHeaderList(tc.raw)
			if len(got) != len(tc.want) {
				t.Fatalf("条目数不符: got=%v want=%v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Fatalf("键 %q 的取值不符: got=%q want=%q", k, got[k], v)
				}
			}
		})
	}
}

// exampleConfigPath 指向仓库中被 git 跟踪的示例配置。
// 测试的工作目录是 pkg/config，所以要相对走回仓库根。
const exampleConfigPath = "../../configs/config.yaml.example"

// loadExampleConfigValues 把示例配置读成通用 map，供严格解码使用。
func loadExampleConfigValues(t *testing.T) map[string]any {
	t.Helper()
	data, err := os.ReadFile(exampleConfigPath)
	if err != nil {
		t.Fatalf("读取示例配置 %s 失败: %v", exampleConfigPath, err)
	}
	values := map[string]any{}
	if err := yaml.Unmarshal(data, &values); err != nil {
		t.Fatalf("解析示例配置失败: %v", err)
	}
	return values
}

// decodeStrict 用严格模式解码：一旦源数据里有目标结构体不存在的键，立刻报错。
//
// 这是本文件里两个示例配置测试的核心手段。mapstructure 的默认行为是**静默忽略**
// 不认识的键，所以「键名写错」和「配置正确」在运行时表现完全一样，都只能靠人眼比对。
func decodeStrict(raw any, out any) error {
	dec, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:      out,
		ErrorUnused: true,
	})
	if err != nil {
		return err
	}
	return dec.Decode(raw)
}

// TestExampleConfigKeysMatchStruct 守住示例配置的键名与结构体保持一致。
//
// 为什么需要这条：mapstructure 对不认识的键静默忽略 —— 把 otel_otlp_endpoint 写成
// otel_otlp_endpont，服务照常启动、日志照常打印，字段却停在默认值，「配了等于没配」，
// 只能靠抓包或读源码才能发现。而示例配置是全项目唯一面向使用者的配置文档，
// 最容易随结构体演进而腐烂（结构体改了键名、示例没改，谁都发现不了）。
// 这里用严格解码，任何对不上的键都直接让测试失败。
func TestExampleConfigKeysMatchStruct(t *testing.T) {
	values := loadExampleConfigValues(t)

	var cfg Config
	if err := decodeStrict(values, &cfg); err != nil {
		t.Fatalf("示例配置里有结构体不认识的键（键名拼写错误，或结构体改名后示例未同步）: %v", err)
	}
}

// TestExampleConfigObservabilitySection 断言 observability 段的取值确实解码到了对应字段。
//
// 与上一条的分工：上一条查「有没有多余的键」，这一条查「该有的键是不是真的生效了」。
// 关键细节：这里用零值变量接，**不能**先用 Default() 初始化 —— 否则字段没解到时会保留
// 默认值，而示例里写的又恰好等于默认值，测试就永远抓不到键名写错的情况。
func TestExampleConfigObservabilitySection(t *testing.T) {
	values := loadExampleConfigValues(t)

	raw, ok := values["observability"].(map[string]any)
	if !ok {
		t.Fatalf("示例配置缺少 observability 段（或结构不符）: %T", values["observability"])
	}

	var got ObservabilityConfig
	if err := decodeStrict(raw, &got); err != nil {
		t.Fatalf("observability 段里有结构体不认识的键: %v", err)
	}

	// 自研轨道
	if !got.Enabled {
		t.Error("observability.enabled 没有解到 true")
	}
	if got.SamplingRate != 0.2 {
		t.Errorf("observability.sampling_rate 期望 0.2，实际 %v", got.SamplingRate)
	}
	if !got.ErrorAlwaysSample {
		t.Error("observability.error_always_sample 没有解到 true")
	}
	if got.SlowThresholdMs != 5000 {
		t.Errorf("observability.slow_threshold_ms 期望 5000，实际 %d", got.SlowThresholdMs)
	}
	if !got.FeedbackAlwaysSample {
		t.Error("observability.feedback_always_sample 没有解到 true")
	}
	if !got.TraceTableEnabled {
		t.Error("observability.trace_table_enabled 没有解到 true")
	}
	if !got.ExportLogEnabled {
		t.Error("observability.export_log_enabled 没有解到 true")
	}
	if got.MetricsFormat != "json" {
		t.Errorf("observability.metrics_format 期望 json，实际 %q", got.MetricsFormat)
	}
	if got.SinkBufferSize != 1024 {
		t.Errorf("observability.sink_buffer_size 期望 1024，实际 %d", got.SinkBufferSize)
	}
	if got.SinkBatchSize != 50 {
		t.Errorf("observability.sink_batch_size 期望 50，实际 %d", got.SinkBatchSize)
	}
	if got.SinkFlushIntervalMs != 200 {
		t.Errorf("observability.sink_flush_interval_ms 期望 200，实际 %d", got.SinkFlushIntervalMs)
	}
	if got.PIIContentMaxChars != 200 {
		t.Errorf("observability.pii_content_max_chars 期望 200，实际 %d", got.PIIContentMaxChars)
	}
	if !got.PIIMaskSecret {
		t.Error("observability.pii_mask_secret 没有解到 true")
	}
	if !got.FeedbackEnabled {
		t.Error("observability.feedback_enabled 没有解到 true")
	}
	if len(got.WhiteListUserIDs) != 0 {
		t.Errorf("observability.whitelist_user_ids 期望空列表，实际 %v", got.WhiteListUserIDs)
	}
	if got.MaxCardinalityLabels != 500 {
		t.Errorf("observability.max_cardinality_labels 期望 500，实际 %d", got.MaxCardinalityLabels)
	}

	// OTel 三方导出
	if got.OTelExporter != "noop" {
		t.Errorf("observability.otel_exporter 期望 noop，实际 %q", got.OTelExporter)
	}
	if got.OTelOTLPEndpoint != "localhost:4317" {
		t.Errorf("observability.otel_otlp_endpoint 期望 localhost:4317，实际 %q", got.OTelOTLPEndpoint)
	}
	if got.OTelServiceName != "solvify-agent" {
		t.Errorf("observability.otel_service_name 期望 solvify-agent，实际 %q", got.OTelServiceName)
	}
	if got.OTelSamplingRate != 1.0 {
		t.Errorf("observability.otel_sampling_rate 期望 1.0，实际 %v", got.OTelSamplingRate)
	}
	if !got.OTelInsecure {
		t.Error("observability.otel_insecure 没有解到 true")
	}
	if len(got.OTelHeaders) != 0 {
		t.Errorf("observability.otel_headers 期望空的键值对（示例里不能出现真实凭据），实际 %v", got.OTelHeaders)
	}
}

// TestExampleConfigHasNoRealSecrets 守住示例配置里不会混进真实凭据。
//
// 示例文件是被 git 跟踪的，一旦有人把真实 appkey / token 填进去调试后忘了删，
// 就直接提交进仓库了。判定标准：敏感键只允许为空，或是示例里文档化的**占位符**
// （例如 jwt.secret 的 "your-jwt-secret"，它本身就是给人看的格式示意）。
// 白名单要显式列出占位符，不能写成「非空就算可疑」—— 那样模板自带的占位符会误报。
func TestExampleConfigHasNoRealSecrets(t *testing.T) {
	values := loadExampleConfigValues(t)

	// 允许出现在示例里的占位符（只有字面量等于其中之一才算安全）。
	placeholderOK := map[string]bool{
		"":                true,
		"your-jwt-secret": true,
	}

	// 形如 "llm.api_key" 的路径，逐段下钻。
	mustBePlaceholder := []string{
		"jwt.secret",
		"llm.api_key",
		"embedding.api_key",
		"tools.web_search.api_key",
		"dingtalk.app_secret",
		"database.postgres.password",
		"database.redis.password",
		"email.password",
	}
	for _, path := range mustBePlaceholder {
		v, found := lookupPath(values, path)
		if !found || v == nil {
			continue
		}
		s, ok := v.(string)
		if !ok {
			continue
		}
		if !placeholderOK[s] {
			t.Errorf("示例配置 %s 出现了疑似真实凭据（真实值只放本地 config.yaml 或环境变量），"+
				"若确为占位符请显式加入白名单", path)
		}
	}

	// otel_headers 里不能有任何条目：示例只给注释形态的例子。
	if headers, found := lookupPath(values, "observability.otel_headers"); found {
		if m, ok := headers.(map[string]any); ok && len(m) > 0 {
			t.Errorf("示例配置 observability.otel_headers 必须为空（凭据只放本地或环境变量），实际 %v", m)
		}
	}
}

// lookupPath 按 "a.b.c" 逐段下钻取值，任一段缺失都会返回 found=false。
func lookupPath(values map[string]any, path string) (any, bool) {
	var cur any = values
	for _, seg := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[seg]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}
