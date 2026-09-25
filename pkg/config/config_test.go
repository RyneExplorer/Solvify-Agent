package config

import (
	"os"
	"strings"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/mitchellh/mapstructure"
)

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
// 为什么需要这条：mapstructure 对不认识的键静默忽略 —— 把 otel_exporter 写成
// otel_exporters，服务照常启动、日志照常打印，字段却停在默认值，「配了等于没配」，
// 只能靠抓包或读源码才能发现。而示例配置是全项目唯一面向使用者的配置文档，
// 最容易随结构体演进而腐烂（结构体改了键名、示例没改，谁都发现不了）。
// 这里用严格解码，任何对不上的键都直接让测试失败。
//
// 注意这是**单向**校验：只查「示例里有结构体不认识的键」，不要求「结构体每个字段都必须
// 出现在示例里」。后者是刻意保留的语义 —— 示例是模板，省略某个键应当仍然合法（走
// Default()）。所以新增结构体字段时本测试不会失败，需要人自觉去补示例；
// 而一旦补错了键名，这里立刻报出来。
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

	// 自研可观测性模块已整体移除，observability 段只剩「eino → Langfuse」这条
	// 三方链路用得到的键 + 脱敏开关，这里逐个断言它们真的解到了。
	if !got.PIIMaskSecret {
		t.Error("observability.pii_mask_secret 没有解到 true")
	}
	if got.OTelServiceName != "solvify-agent" {
		t.Errorf("observability.otel_service_name 期望 solvify-agent，实际 %q", got.OTelServiceName)
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
