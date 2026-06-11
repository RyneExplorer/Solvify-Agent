package rag

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"solvify-agent/pkg/logger"
)

// EmbeddingFunc 定义文本向量化函数签名
type EmbeddingFunc func(ctx context.Context, text string) ([]float64, error)

// VectorRetriever 实现基于 pgvector 的向量检索
type VectorRetriever struct {
	db             *gorm.DB
	embeddingFunc  EmbeddingFunc
	scoreThreshold float64
}

// VectorRetrieverConfig 描述向量检索器配置
type VectorRetrieverConfig struct {
	DB             *gorm.DB
	EmbeddingFunc  EmbeddingFunc
	ScoreThreshold float64
}

// NewVectorRetriever 创建向量检索器
func NewVectorRetriever(cfg VectorRetrieverConfig) *VectorRetriever {
	threshold := cfg.ScoreThreshold
	if threshold <= 0 {
		threshold = 0.5
	}
	return &VectorRetriever{
		db:             cfg.DB,
		embeddingFunc:  cfg.EmbeddingFunc,
		scoreThreshold: threshold,
	}
}

// chunkResult 描述数据库查询结果
type chunkResult struct {
	ID              string  `gorm:"column:id"`
	KnowledgeBaseID string  `gorm:"column:knowledge_base_id"`
	DocumentID      string  `gorm:"column:document_id"`
	Title           string  `gorm:"column:title"`
	Content         string  `gorm:"column:content"`
	Score           float64 `gorm:"column:score"`
}

// Retrieve 执行向量检索
func (r *VectorRetriever) Retrieve(ctx context.Context, query Query) (Result, error) {
	if len(query.KnowledgeBaseIDs) == 0 {
		return Result{Hit: false, Documents: nil}, nil
	}

	topK := query.TopK
	if topK <= 0 {
		topK = 5
	}

	// 生成查询向量
	logger.Infof("向量检索开始: query=%q, topK=%d, knowledgeBaseIDs=%v", query.Question, topK, query.KnowledgeBaseIDs)
	embedding, err := r.embeddingFunc(ctx, query.Question)
	if err != nil {
		return Result{}, fmt.Errorf("生成查询向量失败: %w", err)
	}

	// 将向量转换为 PostgresSQL 数组格式
	vectorStr := vectorToString(embedding)

	// 执行向量相似度搜索（子查询避免重复计算向量距离）
	var results []chunkResult
	err = r.db.WithContext(ctx).Raw(`
		SELECT id, knowledge_base_id, document_id, title, content, score
		FROM (
			SELECT
				dc.id,
				dc.knowledge_base_id,
				dc.document_id,
				COALESCE(d.title, '') as title,
				dc.content,
				1 - (dc.embedding <=> ?::vector) AS score
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
		return Result{}, fmt.Errorf("向量检索失败: %w", err)
	}
	logger.Infof("向量检索命中原始结果: %d 条", len(results))

	// 过滤低分结果并转换
	docs := make([]Document, 0, len(results))
	for _, item := range results {
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
	logger.Infof("向量检索最终结果: %d 条 (阈值=%.2f)", len(docs), r.scoreThreshold)

	return Result{
		Hit:       len(docs) > 0,
		Documents: docs,
	}, nil
}

// vectorToString 将浮点数切片转换为 PostgresSQL vector 字符串格式
func vectorToString(vec []float64) string {
	if len(vec) == 0 {
		return "[]"
	}
	// 预分配 buffer：每个数最多 20 字符 + 逗号 + 括号
	var sb strings.Builder
	sb.Grow(len(vec)*21 + 2)
	sb.WriteString("[")
	for i, v := range vec {
		if i > 0 {
			sb.WriteByte(',')
		}
		sb.WriteString(strconv.FormatFloat(v, 'f', 6, 64))
	}
	sb.WriteString("]")
	return sb.String()
}
