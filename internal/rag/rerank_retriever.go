package rag

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"time"

	"solvify-agent/pkg/config"
	"solvify-agent/pkg/logger"
)

// RerankRetriever 装饰器：在内层 Retriever 检索后调用外部 Rerank API 重排序
type RerankRetriever struct {
	inner          Retriever
	endpoint       string
	model          string
	apiKey         string
	topN           int
	timeout        time.Duration
	scoreThreshold float64
	httpClient     *http.Client
}

// RerankRetrieverConfig 描述重排序检索器配置
type RerankRetrieverConfig struct {
	Inner          Retriever
	Endpoint       string
	Model          string
	APIKey         string
	TopN           int
	Timeout        int
	ScoreThreshold float64
}

// NewRerankRetriever 创建重排序检索器
func NewRerankRetriever(cfg RerankRetrieverConfig) *RerankRetriever {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5
	}
	topN := cfg.TopN
	if topN <= 0 {
		topN = 3
	}
	threshold := cfg.ScoreThreshold
	if threshold <= 0 {
		threshold = 0.5
	}
	return &RerankRetriever{
		inner:          cfg.Inner,
		endpoint:       cfg.Endpoint,
		model:          cfg.Model,
		apiKey:         cfg.APIKey,
		topN:           topN,
		timeout:        time.Duration(timeout) * time.Second,
		scoreThreshold: threshold,
		httpClient: &http.Client{
			Timeout: time.Duration(timeout) * time.Second,
		},
	}
}

// NewRerankRetrieverFromConfig 从全局配置创建重排序检索器
func NewRerankRetrieverFromConfig(inner Retriever) *RerankRetriever {
	cfg := config.Get().RAG.Reranker
	return NewRerankRetriever(RerankRetrieverConfig{
		Inner:          inner,
		Endpoint:       cfg.Endpoint,
		Model:          cfg.Model,
		APIKey:         cfg.APIKey,
		TopN:           cfg.TopN,
		Timeout:        cfg.Timeout,
		ScoreThreshold: cfg.ScoreThreshold,
	})
}

// rerankRequest 描述 Rerank API 请求体
type rerankRequest struct {
	Query       string   `json:"query"`
	Documents   []string `json:"documents"`
	Model       string   `json:"model,omitempty"`
	TopN        int      `json:"top_n,omitempty"`
	Instruction string   `json:"instruction,omitempty"`
}

// rerankResult 描述单条 Rerank 结果
type rerankResult struct {
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

// rerankResponse 描述 Rerank API 响应体
type rerankResponse struct {
	Results []rerankResult `json:"results"`
}

// Retrieve 执行检索后重排序
func (r *RerankRetriever) Retrieve(ctx context.Context, query Query) (Result, error) {
	logger.Infof("[Rerank] 开始重排序, query=%q, endpoint=%s, model=%s", query.Question, r.endpoint, r.model)

	// 先调用内层检索器
	result, err := r.inner.Retrieve(ctx, query)
	if err != nil {
		return Result{}, err
	}

	// 无结果则直接返回
	if !result.Hit || len(result.Documents) == 0 {
		logger.Infof("[Rerank] 内层检索无结果，跳过重排序")
		return result, nil
	}

	logger.Infof("[Rerank] 内层检索返回 %d 条，开始调用 Rerank API", len(result.Documents))
	for i, doc := range result.Documents {
		logger.Infof("[Rerank]   输入#%d: [%s] score=%.4f chunk#%d title=%q content=%q",
			i, doc.DocumentID, doc.Score, doc.ChunkIndex, doc.Title, truncate(doc.Content, 60))
	}

	// 调用 Rerank API
	reranked, err := r.rerank(ctx, query.Question, result.Documents)
	if err != nil {
		// 优雅降级：Rerank 失败时返回原始结果
		logger.Warnf("[Rerank] Rerank API 调用失败，降级返回原始结果: %v", err)
		return result, nil
	}

	logger.Infof("[Rerank] API 返回 %d 条结果", len(reranked))
	for _, item := range reranked {
		logger.Infof("[Rerank]   API结果#%d: relevance_score=%.4f", item.Index, item.RelevanceScore)
	}

	// 用重排序分数替换原始分数
	for _, item := range reranked {
		if item.Index >= 0 && item.Index < len(result.Documents) {
			oldScore := result.Documents[item.Index].Score
			result.Documents[item.Index].Score = item.RelevanceScore
			logger.Infof("[Rerank]   文档#%d [%s] 分数: %.4f → %.4f",
				item.Index, result.Documents[item.Index].DocumentID, oldScore, item.RelevanceScore)
		}
	}

	// 按新分数降序排序
	sort.Slice(result.Documents, func(i, j int) bool {
		return result.Documents[i].Score > result.Documents[j].Score
	})

	// 过滤低分结果
	filtered := make([]Document, 0, len(result.Documents))
	for _, doc := range result.Documents {
		if doc.Score >= r.scoreThreshold {
			filtered = append(filtered, doc)
		} else {
			logger.Infof("[Rerank]   过滤: [%s] score=%.4f < 阈值 %.2f", doc.DocumentID, doc.Score, r.scoreThreshold)
		}
	}

	// 截取 TopN
	if len(filtered) > r.topN {
		logger.Infof("[Rerank] 截取 TopN: %d → %d", len(filtered), r.topN)
		filtered = filtered[:r.topN]
	}

	logger.Infof("[Rerank] 重排序完成: 原始 %d 条 → 过滤后 %d 条", len(result.Documents), len(filtered))
	for i, doc := range filtered {
		logger.Infof("[Rerank]   最终#%d: [%s] score=%.4f chunk#%d title=%q content=%q",
			i, doc.DocumentID, doc.Score, doc.ChunkIndex, doc.Title, truncate(doc.Content, 60))
	}

	return Result{
		Hit:       len(filtered) > 0,
		Documents: filtered,
	}, nil
}

// rerank 调用外部 Rerank API
func (r *RerankRetriever) rerank(ctx context.Context, query string, docs []Document) ([]rerankResult, error) {
	documents := make([]string, 0, len(docs))
	for _, doc := range docs {
		documents = append(documents, doc.Content)
	}

	logger.Infof("[Rerank] 发送请求: endpoint=%s, query=%q, documents=%d, top_n=%d, model=%s",
		r.endpoint, query, len(documents), r.topN, r.model)

	reqBody := rerankRequest{
		Query:     query,
		Documents: documents,
		Model:     r.model,
		TopN:      r.topN,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if r.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+r.apiKey)
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("调用 Rerank API 失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rerank API 返回 %d: %s", resp.StatusCode, string(respBody))
	}

	var rerankResp rerankResponse
	if err := json.Unmarshal(respBody, &rerankResp); err != nil {
		return nil, fmt.Errorf("解析响应失败: %w", err)
	}

	return rerankResp.Results, nil
}
