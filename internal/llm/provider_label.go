package llm

import (
	"net"
	"net/url"
	"strings"
)

// ProviderLabel 推导可观测性上报用的供应商标识（OTel 的 gen_ai.provider.name）。
//
// 为什么需要它：gen_ai.provider.name 的语义是「这批 token 由谁提供」，三方平台
// （Langfuse 等）靠它做模型归类与成本匹配。而项目里最接近的字段是 api_format ——
// 它的语义是「走哪种 API 协议」，在请求 DTO 上被约束成 `oneof=openai anthropic`。
// 直接拿它上报的后果（2026-09-19 实测）：库里 6 个模型（1 个系统 + 5 个用户级）
// 全部以 openai 出现在平台上，zhipu / deepseek / qwen 混作一谈，provider 维度不可用。
//
// 供应商只能从「用户实际填的连接地址」推断：base_url 指向真实服务端点，比模型名更可靠 ——
// 同一个模型可以由不同平台托管，而计费方是托管方。
//
// 判定顺序：base_url 命中已知域名 > 模型名关键字 > 入参原值（保底不改语义）。
func ProviderLabel(modelID, apiFormatOrProvider, baseURL string) string {
	if p := providerFromHost(baseURL); p != "" {
		return p
	}
	if p := providerFromModelID(modelID); p != "" {
		return p
	}
	return strings.TrimSpace(apiFormatOrProvider)
}

// knownProviderHosts 是「端点域名 → 供应商标识」映射。
//
// 新增服务方时在这里加一行即可；命中的域名不会再走模型名推断。
// 这条链上没有任何凭据，只有域名。
var knownProviderHosts = []struct {
	host     string
	provider string
}{
	{"api.deepseek.com", "deepseek"},
	{"deepseek.com", "deepseek"},
	{"open.bigmodel.cn", "zhipu"},
	{"bigmodel.cn", "zhipu"},
	{"dashscope.aliyuncs.com", "tongyi"},
	{"aliyuncs.com", "tongyi"},
	{"api.openai.com", "openai"},
	{"api.anthropic.com", "anthropic"},
	{"generativelanguage.googleapis.com", "google"},
	{"localhost", "ollama"},
}

// knownProviderModelKeywords 是 base_url 给不出线索时的第二判据（如用户只填了模型名）。
var knownProviderModelKeywords = []struct {
	keyword  string
	provider string
}{
	{"deepseek", "deepseek"},
	{"chatglm", "zhipu"},
	{"glm", "zhipu"},
	{"qwen", "tongyi"},
	{"gpt", "openai"},
	{"claude", "anthropic"},
	{"gemini", "google"},
	{"bge-m3", "ollama"},
}

func providerFromHost(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	// 允许用户只填 host（不含 scheme），统一补一个再解析。
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if host == "" {
		return ""
	}
	for _, k := range knownProviderHosts {
		if strings.Contains(host, k.host) {
			return k.provider
		}
	}
	return secondLevelLabel(host)
}

// secondLevelLabel 对未登记的服务方取「二级域名」当标签（ai.gitee.com → gitee）。
//
// 为什么值得做：这类是第三方托管/中转平台，报成 openai 会让平台以为 token 来自 OpenAI；
// 给出端点标签至少能区分「这是另一个服务方，成本要单独看」。
// ⚠️ 对 `x.co.uk` 这类多段公共后缀会取到 "co"，国内接入场景可忽略；
// 真遇到时在 knownProviderHosts 里补一行精确映射即可。
func secondLevelLabel(host string) string {
	if net.ParseIP(host) != nil {
		return "self-hosted"
	}
	parts := strings.Split(host, ".")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-2]
}

func providerFromModelID(modelID string) string {
	modelID = strings.ToLower(strings.TrimSpace(modelID))
	if modelID == "" {
		return ""
	}
	for _, k := range knownProviderModelKeywords {
		if strings.Contains(modelID, k.keyword) {
			return k.provider
		}
	}
	return ""
}
