package rag

import (
	"strings"
	"testing"

	"solvify-agent/pkg/textseg"
)

func TestQueryKeywordQueryText(t *testing.T) {
	tests := []struct {
		name  string
		query Query
		want  string
	}{
		{
			name:  "未指定关键字 query 时回退到公共 query",
			query: Query{Question: "支持哪些协议"},
			want:  "支持哪些协议",
		},
		{
			name:  "指定了关键字 query 时优先用它",
			query: Query{Question: "Redis 的超时时间. 那它的超时时间是多少", KeywordQuery: "redis 的超时时间是多少"},
			want:  "redis 的超时时间是多少",
		},
		{
			name:  "关键字 query 只有空白时回退到公共 query",
			query: Query{Question: "支持哪些协议", KeywordQuery: "   "},
			want:  "支持哪些协议",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.query.keywordQueryText(); got != tt.want {
				t.Errorf("keywordQueryText()=%q 期望 %q", got, tt.want)
			}
		})
	}
}

func TestExtractKeywords(t *testing.T) {
	got := ExtractKeywords("Redis 的超时时间怎么配置")
	if len(got) == 0 {
		t.Fatal("分词结果为空")
	}
	seen := make(map[string]int, len(got))
	for _, k := range got {
		if k != strings.ToLower(k) {
			t.Errorf("关键词 %q 未转小写", k)
		}
		seen[k]++
	}
	for k, n := range seen {
		if n > 1 {
			t.Errorf("关键词 %q 重复出现 %d 次", k, n)
		}
	}
	// 停用词「的」「怎么」必须被过滤
	for _, w := range []string{"的", "怎么"} {
		if _, ok := seen[w]; ok {
			t.Errorf("停用词 %q 不应出现在关键词里: %v", w, got)
		}
	}
	// 纯标点不能成为关键词：它匹配不到任何文档的关键词，
	// 只会抬高关键字命中率的分母、压低全部候选分。
	for _, k := range got {
		if !textseg.HasWordChar(k) {
			t.Errorf("纯标点词项 %q 不应出现在关键词里: %v", k, got)
		}
	}
}

func TestExtractKeywordsDropsSingleChar(t *testing.T) {
	// chunk 侧关键词是 2~12 字 ngram + ≥2 字符英文/数字串，不存在单字符词条，
	// 所以单字 query 词项分子恒为 0、却占一个分母 → 必须丢弃。
	got := ExtractKeywords("那它一共分了几层？")
	seen := make(map[string]bool, len(got))
	for _, k := range got {
		seen[k] = true
	}
	// 「分」是单字，必须丢
	if seen["分"] {
		t.Errorf("单字符词项「分」不应出现在关键词里: %v", got)
	}
	// 多字词项必须保留（否则等于把 query 清空）
	for _, want := range []string{"一共", "几层"} {
		if !seen[want] {
			t.Errorf("多字词项 %q 丢失: %v", want, got)
		}
	}
	t.Logf("关键词=%v", got)
}

func TestExtractKeywordsDropsPunctuation(t *testing.T) {
	// gse 会把「？\n\n（」切成一个词项，它含换行与标点但不含任何字母数字
	got := ExtractKeywords("Redis 的超时时间怎么配置？\n\n（内容过长，已截断）")
	joined := strings.Join(got, "|")
	if strings.Contains(joined, "？") || strings.Contains(joined, "，") || strings.Contains(joined, "\n") {
		t.Errorf("关键词里混入标点: %v", got)
	}
	// 正文词项必须保留
	for _, want := range []string{"redis", "超时", "时间", "配置"} {
		found := false
		for _, k := range got {
			if k == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("关键词 %q 丢失: %v", want, got)
		}
	}
}
