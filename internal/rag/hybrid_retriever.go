package rag

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"gorm.io/gorm"

	"solvify-agent/pkg/config"
	"solvify-agent/pkg/logger"
)

// HybridRetriever 实现混合检索（向量 + 关键词 + RRF 融合）
type HybridRetriever struct {
	db             *gorm.DB
	embeddingFunc  EmbeddingFunc
	scoreThreshold float64
	vectorWeight   float64
	keywordWeight  float64
	rrfK           float64
}

// HybridRetrieverConfig 描述混合检索器配置
type HybridRetrieverConfig struct {
	DB             *gorm.DB
	EmbeddingFunc  EmbeddingFunc
	ScoreThreshold float64
	VectorWeight   float64
	KeywordWeight  float64
	RRFK           float64
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
	rrfK := cfg.RRFK
	if rrfK <= 0 {
		rrfK = 60
	}
	return &HybridRetriever{
		db:             cfg.DB,
		embeddingFunc:  cfg.EmbeddingFunc,
		scoreThreshold: threshold,
		vectorWeight:   vectorWeight,
		keywordWeight:  keywordWeight,
		rrfK:           rrfK,
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

	if vr.err != nil {
		return Result{}, fmt.Errorf("向量检索失败: %w", vr.err)
	}
	if kr.err != nil {
		return Result{}, fmt.Errorf("关键词检索失败: %w", kr.err)
	}

	logger.Infof("向量检索命中: %d 条, 关键词检索命中: %d 条", len(vr.docs), len(kr.docs))

	// RRF 融合
	fused := r.reciprocalRankFusion(vr.docs, kr.docs)

	// 过滤低分结果
	docs := make([]Document, 0, len(fused))
	for _, item := range fused {
		if item.Score >= r.scoreThreshold {
			docs = append(docs, Document{
				ID:              item.ID,
				KnowledgeBaseID: item.KnowledgeBaseID,
				DocumentID:      item.DocumentID,
				Title:           item.Title,
				Content:         item.Content,
				Score:           item.Score,
			})
			logger.Infof("  ✓ [%s] score=%.4f title=%q", item.DocumentID, item.Score, item.Title)
		} else {
			logger.Infof("  ✗ [%s] score=%.4f (低于阈值 %.2f) title=%q", item.DocumentID, item.Score, r.scoreThreshold, item.Title)
		}
	}

	// 截取 TopK
	if len(docs) > topK {
		docs = docs[:topK]
	}

	logger.Infof("混合检索最终结果: %d 条 (阈值=%.2f)", len(docs), r.scoreThreshold)

	return Result{
		Hit:       len(docs) > 0,
		Documents: docs,
	}, nil
}

// vectorSearch 执行向量检索
func (r *HybridRetriever) vectorSearch(ctx context.Context, query Query) ([]scoredChunk, error) {
	embedding, err := r.embeddingFunc(ctx, query.Question)
	if err != nil {
		return nil, fmt.Errorf("生成查询向量失败: %w", err)
	}

	vectorStr := vectorToString(embedding)
	topK := query.TopK * 2 // 多召回一些用于融合

	var results []scoredChunk
	err = r.db.WithContext(ctx).Raw(`
		SELECT id, knowledge_base_id, document_id, title, content, score, keywords
		FROM (
			SELECT
				dc.id,
				dc.knowledge_base_id,
				dc.document_id,
				COALESCE(d.title, '') as title,
				dc.content,
				1 - (dc.embedding <=> ?::vector) AS score,
				COALESCE(dc.keywords::text, '{}') as keywords
			FROM document_chunks dc
			LEFT JOIN documents d ON d.id = dc.document_id
			WHERE dc.knowledge_base_id IN (?)
				AND dc.embedding IS NOT NULL
				AND dc.user_id = ?
			ORDER BY dc.embedding <=> ?::vector
			LIMIT ?
		) sub
	`, vectorStr, query.KnowledgeBaseIDs, query.UserID, vectorStr, topK).Scan(&results).Error

	if err != nil {
		return nil, err
	}

	logger.Infof("向量检索原始结果: %d 条", len(results))
	return results, nil
}

// keywordSearch 执行关键词检索
func (r *HybridRetriever) keywordSearch(ctx context.Context, query Query) ([]scoredChunk, error) {
	// 从问题中提取关键词（简单的分词策略）
	keywords := extractKeywords(query.Question)
	if len(keywords) == 0 {
		return nil, nil
	}

	topK := query.TopK * 2 // 多召回一些用于融合

	// 使用 PostgreSQL 数组操作符进行关键词匹配
	// 使用 && 操作符检查数组是否有交集
	var results []scoredChunk

	// 构建关键词数组字面量
	keywordArray := buildPostgresArray(keywords)

	err := r.db.WithContext(ctx).Raw(`
		SELECT id, knowledge_base_id, document_id, title, content, score, keywords
		FROM (
			SELECT
				dc.id,
				dc.knowledge_base_id,
				dc.document_id,
				COALESCE(d.title, '') as title,
				dc.content,
				-- 计算关键词匹配分数：匹配的关键词越多，分数越高
				(
					SELECT COUNT(*)::float / GREATEST(array_length(?::text[], 1), 1)
					FROM unnest(dc.keywords) AS kw
					WHERE kw = ANY(?::text[])
				) AS score,
				COALESCE(dc.keywords::text, '{}') as keywords
			FROM document_chunks dc
			LEFT JOIN documents d ON d.id = dc.document_id
			WHERE dc.knowledge_base_id IN (?)
				AND dc.keywords IS NOT NULL
				AND dc.keywords && ?::text[]
				AND dc.user_id = ?
			ORDER BY score DESC
			LIMIT ?
		) sub
		WHERE score > 0
	`, keywordArray, keywordArray, query.KnowledgeBaseIDs, keywordArray, query.UserID, topK).Scan(&results).Error

	if err != nil {
		return nil, err
	}

	logger.Infof("关键词检索原始结果: %d 条, 关键词: %v", len(results), keywords)
	return results, nil
}

// extractKeywords 从问题中提取关键词
func extractKeywords(question string) []string {
	// 简单的关键词提取策略：
	// 1. 转小写
	// 2. 按空格和标点分词
	// 3. 过滤停用词和短词
	stopWords := map[string]bool{
		"的": true, "了": true, "在": true, "是": true, "我": true,
		"有": true, "和": true, "就": true, "不": true, "人": true,
		"都": true, "一": true, "一个": true, "上": true, "也": true,
		"很": true, "到": true, "说": true, "要": true, "去": true,
		"你": true, "会": true, "着": true, "没有": true, "看": true,
		"好": true, "自己": true, "这": true, "他": true, "她": true,
		"它": true, "们": true, "那": true, "什么": true,
		"怎么": true, "如何": true, "为什么": true, "哪": true, "哪个": true,
		"哪些": true, "吗": true, "呢": true, "吧": true, "啊": true,
		"the": true, "a": true, "an": true, "is": true, "are": true,
		"was": true, "were": true, "be": true, "been": true, "being": true,
		"have": true, "has": true, "had": true, "do": true, "does": true,
		"did": true, "will": true, "would": true, "could": true, "should": true,
		"may": true, "might": true, "can": true, "shall": true, "must": true,
		"to": true, "of": true, "in": true, "for": true, "on": true,
		"with": true, "at": true, "by": true, "from": true, "as": true,
		"into": true, "through": true, "during": true, "before": true, "after": true,
		"above": true, "below": true, "between": true, "and": true, "but": true,
		"or": true, "nor": true, "not": true, "so": true, "yet": true,
		"both": true, "either": true, "neither": true, "each": true, "every": true,
		"all": true, "any": true, "few": true, "more": true, "most": true,
		"other": true, "some": true, "such": true, "no": true, "only": true,
		"own": true, "same": true, "than": true, "too": true, "very": true,
		"just": true, "because": true, "if": true, "when": true, "where": true,
		"how": true, "what": true, "which": true, "who": true, "whom": true,
		"this": true, "that": true, "these": true, "those": true, "i": true,
		"me": true, "my": true, "we": true, "our": true, "you": true,
		"your": true, "he": true, "him": true, "his": true, "she": true,
		"her": true, "it": true, "its": true, "they": true, "them": true,
		"their": true,
	}

	// 分词
	words := strings.FieldsFunc(strings.ToLower(question), func(r rune) bool {
		return r == ' ' || r == ',' || r == '.' || r == '?' || r == '!' ||
			r == ';' || r == ':' || r == '"' || r == '\'' || r == '(' ||
			r == ')' || r == '[' || r == ']' || r == '{' || r == '}' ||
			r == '/' || r == '\\' || r == '|' || r == '@' || r == '#' ||
			r == '$' || r == '%' || r == '^' || r == '&' || r == '*' ||
			r == '-' || r == '_' || r == '+' || r == '=' || r == '~' ||
			r == '`' || r == '<' || r == '>'
	})

	var keywords []string
	for _, word := range words {
		// 过滤停用词和短词
		if len(word) >= 2 && !stopWords[word] {
			keywords = append(keywords, word)
		}
	}

	return keywords
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
