package llm

import "testing"

// 用例全部取自 2026-09-19 库里的真实数据：api_format 一律是 openai（协议格式），
// 但真实服务方各不相同 —— 这正是 ProviderLabel 要修的那类「字段语义被混用」。
func TestProviderLabelPrefersEndpointOverAPIFormat(t *testing.T) {
	cases := []struct {
		name      string
		modelID   string
		apiFormat string
		baseURL   string
		want      string
	}{
		{"用户级 deepseek-flash", "deepseek-v4-flash", "openai", "https://api.deepseek.com/v1", "deepseek"},
		{"用户级 deepseek-pro", "deepseek-v4-pro", "openai", "https://api.deepseek.com/v1", "deepseek"},
		{"系统模型 glm（provider 列已是真实厂商）", "glm-4.7-flash", "zhipu", "https://open.bigmodel.cn/api/paas/v4", "zhipu"},
		{"gitee 托管（模型名带 deepseek 也应让位于端点）", "DeepSeek-R1-Distill-Qwen-14B", "openai", "https://ai.gitee.com/v1", "gitee"},
		{"未登记的中转平台取二级域名", "gpt-5.5", "openai", "https://blackaicoding.com/v1", "blackaicoding"},
		{"本地 ollama", "bge-m3", "openai", "http://localhost:11434/v1", "ollama"},
		{"内网 IP 端点", "Qwen3-4B", "openai", "http://10.0.0.5:8000/v1", "self-hosted"},
		{"只填 host 无 scheme", "deepseek-v4-flash", "openai", "api.deepseek.com", "deepseek"},
		{"base_url 为空时退到模型名", "deepseek-v4-flash", "openai", "", "deepseek"},
		{"全无线索时保底原值（不擅自编造）", "unknown-model", "anthropic", "", "anthropic"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ProviderLabel(c.modelID, c.apiFormat, c.baseURL); got != c.want {
				t.Errorf("ProviderLabel(%q, %q, %q) = %q，期望 %q",
					c.modelID, c.apiFormat, c.baseURL, got, c.want)
			}
		})
	}
}

// 回归价值：把 providerFromHost 的判据改成「只看 api_format」或删掉端点分支，本测试会红。
func TestProviderLabelNeverReturnsEmptyForKnownInput(t *testing.T) {
	if got := ProviderLabel("glm-4.7-flash", "openai", "https://open.bigmodel.cn/api/paas/v4"); got != "zhipu" {
		t.Fatalf("glm 模型未还原出 zhipu，实际 %q —— provider 维度会退化成 openai", got)
	}
}
