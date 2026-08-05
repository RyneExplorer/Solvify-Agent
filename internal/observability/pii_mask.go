package observability

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// PIISanitizer 负责对文本和 attrs 做 PII 脱敏与截断。
type PIISanitizer struct {
	ContentMaxChars int
	MaskSecret      bool
}

var (
	emailRe           = regexp.MustCompile(`(?i)[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}`)
	phoneRe           = regexp.MustCompile(`(1[3-9]\d)(\d{4})(\d{4})|(\d{3})(\d{4})(\d{4})`)
	secretHeaderRe    = regexp.MustCompile(`(?i)(Authorization|Bearer|Api-Key|X-API-Key|X-Auth-Token|Proxy-Authorization)[:=]\s*[^\s,;"']+`)
	skKeyRe           = regexp.MustCompile(`(?i)(sk-|pk-|token|apikey|api_key|secret)[^ \t\n\r]{0,4}[=: ]\s*[A-Za-z0-9_\-]{8,}`)
)

// NewPIISanitizer 构造 PIISanitizer。
func NewPIISanitizer(contentMaxChars int, maskSecret bool) *PIISanitizer {
	if contentMaxChars < 0 {
		contentMaxChars = 0
	}
	return &PIISanitizer{ContentMaxChars: contentMaxChars, MaskSecret: maskSecret}
}

// SanitizeString 对字符串做 PII 脱敏并按字符数截断。
func (s *PIISanitizer) SanitizeString(text string) string {
	if s == nil {
		return truncateRunes(text, 200)
	}
	out := text
	if s.MaskSecret {
		out = secretHeaderRe.ReplaceAllStringFunc(out, maskHeaderSecret)
		out = skKeyRe.ReplaceAllStringFunc(out, maskKeyValueSecret)
	}
	out = emailRe.ReplaceAllStringFunc(out, maskEmail)
	out = phoneRe.ReplaceAllStringFunc(out, maskPhone)
	out = truncateRunes(out, s.ContentMaxChars)
	return out
}

// SanitizeAttrs 对 attrs 中所有值递归做 PII 脱敏。
func (s *PIISanitizer) SanitizeAttrs(attrs Attrs) Attrs {
	if len(attrs) == 0 || s == nil {
		return attrs
	}
	out := make(Attrs, len(attrs))
	for k, v := range attrs {
		out[k] = s.sanitizeValue(v)
	}
	return out
}

func (s *PIISanitizer) sanitizeValue(v any) any {
	switch val := v.(type) {
	case string:
		return s.SanitizeString(val)
	case map[string]string:
		m := make(map[string]string, len(val))
		for k, vv := range val {
			m[k] = s.SanitizeString(vv)
		}
		return m
	case Attrs:
		return s.SanitizeAttrs(val)
	case map[string]any:
		m := make(map[string]any, len(val))
		for k, vv := range val {
			m[k] = s.sanitizeValue(vv)
		}
		return m
	case []string:
		arr := make([]string, 0, len(val))
		for _, vv := range val {
			arr = append(arr, s.SanitizeString(vv))
		}
		return arr
	default:
		return v
	}
}

func truncateRunes(s string, max int) string {
	if max <= 0 || s == "" {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	r := []rune(s)
	if max > len(r) {
		max = len(r)
	}
	return string(r[:max]) + "…"
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

// TruncatePreview 对 attrs 的「预览字段」做两段式控制：
//  1. 先统一做 PII mask（SanitizeString）
//  2. 再按 maxRunes 截断，并在尾部加 "…(+X chars)"，
//     既保证前端能看到关键片段，又防止 attrs 把 span_tree JSON 撑爆。
//
// 典型 maxRunes：query/args_preview = 300；response_preview/top doc snippet = 500；
// short_preview（工具名列表/last_user_msg_preview）= 200。
func (s *PIISanitizer) TruncatePreview(text string, maxRunes int) string {
	if s == nil {
		return truncateRunes(text, 300)
	}
	if maxRunes <= 0 {
		maxRunes = 200
	}
	masked := s.SanitizeString(text)
	if masked == "" {
		return ""
	}
	total := utf8.RuneCountInString(masked)
	if total <= maxRunes {
		return masked
	}
	head := truncateRunes(masked, maxRunes)
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
