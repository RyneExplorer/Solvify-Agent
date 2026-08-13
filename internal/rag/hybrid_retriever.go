package rag

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/go-ego/gse"
	"gorm.io/gorm"

	"solvify-agent/internal/observability"
	"solvify-agent/pkg/config"
	"solvify-agent/pkg/logger"
	"solvify-agent/pkg/stopwords"
)

var (
	segOnce sync.Once
	segInst gse.Segmenter
)

// getSegmenter 获取全局 gse 分词实例（懒加载，使用内嵌词典）
func getSegmenter() *gse.Segmenter {
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

// HybridRetriever 实现混合检索（向量 + 关键词 + RRF 融合）
type HybridRetriever struct {
	db                    *gorm.DB
	embeddingFunc         EmbeddingFunc
	scoreThreshold        float64
	vectorWeight          float64
	keywordWeight         float64
	keywordScoreThreshold float64 // 向量全灭时，关键词结果的最低匹配比例
	rrfK                  float64
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
	return &HybridRetriever{
		db:                    cfg.DB,
		embeddingFunc:         cfg.EmbeddingFunc,
		scoreThreshold:        threshold,
		vectorWeight:          vectorWeight,
		keywordWeight:         keywordWeight,
		keywordScoreThreshold: keywordScoreThreshold,
		rrfK:                  rrfK,
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
		RRFK:           cfg.RRFK,
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

	topK := query.TopK
	if topK <= 0 {
		topK = 5
	}

	logger.Infof("混合检索开始: query=%q, topK=%d, knowledgeBaseIDs=%v", query.Question, topK, query.KnowledgeBaseIDs)

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

	rec := observability.RecorderFromContext(ctx)

	// 向量检索失败时降级：仅用关键词结果，不阻断检索
	if vr.err != nil {
		logger.Warnf("向量检索失败，降级为纯关键词检索: %v", vr.err)
		vr.docs = nil // 清空，后续只用关键词结果
		rec.Incr(ctx, "rag_retriever_degradation_total", map[string]string{"side": "vector", "reason": "search_error"}, 1)
	}
	if kr.err != nil {
		logger.Warnf("关键词检索失败，降级为纯向量检索: %v", kr.err)
		kr.docs = nil
		rec.Incr(ctx, "rag_retriever_degradation_total", map[string]string{"side": "keyword", "reason": "search_error"}, 1)
	}

	// 两种检索都失败才报错
	if vr.err != nil && kr.err != nil {
		rec.Incr(ctx, "rag_retriever_degradation_total", map[string]string{"side": "both", "reason": "search_error"}, 1)
		return Result{}, fmt.Errorf("混合检索完全失败: 向量(%v), 关键词(%v)", vr.err, kr.err)
	}

	observeStage(rec, ctx, "vector_raw", float64(len(vr.docs)))
	observeStage(rec, ctx, "keyword_raw", float64(len(kr.docs)))

	logger.Infof("向量检索命中: %d 条, 关键词检索命中: %d 条", len(vr.docs), len(kr.docs))

	// ===== Step 1: 同源质量检查（各自独立过滤） =====

	// 1a. 向量侧：绝对阈值过滤（余弦相似度 >= scoreThreshold）
	filteredVector := make([]scoredChunk, 0, len(vr.docs))
	for _, doc := range vr.docs {
		if doc.Score >= r.scoreThreshold {
			filteredVector = append(filteredVector, doc)
		}
	}
	observeStage(rec, ctx, "vector_filtered", float64(len(filteredVector)))

	// 1b. 关键词侧：陡峭度检测 + 最低匹配过滤
	filteredKeyword := r.keywordSourceFilter(kr.docs)

	// 1c. 向量全灭时，对关键词结果加最低匹配比例过滤
	if len(filteredVector) == 0 && len(filteredKeyword) > 0 {
		filteredKeyword = filterByMinScore(filteredKeyword, r.keywordScoreThreshold, "关键词")
		rec.Incr(ctx, "rag_retriever_degradation_total", map[string]string{"side": "keyword_only", "reason": "min_score_filter"}, 1)
	}
	observeStage(rec, ctx, "keyword_filtered", float64(len(filteredKeyword)))

	// ===== Step 2: 同源内 Min-Max 归一化 =====
	vectorNorm := minMaxNormalize(filteredVector)
	keywordNorm := minMaxNormalize(filteredKeyword)

	// ===== Step 3: RRF 融合 =====
	fused := r.reciprocalRankFusion(filteredVector, filteredKeyword)
	observeStage(rec, ctx, "rrf_fused", float64(len(fused)))

	// ===== Step 4: 跨源交叉验证 =====
	fused = r.crossSourceFilter(fused, filteredVector, filteredKeyword, vectorNorm, keywordNorm)
	observeStage(rec, ctx, "cross_filtered", float64(len(fused)))

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
	observeStage(rec, ctx, "final", float64(len(docs)))

	logger.Infof("混合检索最终结果: %d 条 (向量过滤阈值=%.2f, TopK=%d, 向量候选=%d, 关键词候选=%d)",
		len(docs), r.scoreThreshold, topK, len(filteredVector), len(filteredKeyword))

	return Result{
		Hit:       len(docs) > 0,
		Documents: docs,
	}, nil
}

// vectorSearch 执行向量检索
// 优化：主查询只查 document_chunks 表（不 LEFT JOIN documents），
// 向量距离排序在 chunks 表上直接跑，拿 topK*2 后再批量查 documents 表的 title。
// 避免对所有候选 chunk 做额外 JOIN。
func (r *HybridRetriever) vectorSearch(ctx context.Context, query Query) ([]scoredChunk, error) {
	embedding, err := r.embeddingFunc(ctx, query.Question)
	if err != nil {
		return nil, fmt.Errorf("生成查询向量失败: %w", err)
	}

	vectorStr := vectorToString(embedding)
	topK := query.TopK * 2

	var results []scoredChunk
	err = r.db.WithContext(ctx).Raw(`
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
			AND dc.user_id = ?
		ORDER BY dc.embedding <=> ?::vector
		LIMIT ?
	`, vectorStr, query.KnowledgeBaseIDs, query.UserID, vectorStr, topK).Scan(&results).Error

	if err != nil {
		return nil, err
	}

	// 批量填 title（只对 topK*2 条查，开销可忽略）
	batchFillTitles(r.db, results)

	logger.Infof("向量检索原始结果: %d 条", len(results))
	return results, nil
}

// keywordSearch 执行关键词检索
// 优化：GIN 索引加速 && overlap 过滤（主收益），unnest 仅对过滤后的少量行计算分数
// 也去掉了 LEFT JOIN documents，title 在主查询完成后批量填
func (r *HybridRetriever) keywordSearch(ctx context.Context, query Query) ([]scoredChunk, error) {
	keywords := extractKeywords(query.Question)
	if len(keywords) == 0 {
		return nil, nil
	}

	topK := query.TopK * 2

	var results []scoredChunk

	keywordArray := buildPostgresArray(keywords)

	err := r.db.WithContext(ctx).Raw(`
		SELECT
			dc.id,
			dc.knowledge_base_id,
			dc.document_id,
			dc.version_id,
			dc.chunk_index,
			dc.content,
			(
				SELECT COUNT(*)::float / GREATEST(cardinality(?::text[]), 1)
				FROM unnest(dc.keywords) AS kw
				WHERE kw = ANY(?::text[])
			) AS score,
			COALESCE(dc.keywords::text, '{}') as keywords
		FROM document_chunks dc
		WHERE dc.knowledge_base_id IN (?)
			AND dc.keywords IS NOT NULL
			AND dc.keywords && ?::text[]
			AND dc.user_id = ?
			AND dc.embedding IS NOT NULL
		ORDER BY score DESC
		LIMIT ?
	`, keywordArray, keywordArray, query.KnowledgeBaseIDs, keywordArray, query.UserID, topK).Scan(&results).Error

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

// extractKeywords 使用 gse 分词提取关键词，过滤停用词
func extractKeywords(question string) []string {
	seg := getSegmenter()
	words := seg.Cut(question, true)

	var keywords []string
	seen := make(map[string]bool)
	for _, w := range words {
		w = strings.ToLower(strings.TrimSpace(w))
		if w == "" || len(w) < 2 {
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

	// 转换为切片并排序
	var results []scoredChunk
	for _, doc := range docScores {
		results = append(results, *doc)
	}
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
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

// observeStage 记录混合检索各阶段的候选条数，供 Prometheus 观测管线漏斗。
// rec 为 nil 时静默跳过（单元测试或未注册 observability 的场景）。
func observeStage(rec observability.Recorder, ctx context.Context, stage string, count float64) {
	if rec == nil {
		return
	}
	rec.Observe(ctx, "rag_retriever_stage_count", map[string]string{"stage": stage}, count)
}
