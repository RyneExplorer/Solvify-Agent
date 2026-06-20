package llm

import (
	"context"
	"fmt"
	"strings"

	einoOpenai "github.com/cloudwego/eino-ext/components/embedding/openai"
)

// EmbeddingClient 基于 eino-ext 实现文本向量化客户端
type EmbeddingClient struct {
	embedder *einoOpenai.Embedder
	model    string
	baseURL  string
}

// EmbeddingClientConfig 描述 Embedding 客户端配置
type EmbeddingClientConfig struct {
	APIKey    string
	BaseURL   string
	Model     string
	Dimension int
	Timeout   int
}

// NewEmbeddingClient 创建 Embedding 客户端
func NewEmbeddingClient(ctx context.Context, cfg EmbeddingClientConfig) (*EmbeddingClient, error) {
	config := &einoOpenai.EmbeddingConfig{
		APIKey:  cfg.APIKey,
		BaseURL: cfg.BaseURL,
		Model:   cfg.Model,
	}

	if cfg.Dimension > 0 {
		dimension := cfg.Dimension
		config.Dimensions = &dimension
	}
	if cfg.Timeout > 0 {
		config.Timeout = toDuration(cfg.Timeout)
	}

	embedder, err := einoOpenai.NewEmbedder(ctx, config)
	if err != nil {
		return nil, err
	}

	return &EmbeddingClient{embedder: embedder, model: cfg.Model, baseURL: cfg.BaseURL}, nil
}

// Embed 将单个文本转换为向量
func (c *EmbeddingClient) Embed(ctx context.Context, text string) ([]float64, error) {
	results, err := c.EmbedBatch(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("embedding API 返回空结果")
	}
	return results[0], nil
}

// EmbedBatch 将多个文本批量转换为向量
func (c *EmbeddingClient) EmbedBatch(ctx context.Context, texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	results, err := c.embedder.EmbedStrings(ctx, texts)
	if err != nil {
		return nil, c.normalizeError(err)
	}
	return results, nil
}

func (c *EmbeddingClient) normalizeError(err error) error {
	if err == nil {
		return nil
	}
	errMsg := err.Error()
	lower := strings.ToLower(errMsg)
	model := c.model
	if strings.TrimSpace(model) == "" {
		model = "当前向量模型"
	}
	if strings.Contains(lower, "model") && strings.Contains(lower, "not found") {
		return fmt.Errorf("向量模型 %q 未安装，请先拉取或切换可用的向量模型", model)
	}
	if strings.Contains(lower, "connection refused") || strings.Contains(lower, "actively refused") || strings.Contains(lower, "connectex") {
		return fmt.Errorf("向量服务连接失败，请检查 Embedding 服务是否已启动")
	}
	if strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded") {
		return fmt.Errorf("向量服务响应超时，请稍后重试")
	}
	if strings.Contains(lower, "401") || strings.Contains(lower, "403") || strings.Contains(lower, "unauthorized") || strings.Contains(lower, "forbidden") {
		return fmt.Errorf("向量服务认证失败，请检查 API Key 配置")
	}
	return fmt.Errorf("向量服务调用失败，请检查 Embedding 配置")
}
