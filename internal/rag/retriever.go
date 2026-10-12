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

// defaultCandidateMultiplier 是候选放大系数的兜底值：先按 TopK×N 多取候选，
// 融合 / 阈值过滤后再收敛到 TopK。可通过 HybridRetrieverConfig.CandidateMultiplier 调整。
//
// 为什么是 1 而不是 2：2026-09-23 的 A/B 实测（test1/rag_eval/AB-对比结论-20260923.md）
// 把每侧候选从 6 收到 3，精度 82.1% → 95.7%、含噪率 17.9% → 4.3%，
// 而 hit@1 / hit@3 / MRR 全部持平（都已满分）—— 噪声原先就是从第 4~6 名候选进来的。
// 真实风险面：单侧排到第 4 名及以后的 gold 拿不到了 —— 但 2026-09-23 的补题实验里
// 观察到的 e-go-gmp 掉一条**不是**这个原因：gold 在关键词侧一直排第 1/2，从未被切出候选池。
// 真因是**排序非全序**：`ORDER BY score DESC` 后面没有唯一键，而 `LIMIT` 会换 PG 的排序算法
// （7 行输入：3=top-N heapsort、≥4=quicksort，执行计划文本完全相同），
// 于是两条同分块（0.75 vs 0.75）的先后随候选池大小翻转。
// 已于 2026-09-24 修正：三条检索 SQL + 两处 Go 侧排序都补了唯一键兜底。
// ⚠️ 因此报告里「候选池 6 ⇒ chunk 级 hit@1 5/5」那一格是 quicksort 的运气、不是旧配置的功劳；
// 修完全序后该题稳定为 4/5（gold 的 uuid 字典序更靠后）。A/B 对比表该格作废。
// ⚠️ 而 B 的真实风险面（gold 单侧排到第 4 名之后）在 5 条硬题上**没有发生** ⇒ 仍无安全证明：
// 扩题集时要专造这类题（详见报告 §10）。
// ⚠️ 它与 minMaxNormalize 有耦合：归一化把每侧**最后一名**压成 0，crossSourceFilter 又
// 丢掉「单源且 < 0.05」的结果 ⇒ 每侧最后一名必然出局，所以实际存活是每侧 TopK-1 条。
// 动归一化之前先看那份报告，两者不能各自独立演进。
const defaultCandidateMultiplier = 1

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

// candidateLimitWith 返回送入融合 / 过滤之前的候选条数 = TopK × multiplier。
//
// multiplier <= 0 时回落到 defaultCandidateMultiplier：让"没配置"与"配成 0"
// 不会被同一处理 —— 配成 0 若直接参与乘法就是 `LIMIT 0` 恒空，是静默失效。
func (q Query) candidateLimitWith(multiplier int) int {
	if multiplier <= 0 {
		multiplier = defaultCandidateMultiplier
	}
	return q.effectiveTopK() * multiplier
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
