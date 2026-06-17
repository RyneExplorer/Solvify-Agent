package llm

import (
	"context"
	"fmt"
	"sync"
	"time"

	"solvify-agent/pkg/config"
	"solvify-agent/pkg/logger"
)

// ModelConfig 描述从数据库解析出的模型配置
type ModelConfig struct {
	Provider string
	ModelID  string
	BaseURL  string
	APIKey   string
	Config   []byte // entity.Model.Config 或 entity.UserModelConfig.Config 的原始 JSON
}

// clientCacheKey 用于缓存 key
type clientCacheKey struct {
	BaseURL string
	APIKey  string
	ModelID string
	Config  string // 序列化后的配置，确保不同配置不复用
}

var llmClientCache sync.Map

// NewClientFromModelConfig 根据模型配置动态创建 LLM 客户端（带缓存）
func NewClientFromModelConfig(ctx context.Context, cfg ModelConfig) (*OpenAIClient, error) {
	switch cfg.Provider {
	case "openai", "deepseek", "zhipu", "tongyi":
		key := clientCacheKey{BaseURL: cfg.BaseURL, APIKey: cfg.APIKey, ModelID: cfg.ModelID, Config: string(cfg.Config)}
		if cached, ok := llmClientCache.Load(key); ok {
			logger.Infof("[LLM] 客户端缓存命中: modelID=%s", cfg.ModelID)
			return cached.(*OpenAIClient), nil
		}
		logger.Infof("[LLM] 客户端缓存未命中: modelID=%s, 创建新客户端...", cfg.ModelID)
		t0 := time.Now()
		client, err := NewOpenAIClient(ctx, OpenAIClientConfig{
			APIKey:  cfg.APIKey,
			BaseURL: cfg.BaseURL,
			Model:   cfg.ModelID,
			Config:  cfg.Config,
		})
		logger.Infof("[LLM] 创建客户端耗时: modelID=%s, cost=%dms", cfg.ModelID, time.Since(t0).Milliseconds())
		if err != nil {
			return nil, err
		}
		llmClientCache.Store(key, client)
		return client, nil
	default:
		return nil, fmt.Errorf("不支持的 LLM 提供商: %s", cfg.Provider)
	}
}

// PrewarmClients 启动时预创建所有已启用系统模型的 LLM 客户端
// 效果：首次请求命中缓存，跳过 DB 查询 + 客户端创建，消除冷启动延迟
func PrewarmClients(ctx context.Context, models []SystemModelInfo) {
	success, fail := 0, 0
	for _, m := range models {
		client, err := NewOpenAIClient(ctx, OpenAIClientConfig{
			APIKey:  m.APIKey,
			BaseURL: m.BaseURL,
			Model:   m.ModelID,
		})
		if err != nil {
			logger.Warnf("预热模型客户端失败: modelID=%s, err=%v", m.ModelID, err)
			fail++
			continue
		}
		key := clientCacheKey{BaseURL: m.BaseURL, APIKey: m.APIKey, ModelID: m.ModelID}
		llmClientCache.Store(key, client)
		success++
		logger.Infof("预热模型客户端成功: modelID=%s", m.ModelID)
	}
	logger.Infof("预热模型客户端完成: 总计=%d, 成功=%d, 失败=%d", len(models), success, fail)
}

// SystemModelInfo 描述系统模型信息（用于预热）
type SystemModelInfo struct {
	ModelID string
	BaseURL string
	APIKey  string
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
