package llm

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	einoOpenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// sharedHTTPClient 所有 LLM 客户端共享的 HTTP 连接池
// 同 Host 的模型（如多个 OpenAI 模型）复用 TCP/TLS 连接，消除重复握手开销
var (
	sharedHTTPClientOnce sync.Once
	sharedHTTPClient     *http.Client
)

func getSharedHTTPClient() *http.Client {
	sharedHTTPClientOnce.Do(func() {
		sharedHTTPClient = &http.Client{
			Transport: &http.Transport{
				DialContext: (&net.Dialer{
					Timeout:   10 * time.Second,
					KeepAlive: 60 * time.Second, // TCP keep-alive 探活
				}).DialContext,
				TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
				MaxIdleConns:          100,
				MaxIdleConnsPerHost:   20,               // 同 Host 最多保持 20 个空闲连接
				MaxConnsPerHost:       50,               // 同 Host 最多 50 个并发连接
				IdleConnTimeout:       10 * time.Minute, // 空闲连接保活 10 分钟
				TLSHandshakeTimeout:   5 * time.Second,
				ResponseHeaderTimeout: 60 * time.Second,
				ForceAttemptHTTP2:     true, // 启用 HTTP/2 多路复用
			},
		}
	})
	return sharedHTTPClient
}

// OpenAIClient 基于 eino-ext 实现 OpenAI 兼容 API 客户端
type OpenAIClient struct {
	chatModel *einoOpenai.ChatModel
	model     string
}

// OpenAIClientConfig 描述 OpenAI 客户端配置
type OpenAIClientConfig struct {
	APIKey  string
	BaseURL string
	Model   string
	Config  []byte // 可选，数据库中的 JSON 扩展配置
}

// ModelExtraConfig 描述数据库 Config JSON 中可配置的参数
type ModelExtraConfig struct {
	Temperature         *float32 `json:"temperature,omitempty"`
	MaxCompletionTokens *int     `json:"max_completion_tokens,omitempty"`
	Timeout             *int     `json:"timeout,omitempty"` // 秒
}

// NewOpenAIClient 创建 OpenAI 兼容客户端
func NewOpenAIClient(ctx context.Context, cfg OpenAIClientConfig) (*OpenAIClient, error) {
	// 解析扩展配置
	var extra ModelExtraConfig
	if len(cfg.Config) > 0 {
		if err := json.Unmarshal(cfg.Config, &extra); err != nil {
			return nil, fmt.Errorf("解析模型扩展配置失败: %w", err)
		}
	}

	defaultMaxCompletionTokens := 4096
	config := &einoOpenai.ChatModelConfig{
		APIKey:              cfg.APIKey,
		BaseURL:             cfg.BaseURL,
		Model:               cfg.Model,
		HTTPClient:          getSharedHTTPClient(),
		MaxCompletionTokens: &defaultMaxCompletionTokens,
	}

	// 用户配置覆盖默认值
	if extra.Temperature != nil {
		config.Temperature = extra.Temperature
	}
	if extra.MaxCompletionTokens != nil {
		config.MaxCompletionTokens = extra.MaxCompletionTokens
	}
	if extra.Timeout != nil && *extra.Timeout > 0 {
		config.Timeout = toDuration(*extra.Timeout)
	}

	cm, err := einoOpenai.NewChatModel(ctx, config)
	if err != nil {
		return nil, err
	}

	return &OpenAIClient{
		chatModel: cm,
		model:     cfg.Model,
	}, nil
}

// ChatModel 返回底层 eino ChatModel，满足 model.ToolCallingChatModel 接口
func (c *OpenAIClient) ChatModel() model.ToolCallingChatModel {
	return c.chatModel
}

// TestConnection 发送一个最小化请求来验证模型连接是否真正可用
func (c *OpenAIClient) TestConnection(ctx context.Context) error {
	_, err := c.chatModel.Generate(ctx, []*schema.Message{
		{Role: schema.User, Content: "hi"},
	})
	return err
}

func toDuration(seconds int) time.Duration {
	return time.Duration(seconds) * time.Second
}
