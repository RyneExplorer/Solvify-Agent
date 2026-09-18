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
// 缓存的 key 与创建调用都从这一个方法取值，所以「key 漏字段」在结构上不可能再发生 ——
// 原先的写法是：查询路径手拼一份 key、预热路径另手拼一份，两份字段清单不同步。
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
func supportsProvider(provider string) bool {
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
	if !supportsProvider(cfg.Provider) {
		return nil, fmt.Errorf("不支持的 LLM 提供商: %s", cfg.Provider)
	}
	return getOrCreateClient(ctx, cfg)
}

// getOrCreateClient 是「查缓存 → 未命中则创建并回填」的唯一实现。
//
// 预热也走这里：只要还存在第二条自己拼 key、自己 Store 的写入路径，
// 两条路径的 key 口径就有分叉的空间 —— P1-8 正是这么发生的。
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

// PrewarmClients 启动时预创建所有已启用系统模型的 LLM 客户端。
// 效果：首次请求命中缓存，跳过 DB 查询 + 客户端创建，消除冷启动延迟。
//
// 入参类型此前是 SystemModelInfo —— 一个「ModelConfig 的子集」结构，
// 它恰好漏掉了 Config 字段，而预热又自己手拼了一遍缓存 key。
// 于是凡是在库里配了 temperature / timeout（Config 非空）的模型，
// 预热写入的条目永远命中不到；Config 为空的模型碰巧能命中，
// 所以这个 bug 长期表现为「有时候有效」，排查时极易被当成偶发。
//
// 现在预热与请求共用 ModelConfig + getOrCreateClient：
// ① key 只有一处定义；② 预热「成功」与请求「能用」从此是同一个判据
// （原先预热对请求路径根本不支持的 Provider 也会报成功）。
func PrewarmClients(ctx context.Context, models []ModelConfig) {
	success, fail := 0, 0
	for _, m := range models {
		if _, err := NewClientFromModelConfig(ctx, m); err != nil {
			logger.Warnf("预热模型客户端失败: modelID=%s, provider=%s, err=%v", m.ModelID, m.Provider, err)
			fail++
			continue
		}
		success++
		logger.Infof("预热模型客户端成功: modelID=%s", m.ModelID)
	}
	logger.Infof("预热模型客户端完成: 总计=%d, 成功=%d, 失败=%d", len(models), success, fail)
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
