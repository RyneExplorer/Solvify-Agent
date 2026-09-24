package observability

import (
	"regexp"
	"strings"
	"unicode/utf8"

	"solvify-agent/pkg/strutil"
)

// PIISanitizer 负责对文本和 attrs 做 PII 脱敏与截断。
type PIISanitizer struct {
	ContentMaxChars int
	MaskSecret      bool
}

var (
	emailRe        = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)
	phoneRe        = regexp.MustCompile(`(1[3-9]\d)(\d{4})(\d{4})|(\d{3})(\d{4})(\d{4})`)
	secretHeaderRe = regexp.MustCompile(`(?i)(Authorization|Bearer|Api-Key|X-API-Key|X-Auth-Token|Proxy-Authorization)[:=]\s*[^\s,;"']+`)
	skKeyRe        = regexp.MustCompile(`(?i)(sk-|pk-|token|apikey|api_key|secret)[^ \t\n\r]{0,4}[=: ]\s*[A-Za-z0-9_\-]{8,}`)
)

// NewPIISanitizer 构造 PIISanitizer。
func NewPIISanitizer(contentMaxChars int, maskSecret bool) *PIISanitizer {
	if contentMaxChars < 0 {
		contentMaxChars = 0
	}
	return &PIISanitizer{ContentMaxChars: contentMaxChars, MaskSecret: maskSecret}
}

// 内容字段的长度上限（rune）。这些值决定「一次排查能看到多少内容」。
//
// 为什么不复用 ContentMaxChars：它的语义是「单字段兜底截断」，默认 200，
// 作用在所有没有独立长度控制的字符串上（error / url / 杂项 attrs）。
// 内容字段的长度应由产生它的地方按场景决定，两者混用会让调参无从下手，
// 也会让「声明 500 实际 200」这类不一致无从定位。
const (
	// contentLenShort 工具名列表等单行短文本
	contentLenShort = 500
	// contentLenMedium system prompt、单条消息、检索片段
	contentLenMedium = 2000
	// contentLenLong 工具返回、模型回复正文
	contentLenLong = 4000
	// contentLenFull 模型完整输入 messages（多轮工具结果叠加后体积最大）
	contentLenFull = 12000
	// contentHardCap 内容字段的硬上限：调用点漏传长度时的兜底，防止 span_tree 失控
	contentHardCap = 20000
)

// isContentAttr 判断属性名是否属于「内容承载型」。
//
// 判据用命名约定而不是手写清单 —— 手写清单必然漏，而漏掉的字段会被静默截到
// ContentMaxChars(200)，产生侧声明的 4000 全部作废且没有任何报错。
// 所以：内容字段一律以 _preview 结尾即自动豁免；只有 gen_ai 语义约定的那几个名字
// 由标准规定、改不了，必须显式列出。
func isContentAttr(key string) bool {
	if strings.HasSuffix(key, "_preview") {
		return true
	}
	switch key {
	case AttrGenAIInputMessages, AttrGenAIOutputMessages,
		AttrGenAIToolCallArguments, AttrGenAIToolCallResult,
		AttrGenAIRetrievalQueryText:
		return true
	}
	return false
}

// maskOnly 只做 PII mask，不做任何长度截断。
//
// 分工：
//   - SanitizeString  = maskOnly + 按 ContentMaxChars 截断（兜底，作用于无长度控制的字段）
//   - TruncatePreview = maskOnly + 按调用点 maxRunes 截断（精确，作用于内容字段）
//
// s 为 nil 时原样返回，长度由调用方自行保证。
func (s *PIISanitizer) maskOnly(text string) string {
	if s == nil || text == "" {
		return text
	}
	out := text
	if s.MaskSecret {
		out = secretHeaderRe.ReplaceAllStringFunc(out, maskHeaderSecret)
		out = skKeyRe.ReplaceAllStringFunc(out, maskKeyValueSecret)
	}
	out = emailRe.ReplaceAllStringFunc(out, maskEmail)
	out = phoneRe.ReplaceAllStringFunc(out, maskPhone)
	return out
}

// SanitizeString 对字符串做 PII 脱敏并按 ContentMaxChars 截断。
//
// 用途：作用于**没有独立长度控制**的字符串（Span.Error、反馈评论、Agent 步骤摘要等）。
// 内容字段不要走这里，走 TruncatePreview。
func (s *PIISanitizer) SanitizeString(text string) string {
	if s == nil {
		return strutil.TruncateWith(text, 200, strutil.EllipsisChar)
	}
	return strutil.TruncateWith(s.maskOnly(text), s.ContentMaxChars, strutil.EllipsisChar)
}

// SanitizeAttrs 对 attrs 中所有值递归做 PII 脱敏。
//
// 截断策略按属性名分流（见 sanitizeAttrValue）：内容字段只做 mask + 硬上限兜底，
// 长度由产生侧的 TruncatePreview 决定；其余字段按 ContentMaxChars 兜底截断。
//
// 为什么必须分流：落库出口（cleanSpan）与三方导出出口（StartSpan / EndSpan）都调这里，
// 若不分流，产生侧 TruncatePreview(x, 4000) 的结果会被这里无声截回 200 ——
// 长度控制就有两个来源，且小的那个赢。
func (s *PIISanitizer) SanitizeAttrs(attrs Attrs) Attrs {
	if len(attrs) == 0 || s == nil {
		return attrs
	}
	out := make(Attrs, len(attrs))
	for k, v := range attrs {
		out[k] = s.sanitizeAttrValue(k, v)
	}
	return out
}

// sanitizeAttrValue 按「属性名」决定该用哪套截断规则。
//
// key 取外层属性名：嵌套结构里的值沿用同一个 key 判断 ——
// 内容字段的判定依据是它挂在哪个属性上，不是它的嵌套深度。
func (s *PIISanitizer) sanitizeAttrValue(key string, v any) any {
	switch val := v.(type) {
	case string:
		return s.sanitizeStr(key, val)
	case map[string]string:
		m := make(map[string]string, len(val))
		for k, vv := range val {
			m[k] = s.sanitizeStr(key, vv)
		}
		return m
	case Attrs:
		return s.SanitizeAttrs(val)
	case map[string]any:
		m := make(map[string]any, len(val))
		for k, vv := range val {
			m[k] = s.sanitizeAttrValue(key, vv)
		}
		return m
	case []string:
		arr := make([]string, 0, len(val))
		for _, vv := range val {
			arr = append(arr, s.sanitizeStr(key, vv))
		}
		return arr
	default:
		return v
	}
}

// sanitizeStr 是 attrs 字符串值的统一脱敏出口。
func (s *PIISanitizer) sanitizeStr(key, text string) string {
	masked := s.maskOnly(text)
	if isContentAttr(key) {
		return strutil.TruncateWith(masked, contentHardCap, strutil.EllipsisChar)
	}
	return strutil.TruncateWith(masked, s.ContentMaxChars, strutil.EllipsisChar)
}

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
	return user[:2] + strings.Repeat("*", max(3, len(user)-2)) + domain
}

func maskPhone(s string) string {
	if len(s) != 11 {
		if len(s) >= 7 {
			return s[:3] + strings.Repeat("*", len(s)-7) + s[len(s)-4:]
		}
		return s
	}
	return s[:3] + "****" + s[7:]
}

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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// TruncatePreview 对内容字段做「mask + 按 maxRunes 定长」，超长时补 "…(+X chars)" 尾标。
//
// 曾经的缺陷（这里误用了 SanitizeString）：SanitizeString 内部先按 ContentMaxChars(200)
// 截断，于是
//  1. maxRunes 传 500 / 800 时 `total <= maxRunes` 恒真 ⇒ 声明值全部失效，实际恒 200；
//  2. "…(+X chars)" 分支永不可达 ⇒ 前端无法判断到底被砍掉多少字符。
// 现在只 mask、不经过 ContentMaxChars，maxRunes 成为唯一的长度来源。
//
// 典型 maxRunes 见 contentLenShort / contentLenMedium / contentLenLong / contentLenFull。
func (s *PIISanitizer) TruncatePreview(text string, maxRunes int) string {
	if maxRunes <= 0 {
		maxRunes = contentLenShort
	}
	masked := text
	if s != nil {
		masked = s.maskOnly(text)
	}
	if masked == "" {
		return ""
	}
	total := utf8.RuneCountInString(masked)
	if total <= maxRunes {
		return masked
	}
	head := strutil.TruncateWith(masked, maxRunes, strutil.EllipsisChar)
	// head 末尾自带 "…"，去掉再拼接统一尾标。
	head = strings.TrimRight(head, "…")
	return head + "…(+" + itoa(total-maxRunes) + " chars)"
}

// itoa 轻量实现，避免额外依赖 strconv。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	buf := [16]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
