package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"solvify-agent/pkg/config"
	apperrors "solvify-agent/pkg/errors"
)

const (
	defaultEmbeddingTimeoutSeconds = 60
	defaultEmbeddingBatchSize      = 10
)

// embeddingService 封装 OpenAI 兼容文本向量调用
type embeddingService struct {
	cfg        config.EmbeddingConfig
	httpClient *http.Client
}

// NewEmbeddingService 创建文本向量服务
func NewEmbeddingService(cfg config.EmbeddingConfig) EmbeddingServiceInterface {
	return &embeddingService{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: defaultEmbeddingTimeoutSeconds * time.Second,
		},
	}
}

// Model 返回当前向量模型名称
func (s *embeddingService) Model() string {
	return s.cfg.Model
}

// Dimension 返回当前向量维度
func (s *embeddingService) Dimension() int {
	return s.cfg.Dimension
}

// EmbedTexts 批量生成文本向量
func (s *embeddingService) EmbedTexts(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	if strings.TrimSpace(s.cfg.APIKey) == "" || strings.TrimSpace(s.cfg.BaseURL) == "" {
		return nil, apperrors.New(apperrors.CodeInternalError, "文本向量模型配置不完整")
	}

	batchSize := s.cfg.BatchSize
	if batchSize <= 0 {
		batchSize = defaultEmbeddingBatchSize
	}
	vectors := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += batchSize {
		end := start + batchSize
		if end > len(texts) {
			end = len(texts)
		}
		batchTexts := texts[start:end]
		reqBody, err := json.Marshal(embeddingRequest{
			Model: s.cfg.Model,
			Input: batchTexts,
		})
		if err != nil {
			return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "构建向量请求失败", err)
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.embeddingURL(), bytes.NewReader(reqBody))
		if err != nil {
			return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "创建向量请求失败", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+s.cfg.APIKey)

		resp, err := s.httpClient.Do(req)
		if err != nil {
			return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "调用文本向量模型失败", err)
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
		resp.Body.Close()
		if readErr != nil {
			return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "读取向量响应失败", readErr)
		}
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			return nil, normalizeEmbeddingHTTPError(resp.StatusCode, body, s.cfg.Model)
		}

		var parsed embeddingResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "解析向量响应失败", err)
		}
		if len(parsed.Data) != len(batchTexts) {
			return nil, apperrors.New(apperrors.CodeInternalError, "文本向量数量与分块数量不一致")
		}

		batchVectors := make([][]float32, len(parsed.Data))
		for _, item := range parsed.Data {
			if item.Index < 0 || item.Index >= len(parsed.Data) {
				return nil, apperrors.New(apperrors.CodeInternalError, "文本向量响应索引异常")
			}
			batchVectors[item.Index] = item.Embedding
		}
		vectors = append(vectors, batchVectors...)
	}
	return vectors, nil
}

func normalizeEmbeddingHTTPError(statusCode int, body []byte, model string) error {
	message := strings.ToLower(string(body))
	if strings.Contains(message, "model") && strings.Contains(message, "not found") {
		if strings.TrimSpace(model) == "" {
			model = "当前向量模型"
		}
		return apperrors.New(apperrors.CodeInternalError, fmt.Sprintf("向量模型 %q 未安装，请先拉取或切换可用的向量模型", model))
	}
	if statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden {
		return apperrors.New(apperrors.CodeInternalError, "向量服务认证失败，请检查 API Key 配置")
	}
	if statusCode == http.StatusTooManyRequests {
		return apperrors.New(apperrors.CodeInternalError, "向量服务请求过多，请稍后重试")
	}
	return apperrors.New(apperrors.CodeInternalError, "向量服务调用失败，请检查 Embedding 配置")
}

// embeddingURL 返回 OpenAI 兼容向量接口地址
func (s *embeddingService) embeddingURL() string {
	baseURL := strings.TrimRight(s.cfg.BaseURL, "/")
	if strings.HasSuffix(baseURL, "/embeddings") {
		return baseURL
	}
	return baseURL + "/embeddings"
}

// embeddingRequest 定义文本向量请求体
type embeddingRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

// embeddingResponse 定义文本向量响应体
type embeddingResponse struct {
	Data []embeddingResponseItem `json:"data"`
}

// embeddingResponseItem 定义单条文本向量结果
type embeddingResponseItem struct {
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}
