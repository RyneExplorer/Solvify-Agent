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
	maxRetries     int
	baseBackoff    time.Duration
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

const (
	defaultRerankTimeout    = 5
	defaultRerankTopN       = 3
	defaultRerankThreshold  = 0.5
	defaultRerankMaxRetries = 3
	defaultRerankBaseBackoff = 100 * time.Millisecond
)

// NewRerankRetriever 创建重排序检索器
func NewRerankRetriever(cfg RerankRetrieverConfig) *RerankRetriever {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultRerankTimeout
	}
	topN := cfg.TopN
	if topN <= 0 {
		topN = defaultRerankTopN
	}
	threshold := cfg.ScoreThreshold
	if threshold <= 0 {
		threshold = defaultRerankThreshold
	}
	d := time.Duration(timeout) * time.Second
	return &RerankRetriever{
		inner:          cfg.Inner,
		endpoint:       cfg.Endpoint,
		model:          cfg.Model,
		apiKey:         cfg.APIKey,
		topN:           topN,
		timeout:        d,
		scoreThreshold: threshold,
		httpClient:     &http.Client{Timeout: d},
		maxRetries:     defaultRerankMaxRetries,
		baseBackoff:    defaultRerankBaseBackoff,
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

	result, err := r.inner.Retrieve(ctx, query)
	if err != nil {
		return Result{}, err
	}

	if !result.Hit || len(result.Documents) == 0 {
		logger.Infof("[Rerank] 内层检索无结果，跳过重排序")
		return result, nil
	}

	logger.Infof("[Rerank] 内层检索返回 %d 条，开始调用 Rerank API", len(result.Documents))
	for i, doc := range result.Documents {
		logger.Debugf("[Rerank]   输入#%d: [%s] score=%.4f chunk#%d title=%q content=%q",
			i, doc.DocumentID, doc.Score, doc.ChunkIndex, doc.Title, truncate(doc.Content, 60))
	}

	reranked, err := r.rerankWithRetry(ctx, query.Question, result.Documents)
	if err != nil {
		logger.Warnf("[Rerank] Rerank API 调用失败（已重试 %d 次），降级返回原始结果: %v", r.maxRetries, err)
		return result, nil
	}

	for _, item := range reranked {
		if item.Index >= 0 && item.Index < len(result.Documents) {
			oldScore := result.Documents[item.Index].Score
			result.Documents[item.Index].Score = item.RelevanceScore
			logger.Debugf("[Rerank]   文档#%d [%s] 分数: %.4f → %.4f",
				item.Index, result.Documents[item.Index].DocumentID, oldScore, item.RelevanceScore)
		}
	}

	sort.Slice(result.Documents, func(i, j int) bool {
		return result.Documents[i].Score > result.Documents[j].Score
	})

	filtered := make([]Document, 0, len(result.Documents))
	for _, doc := range result.Documents {
		if doc.Score >= r.scoreThreshold {
			filtered = append(filtered, doc)
		} else {
			logger.Debugf("[Rerank]   过滤: [%s] score=%.4f < 阈值 %.2f", doc.DocumentID, doc.Score, r.scoreThreshold)
		}
	}

	if len(filtered) > r.topN {
		logger.Infof("[Rerank] 截取 TopN: %d → %d", len(filtered), r.topN)
		filtered = filtered[:r.topN]
	}

	logger.Infof("[Rerank] 重排序完成: 原始 %d 条 → 过滤后 %d 条", len(result.Documents), len(filtered))
	return Result{
		Hit:       len(filtered) > 0,
		Documents: filtered,
	}, nil
}

// rerankWithRetry 调用 Rerank API，带指数退避重试
//
// 重试策略：
//   - 4xx（除 429）: 客户端错误，立即放弃（参数/鉴权问题，重试无意义）
//   - 429 / 5xx / 网络错误: 重试，baseBackoff * 2^attempt，最多 maxRetries 次
//   - context 取消: 立即放弃
func (r *RerankRetriever) rerankWithRetry(ctx context.Context, query string, docs []Document) ([]rerankResult, error) {
	var lastErr error

	for attempt := 0; attempt <= r.maxRetries; attempt++ {
		if attempt > 0 {
			// 计算退避时间
			backoff := r.baseBackoff * time.Duration(1<<uint(attempt-1))
			logger.Infof("[Rerank] 第 %d 次重试，等待 %v...", attempt, backoff)

			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
		}

		results, retry, err := r.tryRerank(ctx, query, docs)
		if err == nil {
			logger.Infof("[Rerank] API 调用成功（第 %d 次尝试）", attempt+1)
			return results, nil
		}

		lastErr = err
		if !retry {
			logger.Warnf("[Rerank] 错误不可重试，放弃: %v", err)
			return nil, err
		}
		logger.Warnf("[Rerank] 第 %d 次尝试失败（可重试）: %v", attempt+1, err)
	}

	return nil, fmt.Errorf("超过最大重试次数 %d，最后一次错误: %w", r.maxRetries, lastErr)
}

// tryRerank 执行单次 Rerank API 调用
// 返回值：
//   - results: 成功时的结果
//   - retry: 该错误是否值得重试
//   - err: 失败原因
func (r *RerankRetriever) tryRerank(ctx context.Context, query string, docs []Document) ([]rerankResult, bool, error) {
	documents := make([]string, 0, len(docs))
	for _, doc := range docs {
		documents = append(documents, doc.Content)
	}

	reqBody := rerankRequest{
		Query:     query,
		Documents: documents,
		Model:     r.model,
		TopN:      r.topN,
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		// JSON 序列化失败是确定性错误，不可重试
		return nil, false, fmt.Errorf("序列化请求失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, false, fmt.Errorf("创建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if r.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+r.apiKey)
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		// 网络错误 / 超时 / context 取消
		if ctx.Err() != nil {
			return nil, false, ctx.Err()
		}
		return nil, true, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, true, fmt.Errorf("读取响应失败: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusOK:
		var rerankResp rerankResponse
		if err := json.Unmarshal(respBody, &rerankResp); err != nil {
			// 响应格式错误，可能服务端临时返回异常内容，重试一次看看
			return nil, true, fmt.Errorf("解析响应失败: %w, status=200, body=%q", err, truncate(string(respBody), 200))
		}
		return rerankResp.Results, false, nil

	case http.StatusTooManyRequests: // 429
		return nil, true, fmt.Errorf("rerank API 限流(429)")

	default:
		if resp.StatusCode >= 500 {
			// 5xx 服务端错误，可重试
			return nil, true, fmt.Errorf("rerank API 返回 %d (5xx): %s", resp.StatusCode, truncate(string(respBody), 200))
		}
		// 其他 4xx（400, 401, 403, 404 等）：客户端错误，不可重试
		return nil, false, fmt.Errorf("rerank API 返回 %d (4xx, 不可重试): %s", resp.StatusCode, truncate(string(respBody), 200))
	}
}
