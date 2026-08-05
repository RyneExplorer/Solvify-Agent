package tokenutil

import (
	"strings"
	"sync"

	"github.com/pkoukk/tiktoken-go"
)

// defaultEncoding 是在调用方没传模型时的兜底编码（cl100k_base 覆盖绝大多数中文开源/openai 兼容模型）。
// 2024+ 新模型（GPT-4o、o1、通义千问新版等）应走 EncodingForModel 直接拿到 o200k_base。
const defaultEncoding = "cl100k_base"

var (
	encMu       sync.RWMutex
	encCache    = map[string]*tiktoken.Tiktoken{}
	encErrs     = map[string]struct{}{} // 加载失败过的 name，避免反复打错误日志 / 网络请求
)

// getEncoding 按名字取编码，失败就 fallback 到 defaultEncoding。
// 绝大多数场景（tiktoken-go 离线 bpe 文件已经下过一次后）这里就是一次 map 命中。
func getEncoding(name string) (*tiktoken.Tiktoken, error) {
	if name == "" {
		name = defaultEncoding
	}
	encMu.RLock()
	if tk, ok := encCache[name]; ok {
		encMu.RUnlock()
		return tk, nil
	}
	_, failed := encErrs[name]
	encMu.RUnlock()
	if failed {
		return fallbackEstimate()
	}

	encMu.Lock()
	defer encMu.Unlock()
	if tk, ok := encCache[name]; ok {
		return tk, nil
	}

	// 优先按"模型名"解析；解析失败就按"编码名"取
	tk, err := tiktoken.EncodingForModel(name)
	if err != nil {
		tk, err = tiktoken.GetEncoding(name)
	}
	if err != nil {
		encErrs[name] = struct{}{}
		return fallbackEstimate()
	}
	encCache[name] = tk
	return tk, nil
}

// fallbackEstimate 无法真 BPE 时返回一个永远不会真的被调用的编码：
// 这里不做实现，调用链会改走 legacyEstimate 兜底。
// 保留一个 error 接口不变，外部用 CountTokens 统一接入点。
func fallbackEstimate() (*tiktoken.Tiktoken, error) {
	return nil, nil
}

// ─── 公共 API ───────────────────────────────────────────

// CountTokens 统计文本真实 BPE token 数。
// modelOrEncoding 可以是具体模型名（"gpt-4o"、"qwen-max" 等会尝试 EncodingForModel），
// 也可以是编码名（"cl100k_base"、"o200k_base"）。空字符串默认 cl100k_base。
// 无法真 BPE 时自动退回到字符加权估算，保证任何场景都有稳定返回。
func CountTokens(text, modelOrEncoding string) int {
	if text == "" {
		return 0
	}
	tk, err := getEncoding(modelOrEncoding)
	if err == nil && tk != nil {
		return len(tk.Encode(text, nil, nil))
	}
	return legacyEstimate(text)
}

// TruncateByTokens 按真 BPE 从左往右截断 text，保留至多 maxTokens 个 token。
// 返回截断后文本 + 实际 token 数。尾部若被截断不追加 "..."，调用方决定是否加（避免重复加）。
func TruncateByTokens(text, modelOrEncoding string, maxTokens int) (string, int) {
	if maxTokens <= 0 {
		return "", 0
	}
	if text == "" {
		return "", 0
	}
	tk, err := getEncoding(modelOrEncoding)
	if err != nil || tk == nil {
		// 真 BPE 拿不到就用估算做字节/字符近似，优先保证不越上限
		return legacyTruncate(text, maxTokens)
	}
	ids := tk.Encode(text, nil, nil)
	if len(ids) <= maxTokens {
		return text, len(ids)
	}
	trimmed := tk.Decode(ids[:maxTokens])
	if trimmed == "" && len(ids) > 0 {
		return legacyTruncate(text, maxTokens)
	}
	return trimmed, maxTokens
}

// EncodingForModelName 暴露"给模型名，拿到编码名"的能力，给上层分块指标命名用。
// 未知模型默认返回 cl100k_base，不会失败。
func EncodingForModelName(modelName string) string {
	name := strings.TrimSpace(modelName)
	if name == "" {
		return defaultEncoding
	}
	if _, err := tiktoken.EncodingForModel(name); err == nil {
		return name
	}
	// 少数开源模型会走 "编码名本身" 的路径
	if tk, err := tiktoken.GetEncoding(name); err == nil && tk != nil {
		return name
	}
	return defaultEncoding
}

// ─── legacy 兜底：真 BPE 拿不到时用旧算法 ────────────────

// legacyEstimate 估算文本 token 数（旧算法），仅作为离线/首次加载编码失败时兜底。
func legacyEstimate(text string) int {
	if text == "" {
		return 0
	}
	var cn, en, other float64
	for _, r := range []rune(text) {
		switch {
		case r >= 0x4e00 && r <= 0x9fff:
			cn++
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			en++
		default:
			other++
		}
	}
	total := cn*1.5 + en*0.25 + other*0.5
	if total < 1 {
		return 1
	}
	return int(total)
}

// legacyTruncate 按估算 1.5 倍系数截 rune，保证"估算 token <= maxTokens"。
func legacyTruncate(text string, maxTokens int) (string, int) {
	if maxTokens <= 0 {
		return "", 0
	}
	runes := []rune(text)
	// 按 1.5 中文系数估算最坏字符上界，多留一点安全垫
	maxRunes := int(float64(maxTokens) / 0.8)
	if maxRunes < 1 {
		maxRunes = 1
	}
	if len(runes) <= maxRunes {
		est := legacyEstimate(text)
		if est <= maxTokens {
			return text, est
		}
	}
	// 逐步收缩，直到估算 <= maxTokens
	n := len(runes)
	if n > maxRunes {
		n = maxRunes
	}
	for n > 0 {
		cut := string(runes[:n])
		est := legacyEstimate(cut)
		if est <= maxTokens {
			return cut, est
		}
		n--
	}
	return "", 0
}

// Estimate 兼容旧调用方（内部直接走 CountTokens + 默认编码），保持对外签名不变。
// 新代码优先用 CountTokens(text, modelName) 传具体模型名，准确度更高。
func Estimate(text string) int {
	return CountTokens(text, defaultEncoding)
}
