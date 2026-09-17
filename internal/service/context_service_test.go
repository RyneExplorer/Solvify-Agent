package service

import (
	"slices"
	"strings"
	"testing"

	"solvify-agent/internal/rag"
)

// ─── 「相关历史」关键词口径 ────────────────────────────────────────────────
//
// 回归背景：extractKeywords 原来是本地正则切词（tokenRegexp = [\x{4e00}-\x{9fff}]+），
// 会把一整段连续中文当成**一个**词项。该词项随后进 SearchRecentByKeywords 的
// `content ILIKE '%<词项>%'`，而历史消息里不可能出现这么长的整串 —— 一条都匹配不上，
// 「相关历史」通道因此近似空转。下面把「必须与检索侧同源」钉成断言。

// 中文长句必须被切碎，且切出的词项必须是 rag.ExtractKeywords 的产物。
func TestExtractKeywords_DelegatesToRetrievalTokenizer(t *testing.T) {
	const query = "那网络安全这块你们是怎么做的？"

	got := extractKeywords(query)
	if len(got) == 0 {
		t.Fatal("没切出任何关键词 —— 「相关历史」通道会直接退化成不检索")
	}

	// 口径一致的直接证据：每个词项都能在检索侧词项里找到
	want := rag.ExtractKeywords(query)
	for _, kw := range got {
		if !slices.Contains(want, kw) {
			t.Errorf("词项 %q 不在 rag.ExtractKeywords 结果 %v 里 → 两条路径口径不一致", kw, want)
		}
	}

	// 旧实现只会返回 1 个「整个中文 run」的词项
	if len(got) < 2 {
		t.Errorf("中文长句只切出 %d 个词项 %v，疑似退回整段切词", len(got), got)
	}
}

// 词项条数受 maxHistoryKeywords 约束（每个词项都是一个 ILIKE 条件）。
func TestExtractKeywords_CapsAtMaxHistoryKeywords(t *testing.T) {
	long := strings.Repeat("网络安全 数据加密 访问控制 日志审计 漏洞扫描 权限管理 传输安全 密钥轮换 ", 2)

	got := extractKeywords(long)
	if len(got) > maxHistoryKeywords {
		t.Errorf("词项条数 %d 超过上限 %d: %v", len(got), maxHistoryKeywords, got)
	}
}

// 空串 / 纯空白不得产出词项（否则会生成 `ILIKE '%%'` 这种匹配全表的条件）。
func TestExtractKeywords_BlankQueryYieldsNothing(t *testing.T) {
	for _, q := range []string{"", "   ", "\n\t"} {
		if got := extractKeywords(q); len(got) != 0 {
			t.Errorf("query=%q 不该产出词项: %v", q, got)
		}
	}
}

// 中英混排时英文/数字词项要保留：gse 对拉丁串的切分与检索侧同源。
func TestExtractKeywords_KeepsLatinTerms(t *testing.T) {
	got := extractKeywords("bge-m3 和 pgvector 哪个更适合做向量检索")

	for _, want := range []string{"pgvector", "bge-m3"} {
		if slices.Contains(got, want) {
			return
		}
	}
	t.Errorf("中英混排里的英文词项被丢了: %v", got)
}
