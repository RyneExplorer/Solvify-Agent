// Package textseg 提供全项目唯一的「文本 → 参与检索匹配的词项」口径。
//
// 为什么必须唯一：建库侧（给 chunk 打关键词）与检索侧（切用户 query）如果各写一份，
// 两边「什么算一个词」的口径就会漂移。这一点已经真实踩过 ——
// chunk 侧曾用穷举 ngram 子串、query 侧用真分词，两侧求交集几乎撞不上，
// 于是「布隆过滤器」这一块明明挂着相关正文，用户搜「布隆」却永远搜不到。
// 所以两侧都必须调用本包，不允许任何模块另写切词规则。
package textseg

import (
	"strings"
	"sync"
	"unicode"

	"github.com/go-ego/gse"

	"solvify-agent/pkg/logger"
	"solvify-agent/pkg/stopwords"
)

var (
	segOnce sync.Once
	segInst gse.Segmenter
)

// segmenter 获取全局 gse 分词实例（懒加载，使用内嵌词典）。
func segmenter() *gse.Segmenter {
	segOnce.Do(func() {
		seg, err := gse.NewEmbed()
		if err != nil {
			logger.Errorf("gse 词典加载失败: %v", err)
			return
		}
		segInst = seg
		logger.Info("gse 词典加载完成")
	})
	return &segInst
}

// Extract 从文本中提取参与检索匹配的词项（小写、去重、已过滤停用词与纯标点）。
//
// 长度要求「≥2 个字符」（按 rune 算，不是按字节）。这不是排版偏好，而是由检索的打分口径决定的：
// 关键词检索的分数是「这条 chunk 覆盖了 query 的多少比例」——
//
//	score = COUNT(chunk 关键词 ∩ query 词项) / cardinality(query 词项)
//
// 而两侧经本包切出的词项都**不存在单字符词条**（中文保留 ≥2 字，英文/数字同样 ≥2 字符）。
// 所以单字符 query 词项（「分」「能」「做」「里」「层」这类由分词切出来的字）
// 分子恒为 0，却照样占一个分母：纯噪声，只会把所有候选分数一起压低，
// 在 keywordScoreThreshold 兜底过滤下甚至能把结果全滤光。故直接丢弃。
// 纯标点（“？”、“，”）同理，用 HasWordChar 兜住。
func Extract(text string) []string {
	seg := segmenter()
	words := seg.Cut(text, true)

	var keywords []string
	seen := make(map[string]bool)
	for _, w := range words {
		w = strings.ToLower(strings.TrimSpace(w))
		if w == "" || len([]rune(w)) < 2 {
			continue
		}
		if !HasWordChar(w) {
			continue
		}
		if stopwords.IsStopWord(w) {
			continue
		}
		if seen[w] {
			continue
		}
		seen[w] = true
		keywords = append(keywords, w)
	}
	return keywords
}

// HasWordChar 判断词项里是否含字母或数字（纯标点/空白/换行的词项对检索无意义）。
func HasWordChar(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return true
		}
	}
	return false
}
