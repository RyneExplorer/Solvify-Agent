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

const defaultEmbeddingTimeoutSeconds = 60

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

	reqBody, err := json.Marshal(embeddingRequest{
		Model: s.cfg.Model,
		Input: texts,
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
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "读取向量响应失败", err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, apperrors.New(apperrors.CodeInternalError, fmt.Sprintf("文本向量模型返回异常状态: %d", resp.StatusCode))
	}

	var parsed embeddingResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "解析向量响应失败", err)
	}
	if len(parsed.Data) != len(texts) {
		return nil, apperrors.New(apperrors.CodeInternalError, "文本向量数量与分块数量不一致")
	}

	vectors := make([][]float32, len(parsed.Data))
	for _, item := range parsed.Data {
		if item.Index < 0 || item.Index >= len(parsed.Data) {
			return nil, apperrors.New(apperrors.CodeInternalError, "文本向量响应索引异常")
		}
		vectors[item.Index] = item.Embedding
	}
	return vectors, nil
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
