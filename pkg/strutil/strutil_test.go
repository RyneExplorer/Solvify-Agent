package strutil

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncate(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{name: "短于上限时原样返回", in: "abc", max: 10, want: "abc"},
		{name: "刚好等于上限时原样返回", in: "abc", max: 3, want: "abc"},
		{name: "超出上限时加省略号", in: "abcdef", max: 3, want: "abc..."},
		{name: "空串", in: "", max: 10, want: ""},
		{name: "上限为0返回空串", in: "abc", max: 0, want: ""},
		{name: "上限为负返回空串", in: "abc", max: -1, want: ""},

		// 核心：中文必须按 rune 计数，按字节截断会切出乱码
		{name: "中文按字截断不乱码", in: "网络安全攻防演练方案", max: 4, want: "网络安全..."},
		{name: "中文短于上限不截断", in: "网络安全", max: 10, want: "网络安全"},
		{name: "emoji按rune截断不被切开", in: "😀😀😀😀", max: 2, want: "😀😀..."},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Truncate(c.in, c.max)
			if got != c.want {
				t.Fatalf("Truncate(%q, %d) = %q, 期望 %q", c.in, c.max, got, c.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("Truncate(%q, %d) 返回了非法 UTF-8: %q", c.in, c.max, got)
			}
		})
	}
}

// TestTruncateNoSplitRune 直接固化「按字节截断」的旧写法会破坏 UTF-8 这一事实，
// 防止将来有人为了"省一次 rune 转换"改回按字节实现。
func TestTruncateNoSplitRune(t *testing.T) {
	const zh = "网络安全攻防演练方案进行中"
	for max := 1; max <= len([]rune(zh)); max++ {
		got := Truncate(zh, max)
		if !utf8.ValidString(got) {
			t.Fatalf("max=%d 时截断结果不是合法 UTF-8: %q", max, got)
		}
		body := strings.TrimSuffix(got, EllipsisDots)
		if rc := utf8.RuneCountInString(body); rc > max {
			t.Fatalf("max=%d 时正文 %d 字，超出上限: %q", max, rc, got)
		}
	}
}

func TestTruncateWith(t *testing.T) {
	if got := TruncateWith("网络安全攻防", 3, EllipsisChar); got != "网络安…" {
		t.Fatalf("TruncateWith 省略号不生效: %q", got)
	}
	// 空省略号表示只切不标记
	if got := TruncateWith("网络安全攻防", 3, ""); got != "网络安" {
		t.Fatalf("TruncateWith 空省略号应只截断: %q", got)
	}
}
