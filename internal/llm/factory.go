package llm

import (
	"context"
	"fmt"
	"sync"

	"solvify-agent/pkg/config"
)

// ModelConfig 描述从数据库解析出的模型配置
type ModelConfig struct {
	Provider string
	ModelID  string
	BaseURL  string
	APIKey   string
}

// clientCacheKey 用于缓存 key
type clientCacheKey struct {
	BaseURL string
	APIKey  string
	ModelID string
}

var llmClientCache sync.Map

// NewClientFromModelConfig 根据模型配置动态创建 LLM 客户端（带缓存）
func NewClientFromModelConfig(ctx context.Context, cfg ModelConfig) (Client, error) {
	switch cfg.Provider {
	case "mock":
		return NewMockClient(cfg.ModelID), nil
	case "openai", "deepseek", "zhipu", "tongyi":
		key := clientCacheKey{BaseURL: cfg.BaseURL, APIKey: cfg.APIKey, ModelID: cfg.ModelID}
		if cached, ok := llmClientCache.Load(key); ok {
			return cached.(Client), nil
		}
		client, err := NewOpenAIClient(ctx, OpenAIClientConfig{
			APIKey:  cfg.APIKey,
			BaseURL: cfg.BaseURL,
			Model:   cfg.ModelID,
		})
		if err != nil {
			return nil, err
		}
		llmClientCache.Store(key, client)
		return client, nil
	default:
		return nil, fmt.Errorf("不支持的 LLM 提供商: %s", cfg.Provider)
	}
}

// NewEmbeddingClientFromConfig 根据配置创建 Embedding 客户端
func NewEmbeddingClientFromConfig(ctx context.Context, cfg *config.EmbeddingConfig) (*EmbeddingClient, error) {
	return NewEmbeddingClient(ctx, EmbeddingClientConfig{
		APIKey:    cfg.APIKey,
		BaseURL:   cfg.BaseURL,
		Model:     cfg.Model,
		Dimension: cfg.Dimension,
	})
}
