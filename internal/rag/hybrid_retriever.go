package rag

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"solvify-agent/internal/model/entity"
	"solvify-agent/pkg/config"
	"solvify-agent/pkg/logger"
	"solvify-agent/pkg/textseg"
)

// HybridRetriever 实现混合检索（向量 + 关键词 + RRF 融合）
type HybridRetriever struct {
	db                    *gorm.DB
	embeddingFunc         EmbeddingFunc
	scoreThreshold        float64
	vectorWeight          float64
	keywordWeight         float64
	keywordScoreThreshold float64 // 向量全灭时，关键词结果的最低匹配比例
	rrfK                  float64
	keywordIDFWeighted    bool // 关键词打分是否给罕见词加权，见 HybridRetrieverConfig 说明
	candidateMultiplier   int  // 候选放大系数（TopK × N）
}

// HybridRetrieverConfig 描述混合检索器配置
type HybridRetrieverConfig struct {
	DB                    *gorm.DB
	EmbeddingFunc         EmbeddingFunc
	ScoreThreshold        float64
	VectorWeight          float64
	KeywordWeight         float64
	KeywordScoreThreshold float64
	RRFK                  float64
	// KeywordIDFWeighted 打开后，关键词打分改为「按词的罕见程度加权」（默认关闭 = 所有词同权）。
	//
	// 为什么需要这个开关：现状打分是「命中词项数 ÷ query 词项数」，分母只看**命中个数**，
	// 不看命中的是「分区表」还是「每次」。词表从每块 20 项扩到 150+ 项之后，
	// 任意一块正文都能撞上几个通用词，于是无关块与正确块在同一个候选池里同权竞争。
	// 打开后权重 w(t) = 1/(1+df(t))（df = 命中该词的可见块数）：
	// 泛化词 df 大 ⇒ 权重趋 0；罕见词/专名 df 小 ⇒ 权重趋 1。
	// ⚠️ df=0（该词在目标库里一条都没有）时权重 = 1，即与旧口径**持平**：这类词对每条候选的分子
	// 都是 0，只把分母抬成同一个倍数，所以**不改变排序**，只影响绝对分（唯一后果是 vector 全灭时那条阈值）。
	// 之所以不发散成 0：那是**第二个**改动，会把「给罕见词加权」和「重定义覆盖率分母」捆在一起，
	// 实验就说不清是谁的功劳。全部 w=1 时与原公式**逐字等价**。
	KeywordIDFWeighted bool
	// CandidateMultiplier 是候选放大系数：先按 TopK×N 多取候选，融合 / 阈值过滤后再收敛到 TopK。
	// <= 0 时用默认值。调小它 = 收紧两侧进入融合的候选池。
	CandidateMultiplier int
}

// NewHybridRetriever 创建混合检索器
func NewHybridRetriever(cfg HybridRetrieverConfig) *HybridRetriever {
	threshold := cfg.ScoreThreshold
	if threshold <= 0 {
		threshold = 0.5
	}
	vectorWeight := cfg.VectorWeight
	if vectorWeight <= 0 {
		vectorWeight = 0.7
	}
	keywordWeight := cfg.KeywordWeight
	if keywordWeight <= 0 {
		keywordWeight = 0.3
	}
	keywordScoreThreshold := cfg.KeywordScoreThreshold
	if keywordScoreThreshold <= 0 {
		keywordScoreThreshold = 0.25
	}
	rrfK := cfg.RRFK
	if rrfK <= 0 {
		rrfK = 60
	}
	candidateMultiplier := cfg.CandidateMultiplier
	if candidateMultiplier <= 0 {
		candidateMultiplier = defaultCandidateMultiplier
	}
	return &HybridRetriever{
		db:                    cfg.DB,
		embeddingFunc:         cfg.EmbeddingFunc,
		scoreThreshold:        threshold,
		vectorWeight:          vectorWeight,
		keywordWeight:         keywordWeight,
		keywordScoreThreshold: keywordScoreThreshold,
		rrfK:                  rrfK,
		keywordIDFWeighted:    cfg.KeywordIDFWeighted,
		candidateMultiplier:   candidateMultiplier,
	}
}

// NewHybridRetrieverFromConfig 从全局配置创建混合检索器
func NewHybridRetrieverFromConfig(db *gorm.DB, embeddingFunc EmbeddingFunc) *HybridRetriever {
	cfg := config.Get().RAG
	return NewHybridRetriever(HybridRetrieverConfig{
		DB:             db,
		EmbeddingFunc:  embeddingFunc,
		ScoreThreshold: cfg.ScoreThreshold,
		VectorWeight:   cfg.VectorWeight,
		KeywordWeight:  cfg.KeywordWeight,
		// KeywordScoreThreshold 曾经漏传：字段有默认值、构造器也认，
		// 但这个唯一的配置入口不传它 → 线上恒为硬编码的 0.25，配置里也没有对应项，
		// 想调只能改代码。这里补上，并在 pkg/config 里加了 keyword_score_threshold。
		KeywordScoreThreshold: cfg.KeywordScoreThreshold,
		RRFK:                  cfg.RRFK,
		CandidateMultiplier:   cfg.CandidateMultiplier,
	})
}

// scoredChunk 描述带分数的检索结果
type scoredChunk struct {
	ID              string  `gorm:"column:id"`
	KnowledgeBaseID string  `gorm:"column:knowledge_base_id"`
	DocumentID      string  `gorm:"column:document_id"`
	VersionID       string  `gorm:"column:version_id"`
	ChunkIndex      int     `gorm:"column:chunk_index"`
	Title           string  `gorm:"column:title"`
	Content         string  `gorm:"column:content"`
	Score           float64 `gorm:"column:score"`
	Keywords        string  `gorm:"column:keywords"`
}

// Retrieve 执行混合检索
func (r *HybridRetriever) Retrieve(ctx context.Context, query Query) (Result, error) {
	if len(query.KnowledgeBaseIDs) == 0 {
		return Result{Hit: false, Documents: nil}, nil
	}

	topK := query.effectiveTopK()
	startedAt := time.Now()

	logger.Infof("混合检索开始: vectorQuery=%q, keywordQuery=%q, topK=%d, knowledgeBaseIDs=%v",
		query.Question, query.keywordQueryText(), topK, query.KnowledgeBaseIDs)

	// 并行执行向量检索和关键词检索
	type vectorResult struct {
		docs []scoredChunk
		err  error
	}
	type keywordResult struct {
		docs []scoredChunk
		err  error
	}

	vectorCh := make(chan vectorResult, 1)
	keywordCh := make(chan keywordResult, 1)

	// 向量检索
	go func() {
		docs, err := r.vectorSearch(ctx, query)
		vectorCh <- vectorResult{docs: docs, err: err}
	}()

	// 关键词检索
	go func() {
		docs, err := r.keywordSearch(ctx, query)
		keywordCh <- keywordResult{docs: docs, err: err}
	}()

	// 等待两个检索完成
	vr := <-vectorCh
	kr := <-keywordCh

	// 向量检索失败时降级：仅用关键词结果，不阻断检索
	if vr.err != nil {
		logger.Warnf("向量检索失败，降级为纯关键词检索: %v", vr.err)
		vr.docs = nil // 清空，后续只用关键词结果
	}
	if kr.err != nil {
		logger.Warnf("关键词检索失败，降级为纯向量检索: %v", kr.err)
		kr.docs = nil
	}

	// 两种检索都失败才报错
	if vr.err != nil && kr.err != nil {
		return Result{}, fmt.Errorf("混合检索完全失败: 向量(%v), 关键词(%v)", vr.err, kr.err)
	}


	logger.Infof("向量检索命中: %d 条, 关键词检索命中: %d 条", len(vr.docs), len(kr.docs))

	// ===== Step 1: 同源质量检查（各自独立过滤） =====

	// 1a. 向量侧：绝对阈值过滤（余弦相似度 >= scoreThreshold）
	filteredVector := make([]scoredChunk, 0, len(vr.docs))
	for _, doc := range vr.docs {
		if doc.Score >= r.scoreThreshold {
			filteredVector = append(filteredVector, doc)
		}
	}

	// 1b. 关键词侧：陡峭度检测 + 最低匹配过滤
	filteredKeyword := r.keywordSourceFilter(kr.docs)

	// 1c. 向量全灭时，对关键词结果加最低匹配比例过滤
	if len(filteredVector) == 0 && len(filteredKeyword) > 0 {
		filteredKeyword = filterByMinScore(filteredKeyword, r.keywordScoreThreshold, "关键词")
	}

	// ===== Step 2: 同源内 Min-Max 归一化 =====
	vectorNorm := minMaxNormalize(filteredVector)
	keywordNorm := minMaxNormalize(filteredKeyword)

	// ===== Step 3: RRF 融合 =====
	fusedRaw := r.reciprocalRankFusion(filteredVector, filteredKeyword)

	// ===== Step 4: 跨源交叉验证 =====
	fused := r.crossSourceFilter(fusedRaw, filteredVector, filteredKeyword, vectorNorm, keywordNorm)

	// ===== Step 5: TopK 截取 =====
	docs := make([]Document, 0, len(fused))
	for _, item := range fused {
		if len(docs) >= topK {
			break
		}
		docs = append(docs, Document{
			ID:              item.ID,
			KnowledgeBaseID: item.KnowledgeBaseID,
			DocumentID:      item.DocumentID,
			VersionID:       item.VersionID,
			ChunkIndex:      item.ChunkIndex,
			Title:           item.Title,
			Content:         item.Content,
			Score:           item.Score,
		})
	}

	logger.Infof("混合检索最终结果: %d 条 (向量过滤阈值=%.2f, TopK=%d, 向量候选=%d, 关键词候选=%d)",
		len(docs), r.scoreThreshold, topK, len(filteredVector), len(filteredKeyword))

	// 每次检索的一行结构化摘要：把漏斗各阶段计数与最终命中的 chunk 一起打出，
	// 便于本地评测时直接对照 gold（chunk 级）算 hit@k / MRR，无需另接指标系统。
	// 注意：只打印 id 与计数，不打印 chunk 正文（日志规范禁止输出正文与密钥）。
	logger.Infof("检索摘要 | 耗时=%s 向量原始=%d 关键词原始=%d 向量过滤=%d 关键词过滤=%d 融合=%d 交叉过滤=%d 最终=%d topK=%d 命中=[%s]",
		time.Since(startedAt).Round(time.Millisecond),
		len(vr.docs), len(kr.docs), len(filteredVector), len(filteredKeyword),
		len(fusedRaw), len(fused), len(docs), topK, chunkIDPreview(docs))

	return Result{
		Hit:       len(docs) > 0,
		Documents: docs,
	}, nil
}

// retrievedChunkVisibilitySQL 是可见性边界在本包内的引用名。
//
// ⚠️ 定义已上移到 entity.RetrievedChunkVisibilitySQL：这条约束现在有**两个**使用方
// （本包的 3 条检索 SQL + repository 的关键字搜索 SQL），留在本包会让另一个复制一份，
// 于是「什么算可见」就有了两个来源。这里保留本别名，只为让既有引用与守卫断言不必改动。
var retrievedChunkVisibilitySQL = entity.RetrievedChunkVisibilitySQL

// vectorSearch 执行向量检索
// 优化：主查询只查 document_chunks 表（不 LEFT JOIN documents），
// 向量距离排序在 chunks 表上直接跑，拿候选数后再批量查 documents 表的 title。
// 避免对所有候选 chunk 做额外 JOIN。
//
// 注意 dc.embedding IS NOT NULL 在向量侧是**必需**的（没有向量就没法算距离），
// 但它在关键词侧是多余的 —— 见 keywordSearchSQL。
var vectorSearchSQL = `
		SELECT
			dc.id,
			dc.knowledge_base_id,
			dc.document_id,
			dc.version_id,
			dc.chunk_index,
			dc.content,
			1 - (dc.embedding <=> ?::vector) AS score,
			COALESCE(dc.keywords::text, '{}') as keywords
		FROM document_chunks dc
		WHERE dc.knowledge_base_id IN (?)
			AND dc.embedding IS NOT NULL
			AND dc.user_id = ?` + retrievedChunkVisibilitySQL + `
		ORDER BY dc.embedding <=> ?::vector, dc.id
		LIMIT ?`

// vectorSearchArgs 按 vectorSearchSQL 里 ? 的出现顺序组装参数。
//
// 单独抽出来有两个理由：
//  1. 5 个位置参数里有 2 个是同一个 vectorStr（SELECT 与 ORDER BY 各一次），内联极易错位；
//  2. 末位 LIMIT 走 query.candidateLimitWith(r.candidateMultiplier)，于是能被测试直接断言。
//     这点很关键：曾经 Retrieve 里算了一遍兜底、两条 search 却各自重算 `query.TopK * 2`，
//     调用方漏传 TopK 就静默变成 `LIMIT 0` 恒空；而单测若只覆盖 effectiveTopK 本身，
//     抓不到"调用点没走它"这种回退。
func (r *HybridRetriever) vectorSearchArgs(query Query, vectorStr string) []any {
	return []any{vectorStr, query.KnowledgeBaseIDs, query.UserID, vectorStr,
		query.candidateLimitWith(r.candidateMultiplier)}
}

func (r *HybridRetriever) vectorSearch(ctx context.Context, query Query) ([]scoredChunk, error) {
	embedding, err := r.embeddingFunc(ctx, query.Question)
	if err != nil {
		return nil, fmt.Errorf("生成查询向量失败: %w", err)
	}

	vectorStr := vectorToString(embedding)

	var results []scoredChunk
	err = r.db.WithContext(ctx).Raw(vectorSearchSQL, r.vectorSearchArgs(query, vectorStr)...).Scan(&results).Error

	if err != nil {
		return nil, err
	}

	// 批量填 title（只对 topK*2 条查，开销可忽略）
	batchFillTitles(r.db, results)

	logger.Infof("向量检索原始结果: %d 条", len(results))
	return results, nil
}

// keywordSearchSQL 是关键词检索 SQL。
//
// 打分口径：score = Σ(命中词项的权重) / Σ(query 全部词项的权重)。
//   - 权重全为 1 时（KeywordIDFWeighted=false）：分子 = 命中个数、分母 = query 词项数，
//     与旧的 `COUNT(*) / cardinality(...)` **逐字等价**；
//   - 开启加权时：weight(t) = 1/(1+df(t))，df 是**可见块**里命中该词的块数
//     ⇒ 泛化词（`每次`/`结果`/`数据`）权重趋 0，专名权重趋 1；df=0（库里一条都没有）时权重 = 1。
//
// ⚠️ 这个权重里**没有 N**（语料总块数），所以它只反映「这块比别的块稀有」，不反映「整个库有多大」。
// 15 块的评测库里 df∈[1,15] ⇒ 权重跨度 8 倍；生产库里 df 能到 10 万 ⇒ 跨度上万倍，效果会比评测猛得多。
// 换言之：这个评测只能证「方向对不对」，证不了「生产上会不会过猛」。
//
// 为什么用乘法权重而不是另写一条 SQL：两条 SQL 只差一个表达式，是"条件串味"的温床；
// 权重全 1 时这条 SQL 退化成原口径，所以只有一处需要维护、也只有一处需要登记可见性边界。
//
// ⚠️ 分母是**全部** query 词项（含一条都匹配不到的），与旧口径一致 —— 不额外引入第二个变量。
//
// ⚠️ 这里**不能**带 `dc.embedding IS NOT NULL`。那个条件是从向量检索抄过来的：
// 关键词命中与这条 chunk 有没有向量毫无关系。带上它的后果是 —— 向量化失败
// （embedding 生成报错、模型没起、文档处理中途失败）的 chunk 在关键词侧**永久不可见**，
// 而关键词侧本来就是这类 chunk 唯一的救命通道。向量侧保留该条件是必需的（要算距离）。
//
// ⚠️ 末尾的 `, dc.id` 不是装饰：没有它，这条排序就**不是全序**。
// 命中率型口径的分值只有 k+1 种取值（k = query 词项数）⇒ 平局是常态，不是例外；
// 而并列行的先后由 PG 的**排序算法**决定，`LIMIT` 一变算法就变
// （7 行输入：LIMIT 3 走 top-N heapsort、≥4 走 quicksort，**执行计划文本完全相同**，
// 只有 EXPLAIN ANALYZE 的 Sort Method 不同）。
// 实测：只把候选池从 6 收到 3，gold 就从第 1 名掉到第 2 名 —— 而 gold 从未被切出候选池。
// ⇒ 后果不是"名次错一点"，而是**让 A/B 实验把"排序算法换了"读成"配置有效果"**。
// 守卫：hybrid_retriever_sql_test.go 的 TestAllChunkReadSQLsAreTotallyOrdered。
var keywordSearchSQL = `
		WITH qw AS (
			SELECT t.term,
			       CASE WHEN ?::boolean THEN
			              1.0 / (1.0 + (
			                  SELECT COUNT(*)::float
			                  FROM document_chunks dc
			                  WHERE dc.knowledge_base_id IN (?)
			                    AND dc.keywords IS NOT NULL
			                    AND dc.keywords && ARRAY[t.term]
			                    AND dc.user_id = ?` + retrievedChunkVisibilitySQL + `
			              ))
			            ELSE 1.0 END AS weight
			FROM unnest(?::text[]) AS t(term)
		),
		qnorm AS (SELECT GREATEST(SUM(weight), 1e-9) AS total FROM qw)
		SELECT
			dc.id,
			dc.knowledge_base_id,
			dc.document_id,
			dc.version_id,
			dc.chunk_index,
			dc.content,
			(
				SELECT COALESCE(SUM(qw.weight), 0)
				FROM unnest(dc.keywords) AS kw
				JOIN qw ON qw.term = kw
			) / (SELECT total FROM qnorm) AS score,
			COALESCE(dc.keywords::text, '{}') as keywords
		FROM document_chunks dc
		WHERE dc.knowledge_base_id IN (?)
			AND dc.keywords IS NOT NULL
			AND dc.keywords && ?::text[]
			AND dc.user_id = ?` + retrievedChunkVisibilitySQL + `
		ORDER BY score DESC, dc.id
		LIMIT ?`

// chunkReadSQLs 登记所有「读取 chunk 内容、可能把内容交给用户」的检索 SQL。
//
// 存在的意义：可见性边界必须是**每一个** chunk 出口的共同约束，但 Go 的类型系统管不到
// SQL 文本 —— 「新加一条检索路径忘了过滤软删文档」正是本次缺陷的形态。把出口登记到一处，
// 配合 TestAllChunkReadSQLsCarryVisibilityBoundary 就把它从「靠人记得」变成「测试变红」。
//
// ⚠️ 新增任何读取 chunk 内容的检索 SQL，必须登记到这里，否则该测试覆盖不到它。
var chunkReadSQLs = map[string]string{
	"vectorSearchSQL":         vectorSearchSQL,
	"keywordSearchSQL":        keywordSearchSQL,
	"expandAdjacentChunksSQL": expandAdjacentChunksSQL,
}

// keywordSearch 执行关键词检索
// 优化：GIN 索引加速 && overlap 过滤（主收益），unnest 仅对过滤后的少量行计算分数
// 也去掉了 LEFT JOIN documents，title 在主查询完成后批量填
//
// 打分口径（注意不是 BM25）：score = COUNT(chunk 关键词 ∩ query 词项) / cardinality(query 词项)，
// 即「这条 chunk 覆盖了 query 的多少比例」—— 没有词频、没有 IDF、没有 chunk 长度归一化。
// 分母完全由 query 决定，所以 query 越长，所有候选的分数被同一比例压得越低；
// 而 vector 全灭时才启用的 keywordScoreThreshold（默认 0.25）会把这些被压低的候选成片滤掉。
//
// 因此 query 文本取 keywordQueryText()：调用方（快速模式）传「实体回填后的短 query」，
// 避免把最近几轮用户提问拼进来抬高分母、把排序拉向历史话题。

// keywordSearchArgs 按 keywordSearchSQL 里 ? 的出现顺序组装参数（理由同 vectorSearchArgs）。
//
// 8 个位置参数里 keywordArray 出现 2 次（CTE 算权重 + 主查询的 && 过滤）、
// kbIDs 出现 2 次（CTE 统计 df + 主查询）、userID 出现 2 次，内联极易错位。
// ⚠️ 位置参数一旦错位不会报错：轻则查不到、重则把 user_id 当知识库 id 用，只有跑真库才暴露。
func (r *HybridRetriever) keywordSearchArgs(query Query, keywordArray string) []any {
	return []any{
		r.keywordIDFWeighted,   // ?1 权重模式开关
		query.KnowledgeBaseIDs, // ?2 CTE 统计 df 用的知识库范围
		query.UserID,           // ?3 CTE 统计 df 用的 user_id
		keywordArray,           // ?4 CTE 的词项
		query.KnowledgeBaseIDs, // ?5 主查询知识库范围
		keywordArray,           // ?6 主查询 keywords && ?
		query.UserID,           // ?7 主查询 user_id
		query.candidateLimitWith(r.candidateMultiplier), // ?8 LIMIT
	}
}

func (r *HybridRetriever) keywordSearch(ctx context.Context, query Query) ([]scoredChunk, error) {
	keywords := textseg.Extract(query.keywordQueryText())
	if len(keywords) == 0 {
		return nil, nil
	}

	var results []scoredChunk

	keywordArray := buildPostgresArray(keywords)

	err := r.db.WithContext(ctx).Raw(keywordSearchSQL, r.keywordSearchArgs(query, keywordArray)...).Scan(&results).Error

	if err != nil {
		return nil, err
	}

	batchFillTitles(r.db, results)

	// 过滤零分结果
	filtered := results[:0]
	for _, r := range results {
		if r.Score > 0 {
			filtered = append(filtered, r)
		}
	}

	logger.Infof("关键词检索原始结果: %d 条(有效), 关键词: %v", len(filtered), keywords)
	return filtered, nil
}

// ExtractKeywords 用与关键词检索完全一致的分词 + 停用词口径从文本中提取词项。
// 供上层（service 层构造检索 query、做实体回填）复用，保证「规划出的词」
// 与「实际参与关键字匹配的词」是同一套口径。
//
// 实现已收敛到 pkg/textseg：那份口径同时被建库侧（给 chunk 打关键词）使用，
// 两侧调同一个函数，才能从结构上排除「一边产词、一边产子串」这类不一致。
func ExtractKeywords(text string) []string {
	return textseg.Extract(text)
}

// batchFillTitles 对检索结果批量填充文档标题。
// 把 LEFT JOIN documents 从主查询里拆出来——主查询只跑 chunks 表排序取 topK，
// 然后对这少量结果的 document_id 做一次 IN 查询拿 title，JOIN 开销从 O(全量 chunks) 降到 O(topK)。
func batchFillTitles(db *gorm.DB, chunks []scoredChunk) {
	if db == nil || len(chunks) == 0 {
		return
	}
	// 收集去重的 document_id
	docIDs := make(map[string]struct{})
	for _, c := range chunks {
		if c.DocumentID != "" {
			docIDs[c.DocumentID] = struct{}{}
		}
	}
	if len(docIDs) == 0 {
		return
	}
	idList := make([]string, 0, len(docIDs))
	for id := range docIDs {
		idList = append(idList, id)
	}

	var rows []struct {
		ID    string
		Title string
	}
	if err := db.Raw("SELECT id, title FROM documents WHERE id IN ?", idList).Scan(&rows).Error; err != nil {
		logger.Warnf("batchFillTitles 查 documents 失败: %v", err)
		return
	}
	titleMap := make(map[string]string, len(rows))
	for _, r := range rows {
		titleMap[r.ID] = r.Title
	}
	for i := range chunks {
		if t, ok := titleMap[chunks[i].DocumentID]; ok {
			chunks[i].Title = t
		}
	}
}

// buildPostgresArray 构建 PostgreSQL 数组字面量
func buildPostgresArray(items []string) string {
	if len(items) == 0 {
		return "{}"
	}

	var sb strings.Builder
	sb.WriteString("{")
	for i, item := range items {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString("\"")
		sb.WriteString(strings.ReplaceAll(item, "\"", "\\\""))
		sb.WriteString("\"")
	}
	sb.WriteString("}")
	return sb.String()
}

// reciprocalRankFusion 实现 RRF 融合算法
func (r *HybridRetriever) reciprocalRankFusion(vectorResults, keywordResults []scoredChunk) []scoredChunk {
	docScores := make(map[string]*scoredChunk)

	// 处理向量检索结果
	for i, doc := range vectorResults {
		id := doc.ID
		if _, exists := docScores[id]; !exists {
			docScores[id] = &scoredChunk{
				ID:              doc.ID,
				KnowledgeBaseID: doc.KnowledgeBaseID,
				DocumentID:      doc.DocumentID,
				VersionID:       doc.VersionID,
				ChunkIndex:      doc.ChunkIndex,
				Title:           doc.Title,
				Content:         doc.Content,
				Keywords:        doc.Keywords,
			}
		}
		// RRF 公式: weight / (k + rank)
		docScores[id].Score += r.vectorWeight / (r.rrfK + float64(i+1))
	}

	// 处理关键词检索结果
	for i, doc := range keywordResults {
		id := doc.ID
		if _, exists := docScores[id]; !exists {
			docScores[id] = &scoredChunk{
				ID:              doc.ID,
				KnowledgeBaseID: doc.KnowledgeBaseID,
				DocumentID:      doc.DocumentID,
				VersionID:       doc.VersionID,
				ChunkIndex:      doc.ChunkIndex,
				Title:           doc.Title,
				Content:         doc.Content,
				Keywords:        doc.Keywords,
			}
		}
		// RRF 公式: weight / (k + rank)
		docScores[id].Score += r.keywordWeight / (r.rrfK + float64(i+1))
	}

	// 转换为切片并排序。
	//
	// ⚠️ 比较函数必须是**全序**：融合分相等时按 id 裁决。
	// 入参来自 map 迭代（顺序本身随机），所以先前只比 Score 的写法在等分时不可复现 ——
	// 实测两条 keyword-only 结果的融合分 0.004918 / 0.004839 并不相等，因而没触发；
	// 但只要等分就会咬人。
	// ⚠️ 判定为全序之后**不需要 SliceStable**：任何排序算法在全序下都给出同一个序列，
	// 稳定性保的是 map 那份随机顺序，语义为零（反而更慢、多一次分配）。
	var results []scoredChunk
	for _, doc := range docScores {
		results = append(results, *doc)
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		return results[i].ID < results[j].ID
	})

	return results
}

// ===== 六步管线辅助函数 =====

// keywordSourceFilter Step 1b: 关键词侧同源质量检查
// 关键词分数量纲是"匹配率"[0,1]，天然可比。
// 检测陡峭度：
//   - 太陡（top1-top5 > 80% 差距）→ 只有 Top1 可信（长尾噪声大），截断
//   - 平坦 → 全部保留（无法区分说明都差不差）
//   - 正常 → 全部保留
func (r *HybridRetriever) keywordSourceFilter(kwDocs []scoredChunk) []scoredChunk {
	if len(kwDocs) < 2 {
		return kwDocs
	}
	top1 := kwDocs[0].Score
	top5Idx := min(5, len(kwDocs)) - 1
	top5 := kwDocs[top5Idx].Score

	if top1 <= 0 {
		return kwDocs
	}

	steepness := (top1 - top5) / top1
	if steepness > 0.80 {
		logger.Infof("[关键词过滤] 分布太陡(steepness=%.2f)，仅保留 Top1", steepness)
		return kwDocs[:1]
	}
	return kwDocs
}

// minMaxNormalize Step 2: 同源内 Min-Max 归一化
// 将原始分数映射到 [0,1]，消除向量/关键词量纲差异。
// 单条时退化为 1.0；全相同分数时退化为 1.0（分数一致说明质量等同，应全部保留）。
// 返回 map[id]normalizedScore
func minMaxNormalize(docs []scoredChunk) map[string]float64 {
	result := make(map[string]float64, len(docs))
	if len(docs) == 0 {
		return result
	}
	if len(docs) == 1 {
		result[docs[0].ID] = 1.0
		return result
	}

	minScore, maxScore := docs[len(docs)-1].Score, docs[0].Score
	span := maxScore - minScore
	if span == 0 {
		// 所有分数相同 → 质量等同，全部给 1.0
		// （之前给 0 会导致 crossSourceFilter 误删全部单源结果）
		for _, doc := range docs {
			result[doc.ID] = 1.0
		}
		return result
	}

	for _, doc := range docs {
		result[doc.ID] = (doc.Score - minScore) / span
	}
	return result
}

// filterByMinScore 通用最低分过滤（带日志）
func filterByMinScore(docs []scoredChunk, threshold float64, label string) []scoredChunk {
	filtered := make([]scoredChunk, 0, len(docs))
	for _, doc := range docs {
		if doc.Score >= threshold {
			filtered = append(filtered, doc)
		}
	}
	return filtered
}

// crossSourceFilter Step 4: 跨源交叉验证
//
// RRF 融合后，每条结果按来源分类：
//   - 双路命中（向量+关键词都命中此文档）→ 高置信，直接采纳
//   - 仅向量侧命中 → 检查归一化分：< 0.05 → 丢弃（在该源内部排名太低）
//   - 仅关键词侧命中 → 检查归一化分：< 0.05 → 丢弃
//
// 参数：
//   - fused: RRF 融合后的全量结果
//   - fv, fk: 过滤后的向量/关键词结果（用于判断来源）
//   - normV, normK: 归一化分数 map
func (r *HybridRetriever) crossSourceFilter(
	fused []scoredChunk,
	fv, fk []scoredChunk,
	normV, normK map[string]float64,
) []scoredChunk {
	if len(fused) == 0 {
		return fused
	}

	// 构建来源查找集
	inVector := make(map[string]bool, len(fv))
	for _, doc := range fv {
		inVector[doc.ID] = true
	}
	inKeyword := make(map[string]bool, len(fk))
	for _, doc := range fk {
		inKeyword[doc.ID] = true
	}

	const crossSourceNormFloor = 0.05 // 归一化后太低→在该源内部排名垫底

	result := make([]scoredChunk, 0, len(fused))
	for _, doc := range fused {
		fromVector := inVector[doc.ID]
		fromKeyword := inKeyword[doc.ID]

		if fromVector && fromKeyword {
			// 双路命中 → 高置信
			result = append(result, doc)
			continue
		}

		if fromVector {
			// 仅向量侧命中
			normScore := normV[doc.ID]
			if normScore < crossSourceNormFloor {
				continue
			}
			result = append(result, doc)
			continue
		}

		if fromKeyword {
			// 仅关键词侧命中
			normScore := normK[doc.ID]
			if normScore < crossSourceNormFloor {
				continue
			}
			result = append(result, doc)
			continue
		}
	}

	return result
}

// chunkIDPreview 把命中结果拼成 "chunkId@docId#idx(s=score)" 的紧凑串，供检索摘要日志使用。
// id 取后 8 位（复用 shortHash）：单次日志内足以区分，且不会把日志撑成一行一屏。
func chunkIDPreview(docs []Document) string {
	if len(docs) == 0 {
		return ""
	}
	parts := make([]string, 0, len(docs))
	for _, d := range docs {
		parts = append(parts, fmt.Sprintf("%s@%s#%d(s=%.3f)", shortHash(d.ID), shortHash(d.DocumentID), d.ChunkIndex, d.Score))
	}
	return strings.Join(parts, ", ")
}
