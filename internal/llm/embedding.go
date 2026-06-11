package llm

import (
	"context"
	"fmt"

	einoOpenai "github.com/cloudwego/eino-ext/components/embedding/openai"
)

// EmbeddingClient 基于 eino-ext 实现文本向量化客户端
type EmbeddingClient struct {
	embedder *einoOpenai.Embedder
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

	return &EmbeddingClient{embedder: embedder}, nil
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
	return c.embedder.EmbedStrings(ctx, texts)
}
