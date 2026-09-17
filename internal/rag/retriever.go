package rag

import (
	"context"
	"strings"
)

// Retriever 定义检索器接口
type Retriever interface {
	Retrieve(ctx context.Context, query Query) (Result, error)
}

// Query 描述检索请求
type Query struct {
	Question string
	// KeywordQuery 是关键字（数组重叠 + 命中率打分）检索专用的 query。
	//
	// 混合检索的两路对 query 长度有相反的偏好：
	//   - 向量侧：语义模型对长文本鲁棒，多给上下文（最近几轮用户提问）能提高召回；
	//   - 关键字侧：打分是「chunk 覆盖了 query 的多少比例」——
	//     score = COUNT(chunk 关键词 ∩ query 词项) / cardinality(query 词项)，
	//     分母就是 query 自己的词项数 → query 越长，所有候选分数被同一比例压低、
	//     排序被拉平，还会撞上 keywordScoreThreshold 把候选成片滤掉，
	//     于是关键字侧要用「短且含实体」的 query。
	//
	// 为空时回退到 Question（深度模式的检索 query 由 Agent 自己构造，不区分两路）。
	KeywordQuery     string
	TopK             int
	KnowledgeBaseIDs []string
	UserID           string
}

// keywordQueryText 返回关键字检索应使用的文本：优先用 KeywordQuery，缺省回退 Question。
func (q Query) keywordQueryText() string {
	if s := strings.TrimSpace(q.KeywordQuery); s != "" {
		return s
	}
	return q.Question
}

// defaultTopK 是调用方未指定 TopK 时的兜底召回量。
const defaultTopK = 5

// candidateMultiplier 是候选放大系数：先按 TopK×N 多取候选，融合 / 阈值过滤后再收敛到 TopK。
const candidateMultiplier = 2

// effectiveTopK 返回生效的 topK，保证 > 0。
//
// 所有检索路径都必须走它。曾经的坑：Retrieve 里算了一遍兜底，而 vectorSearch /
// keywordSearch 又各自重算 `query.TopK * 2`，兜底根本传不下去 —— 调用方漏传 TopK
// 就是 `LIMIT 0` 恒空（线上调用方恰好都传了 3，所以一直没暴露）。
// 把「解析 TopK」收口成一个方法，两套口径就结构上无法再分叉。
func (q Query) effectiveTopK() int {
	if q.TopK > 0 {
		return q.TopK
	}
	return defaultTopK
}

// candidateLimit 返回送入融合 / 过滤之前的候选条数。
func (q Query) candidateLimit() int {
	return q.effectiveTopK() * candidateMultiplier
}

// Result 描述检索结果
type Result struct {
	Hit       bool
	Documents []Document
}

// Document 描述检索到的文档片段
type Document struct {
	ID              string
	KnowledgeBaseID string
	DocumentID      string
	VersionID       string
	ChunkIndex      int
	Title           string
	Content         string
	Score           float64
}
