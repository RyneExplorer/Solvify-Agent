package observability

import (
	"regexp"
	"strings"
)

// 本文件只做一件事：按项目既定的 PII 规则给文本打码（mask），不做任何截断。
//
// 为什么不复用原来的 PIISanitizer：它还背着「按字段名分流截断」与「递归遍历 attrs」
// 两套逻辑，那是自研落库 / 日志出口用的。Langfuse 出口只需要打码 ——
// 长度由官方 SDK 自己的两个旋钮管（MaxAttributeValueLength 单值上限、
// MaxSpanAttributeBytes 单 span 总预算），这里再截一次就是「长度有两个来源」，
// 而且小的那个赢：默认 200 字会把完整 prompt / 回复直接废掉，平台就白接了。

var (
	emailRe        = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)
	phoneRe        = regexp.MustCompile(`(1[3-9]\d)(\d{4})(\d{4})|(\d{3})(\d{4})(\d{4})`)
	secretHeaderRe = regexp.MustCompile(`(?i)(Authorization|Bearer|Api-Key|X-API-Key|X-Auth-Token|Proxy-Authorization)[:=]\s*[^\s,;"']+`)
	skKeyRe        = regexp.MustCompile(`(?i)(sk-|pk-|token|apikey|api_key|secret)[^ \t\n\r]{0,4}[=: ]\s*[A-Za-z0-9_\-]{8,}`)
)

// MaskPII 按项目 PII 规则脱敏文本，返回打码后的副本（不修改入参）。
//
// maskSecret 控制是否连「密钥 / Token 形态」的片段一起打码。凭据类字段属于必须打的，
// 所以生产上应当为 true（配置项 pii_mask_secret，默认 true）。
//
// 邮箱与手机号无条件打码：它们的出现位置无法枚举，漏一次就是一次数据泄露。
func MaskPII(text string, maskSecret bool) string {
	if text == "" {
		return text
	}
	out := text
	if maskSecret {
		out = secretHeaderRe.ReplaceAllStringFunc(out, maskHeaderSecret)
		out = skKeyRe.ReplaceAllStringFunc(out, maskKeyValueSecret)
	}
	out = emailRe.ReplaceAllStringFunc(out, maskEmail)
	out = phoneRe.ReplaceAllStringFunc(out, maskPhone)
	return out
}

// maskEmail 保留前 2 个字符与完整域名：`alice@example.com` → `al***@example.com`。
func maskEmail(s string) string {
	at := strings.LastIndex(s, "@")
	if at <= 0 {
		return s
	}
	user := s[:at]
	domain := s[at:]
	if len(user) <= 2 {
		return user[:1] + "***" + domain
	}
	return user[:2] + strings.Repeat("*", maxInt(3, len(user)-2)) + domain
}

// maskPhone 保留前 3 后 4：`13812345678` → `138****5678`。
func maskPhone(s string) string {
	if len(s) != 11 {
		if len(s) >= 7 {
			return s[:3] + strings.Repeat("*", len(s)-7) + s[len(s)-4:]
		}
		return s
	}
	return s[:3] + "****" + s[7:]
}

// maskHeaderSecret 处理 `Authorization: Bearer xxx` 这类「前缀: 值」形态，保留值的头 4 尾 4。
func maskHeaderSecret(s string) string {
	idx := strings.IndexAny(s, ":=")
	if idx < 0 {
		return s
	}
	prefix := s[:idx+1]
	rest := strings.TrimLeft(s[idx+1:], " \t")
	tail := "***"
	if len(rest) >= 8 {
		tail = rest[:4] + "***" + rest[len(rest)-4:]
	}
	return prefix + " " + tail
}

// maskKeyValueSecret 处理 `api_key=xxx` 这类「键 值」形态，保留值的头 4 尾 4。
func maskKeyValueSecret(s string) string {
	idx := strings.IndexAny(s, "=: ")
	if idx < 0 {
		return s
	}
	prefix := s[:idx+1]
	rest := strings.TrimLeft(s[idx+1:], " \t")
	tail := "***"
	if len(rest) >= 8 {
		tail = rest[:4] + "***" + rest[len(rest)-4:]
	}
	return prefix + tail
}

// maxInt 返回较大者。
//
// 刻意不叫 max：Go 1.21+ 内置了 max，同名会遮蔽内置函数，
// 以后有人在同包内写 max(a, b)（比如 int64）就会撞上这里的 int 版本。
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
