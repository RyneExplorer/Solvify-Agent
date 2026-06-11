package rag

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"solvify-agent/pkg/config"
	"solvify-agent/pkg/logger"
)

// ExpandRetriever 装饰器：检索后扩展相邻分块并去重
type ExpandRetriever struct {
	inner           Retriever
	db              *gorm.DB
	windowSize      int
	maxChunkTokens  int
	dedupeThreshold float64
}

// ExpandRetrieverConfig 描述扩展检索器配置
type ExpandRetrieverConfig struct {
	Inner           Retriever
	DB              *gorm.DB
	WindowSize      int
	MaxChunkTokens  int
	DeduceThreshold float64
}

// NewExpandRetriever 创建扩展检索器
func NewExpandRetriever(cfg ExpandRetrieverConfig) *ExpandRetriever {
	windowSize := cfg.WindowSize
	if windowSize <= 0 {
		windowSize = 1
	}
	maxChunkTokens := cfg.MaxChunkTokens
	if maxChunkTokens <= 0 {
		maxChunkTokens = 1000
	}
	dedupeThreshold := cfg.DeduceThreshold
	if dedupeThreshold <= 0 {
		dedupeThreshold = 0.8
	}
	return &ExpandRetriever{
		inner:           cfg.Inner,
		db:              cfg.DB,
		windowSize:      windowSize,
		maxChunkTokens:  maxChunkTokens,
		dedupeThreshold: dedupeThreshold,
	}
}

// NewExpandRetrieverFromConfig 从全局配置创建扩展检索器
func NewExpandRetrieverFromConfig(inner Retriever, db *gorm.DB) *ExpandRetriever {
	cfg := config.Get().RAG.Expander
	return NewExpandRetriever(ExpandRetrieverConfig{
		Inner:           inner,
		DB:              db,
		WindowSize:      cfg.WindowSize,
		MaxChunkTokens:  cfg.MaxChunkTokens,
		DeduceThreshold: cfg.DedupThreshold,
	})
}

// adjacentChunk 描述相邻分块查询结果
type adjacentChunk struct {
	ID         string `gorm:"column:id"`
	ChunkIndex int    `gorm:"column:chunk_index"`
	Content    string `gorm:"column:content"`
}

// Retrieve 执行检索后扩展相邻分块
func (r *ExpandRetriever) Retrieve(ctx context.Context, query Query) (Result, error) {
	logger.Infof("[Expand] 开始相邻分块扩展, window=%d, maxChunkTokens=%d, dedupThreshold=%.2f",
		r.windowSize, r.maxChunkTokens, r.dedupeThreshold)

	// 先调用内层检索器
	result, err := r.inner.Retrieve(ctx, query)
	if err != nil {
		return Result{}, err
	}

	// 无结果则直接返回
	if !result.Hit || len(result.Documents) == 0 {
		logger.Infof("[Expand] 内层检索无结果，跳过扩展")
		return result, nil
	}

	logger.Infof("[Expand] 内层检索返回 %d 条", len(result.Documents))
	for i, doc := range result.Documents {
		logger.Infof("[Expand]   输入#%d: [%s] score=%.4f chunk#%d title=%q content=%q",
			i, doc.DocumentID, doc.Score, doc.ChunkIndex, doc.Title, truncate(doc.Content, 60))
	}

	// 按 document_id 分组
	docGroups := make(map[string][]Document)
	for _, doc := range result.Documents {
		docGroups[doc.DocumentID] = append(docGroups[doc.DocumentID], doc)
	}
	logger.Infof("[Expand] 涉及 %d 篇文档", len(docGroups))

	// 扩展相邻分块
	expanded := make([]Document, 0, len(result.Documents))
	for docID, docs := range docGroups {
		logger.Infof("[Expand] 扩展文档 %s (%d 条命中 chunk)", docID, len(docs))
		expandedDocs, err := r.expandDocumentChunks(ctx, docs[0].KnowledgeBaseID, docID, docs)
		if err != nil {
			logger.Warnf("[Expand] 扩展文档 %s 的相邻分块失败: %v", docID, err)
			// 降级：保留原始结果
			expanded = append(expanded, docs...)
			continue
		}
		logger.Infof("[Expand]   文档 %s: %d 条 → %d 条", docID, len(docs), len(expandedDocs))
		for j, doc := range expandedDocs {
			logger.Infof("[Expand]     扩展#%d: chunk#%d content=%q", j, doc.ChunkIndex, truncate(doc.Content, 80))
		}
		expanded = append(expanded, expandedDocs...)
	}

	// Jaccard 去重
	deduped := r.deduplicate(expanded)
	if len(deduped) < len(expanded) {
		logger.Infof("[Expand] Jaccard 去重: %d → %d (去除 %d 条重复)", len(expanded), len(deduped), len(expanded)-len(deduped))
	}

	logger.Infof("[Expand] 扩展完成: 原始 %d 条 → 扩展后 %d 条 → 去重后 %d 条",
		len(result.Documents), len(expanded), len(deduped))
	for i, doc := range deduped {
		logger.Infof("[Expand]   最终#%d: [%s] score=%.4f chunk#%d title=%q content=%q",
			i, doc.DocumentID, doc.Score, doc.ChunkIndex, doc.Title, truncate(doc.Content, 80))
	}

	return Result{
		Hit:       len(deduped) > 0,
		Documents: deduped,
	}, nil
}

// expandDocumentChunks 扩展单个文档的相邻分块
func (r *ExpandRetriever) expandDocumentChunks(ctx context.Context, knowledgeBaseID, documentID string, docs []Document) ([]Document, error) {
	// 收集命中的 chunk_index 集合
	hitIndices := make(map[int]bool)
	var minIndex, maxIndex int
	firstHit := true
	for _, doc := range docs {
		hitIndices[doc.ChunkIndex] = true
		if firstHit || doc.ChunkIndex < minIndex {
			minIndex = doc.ChunkIndex
		}
		if firstHit || doc.ChunkIndex > maxIndex {
			maxIndex = doc.ChunkIndex
		}
		firstHit = false
	}

	// 计算扩展范围
	expandMin := minIndex - r.windowSize
	if expandMin < 0 {
		expandMin = 0
	}
	expandMax := maxIndex + r.windowSize
	logger.Infof("[Expand]   查询范围: chunk_index [%d, %d] (命中 [%d, %d], window=%d)",
		expandMin, expandMax, minIndex, maxIndex, r.windowSize)

	// 查询相邻分块
	var adjacentChunks []adjacentChunk
	err := r.db.WithContext(ctx).Raw(`
		SELECT id, chunk_index, content
		FROM document_chunks
		WHERE knowledge_base_id = ?
			AND document_id = ?
			AND chunk_index >= ?
			AND chunk_index <= ?
		ORDER BY chunk_index
	`, knowledgeBaseID, documentID, expandMin, expandMax).Scan(&adjacentChunks).Error
	if err != nil {
		return nil, err
	}
	logger.Infof("[Expand]   查询到 %d 个相邻分块", len(adjacentChunks))
	for _, c := range adjacentChunks {
		hitMark := " "
		if hitIndices[c.ChunkIndex] {
			hitMark = "*"
		}
		logger.Infof("[Expand]     %s chunk#%d: %q", hitMark, c.ChunkIndex, truncate(c.Content, 60))
	}

	// 按连续区间分组并合并
	chunkMap := make(map[int]adjacentChunk)
	for _, c := range adjacentChunks {
		chunkMap[c.ChunkIndex] = c
	}

	// 找出连续区间
	merged := r.mergeContiguousChunks(chunkMap, hitIndices, expandMin, expandMax)

	// 保留原始分数：命中的 chunk 用原始分数，扩展的 chunk 用所属文档的最高分
	var maxScore float64
	for _, doc := range docs {
		if doc.Score > maxScore {
			maxScore = doc.Score
		}
	}

	result := make([]Document, 0, len(merged))
	for _, m := range merged {
		score := maxScore
		// 如果区间内全是扩展的（无命中），降低分数
		hasHit := false
		for idx := m.startIndex; idx <= m.endIndex; idx++ {
			if hitIndices[idx] {
				hasHit = true
				break
			}
		}
		if !hasHit {
			score = maxScore * 0.5
		}

		result = append(result, Document{
			ID:              docs[0].ID,
			KnowledgeBaseID: knowledgeBaseID,
			DocumentID:      documentID,
			VersionID:       docs[0].VersionID,
			ChunkIndex:      m.startIndex,
			Title:           docs[0].Title,
			Content:         m.content,
			Score:           score,
		})
	}

	return result, nil
}

// mergedChunk 描述合并后的分块
type mergedChunk struct {
	startIndex int
	endIndex   int
	content    string
}

// mergeContiguousChunks 将连续的 chunk 合并为更大的内容块
func (r *ExpandRetriever) mergeContiguousChunks(chunks map[int]adjacentChunk, hitIndices map[int]bool, minIdx, maxIdx int) []mergedChunk {
	var result []mergedChunk
	var currentContent strings.Builder
	var currentStart, currentEnd int
	inSegment := false

	for i := minIdx; i <= maxIdx; i++ {
		chunk, exists := chunks[i]
		if !exists {
			// 断裂：结束当前区间
			if inSegment {
				result = append(result, mergedChunk{
					startIndex: currentStart,
					endIndex:   currentEnd,
					content:    currentContent.String(),
				})
				currentContent.Reset()
				inSegment = false
			}
			continue
		}

		// 检查 token 限制
		newLen := currentContent.Len() + len(chunk.Content) + 1 // +1 for newline
		if inSegment && newLen > r.maxChunkTokens*4 {
			// 超过限制：结束当前区间，开始新区间
			result = append(result, mergedChunk{
				startIndex: currentStart,
				endIndex:   currentEnd,
				content:    currentContent.String(),
			})
			currentContent.Reset()
			inSegment = false
		}

		if !inSegment {
			currentStart = i
			inSegment = true
		}
		currentEnd = i
		if currentContent.Len() > 0 {
			currentContent.WriteString("\n")
		}
		currentContent.WriteString(chunk.Content)
	}

	// 最后一个区间
	if inSegment {
		result = append(result, mergedChunk{
			startIndex: currentStart,
			endIndex:   currentEnd,
			content:    currentContent.String(),
		})
	}

	return result
}

// deduplicate 使用 Jaccard 相似度去重
func (r *ExpandRetriever) deduplicate(docs []Document) []Document {
	if len(docs) <= 1 {
		return docs
	}

	result := make([]Document, 0, len(docs))
	contentSets := make([]map[string]struct{}, 0, len(docs))

	for _, doc := range docs {
		words := tokenize(doc.Content)
		wordSet := make(map[string]struct{}, len(words))
		for _, w := range words {
			wordSet[w] = struct{}{}
		}

		// 检查是否与已有结果重复
		isDuplicate := false
		for _, existingSet := range contentSets {
			if jaccardSimilarity(wordSet, existingSet) >= r.dedupeThreshold {
				isDuplicate = true
				break
			}
		}

		if !isDuplicate {
			result = append(result, doc)
			contentSets = append(contentSets, wordSet)
		}
	}

	return result
}

// tokenize 简单分词
func tokenize(text string) []string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return r == ' ' || r == ',' || r == '.' || r == '?' || r == '!' || r == ';' || r == ':' || r == '\'' || r == '(' || r == ')' || r == '[' || r == ']' || r == '{' || r == '}' || r == '/' || r == '\\' || r == '|' || r == '\n' || r == '\t' || r == '\r' || r == '，' || r == '。' || r == '？' || r == '！' || r == '；' || r == '：' || r == '"' || r == '（' || r == '）' || r == '【' || r == '】' || r == '《' || r == '》'
	})
	return words
}

// jacquardSimilarity 计算两个集合的 Jacquard 相似度
func jaccardSimilarity(a, b map[string]struct{}) float64 {
	if len(a) == 0 && len(b) == 0 {
		return 1.0
	}

	intersection := 0
	for word := range a {
		if _, exists := b[word]; exists {
			intersection++
		}
	}

	union := len(a) + len(b) - intersection
	if union == 0 {
		return 0.0
	}

	return float64(intersection) / float64(union)
}
