package strutil

// EllipsisDots 是三个半角点，用于日志预览等纯文本场景。
const EllipsisDots = "..."

// EllipsisChar 是单个全角省略号，用于需要更省字符的事件属性场景。
const EllipsisChar = "…"

// Truncate 按字符数（rune）截断字符串，超出部分用 "..." 替代。
//
// 按 rune 而不是按字节是为了避免把中文/emoji 切成乱码；
// maxRunes <= 0 时返回空串。
func Truncate(s string, maxRunes int) string {
	return TruncateWith(s, maxRunes, EllipsisDots)
}

// TruncateWith 与 Truncate 相同，但可自定义省略号（传空串表示不追加省略号）。
func TruncateWith(s string, maxRunes int, ellipsis string) string {
	if maxRunes <= 0 || s == "" {
		return ""
	}
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes]) + ellipsis
}
