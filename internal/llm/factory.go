package llm

import (
	"context"
	"fmt"
	"time"

	"solvify-agent/pkg/config"
	"solvify-agent/pkg/logger"
)

// ModelConfig 描述从数据库解析出的模型配置
type ModelConfig struct {
	Provider         string
	ModelID          string
	BaseURL          string
	APIKey           string
	Config           []byte // entity.Model.Config 或 entity.UserModelConfig.Config 的原始 JSON
	MaxContextLength int
}

// openAIConfig 把 ModelConfig 归一成「创建客户端所需的完整入参」。
//
// 缓存 key 与客户端创建共用这份配置，避免字段口径不一致。
func (cfg ModelConfig) openAIConfig() OpenAIClientConfig {
	return OpenAIClientConfig{
		APIKey:           cfg.APIKey,
		BaseURL:          cfg.BaseURL,
		Model:            cfg.ModelID,
		Config:           cfg.Config,
		MaxContextLength: cfg.MaxContextLength,
	}
}

// supportsProvider 判断该服务商是否走 OpenAI 兼容客户端路径。
//
// 所有分支最终都由 NewOpenAIClient 构建，故服务商本身不参与缓存 key（不改变客户端本体）。
func SupportsProvider(provider string) bool {
	switch provider {
	case "openai", "deepseek", "zhipu", "tongyi":
		return true
	default:
		return false
	}
}

// NewOpenAIClientDirect 直接创建客户端（跳过缓存），用于连接测试
func NewOpenAIClientDirect(ctx context.Context, cfg OpenAIClientConfig) (*OpenAIClient, error) {
	return NewOpenAIClient(ctx, cfg)
}

// NewClientFromModelConfig 根据模型配置动态创建 LLM 客户端（带缓存）
func NewClientFromModelConfig(ctx context.Context, cfg ModelConfig) (*OpenAIClient, error) {
	if !SupportsProvider(cfg.Provider) {
		return nil, fmt.Errorf("不支持的 LLM 提供商: %s", cfg.Provider)
	}
	return getOrCreateClient(ctx, cfg)
}

// getOrCreateClient 是「查缓存 → 未命中则创建并回填」的唯一实现。
//
// 客户端按需创建，后续相同配置的请求复用缓存实例。
func getOrCreateClient(ctx context.Context, cfg ModelConfig) (*OpenAIClient, error) {
	req := cfg.openAIConfig()
	key := req.cacheKey()

	if cached, ok := llmClientCache.Get(key); ok {
		logger.Infof("[LLM] 客户端缓存命中: modelID=%s", cfg.ModelID)
		return cached, nil
	}
	logger.Infof("[LLM] 客户端缓存未命中: modelID=%s, 创建新客户端...", cfg.ModelID)
	t0 := time.Now()
	client, err := NewOpenAIClient(ctx, req)
	logger.Infof("[LLM] 创建客户端耗时: modelID=%s, cost=%dms", cfg.ModelID, time.Since(t0).Milliseconds())
	if err != nil {
		return nil, err
	}
	llmClientCache.Put(key, client)
	return client, nil
}

// NewEmbeddingClientFromConfig 根据配置创建 Embedding 客户端
func NewEmbeddingClientFromConfig(ctx context.Context, cfg *config.EmbeddingConfig) (*EmbeddingClient, error) {
	return NewEmbeddingClient(ctx, EmbeddingClientConfig{
		APIKey:    cfg.APIKey,
		BaseURL:   cfg.BaseURL,
		Model:     cfg.Model,
		Dimension: cfg.Dimension,
		Timeout:   cfg.Timeout,
	})
}
