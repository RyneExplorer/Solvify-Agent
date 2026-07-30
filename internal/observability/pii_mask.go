package observability

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

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

func NewPIISanitizer(contentMaxChars int, maskSecret bool) *PIISanitizer {
	if contentMaxChars < 0 {
		contentMaxChars = 0
	}
	return &PIISanitizer{ContentMaxChars: contentMaxChars, MaskSecret: maskSecret}
}

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
