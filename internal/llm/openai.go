package llm

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"sync"
	"time"

	einoOpenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/pkg/config"
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
	chatModel        model.ToolCallingChatModel
	outputLimit      int
	model            string
	maxContextLength int
}

// OpenAIClientConfig 描述 OpenAI 客户端配置
type OpenAIClientConfig struct {
	APIKey           string
	BaseURL          string
	Model            string
	Config           []byte // 可选，数据库中的 JSON 扩展配置
	MaxContextLength int    // 模型最大上下文 token 长度
}

// NewOpenAIClient 创建 OpenAI 兼容客户端
func NewOpenAIClient(ctx context.Context, cfg OpenAIClientConfig) (*OpenAIClient, error) {
	// 解析扩展配置
	extra, err := ParseModelOptions(cfg.Config, cfg.MaxContextLength)
	if err != nil {
		return nil, err
	}

	contextPolicy := config.ContextDefaults()
	config := &einoOpenai.ChatModelConfig{
		APIKey:     cfg.APIKey,
		BaseURL:    cfg.BaseURL,
		Model:      cfg.Model,
		HTTPClient: getSharedHTTPClient(),
	}

	// 用户配置覆盖默认值
	if extra.Temperature != nil {
		config.Temperature = extra.Temperature
	}
	if extra.OutputTokenField == "max_completion_tokens" {
		config.MaxCompletionTokens = extra.MaxCompletionTokens
	} else {
		config.MaxTokens = extra.MaxCompletionTokens
	}
	if extra.Timeout != nil && *extra.Timeout > 0 {
		config.Timeout = toDuration(*extra.Timeout)
	}

	cm, err := einoOpenai.NewChatModel(ctx, config)
	if err != nil {
		return nil, err
	}

	maxCtx := cfg.MaxContextLength

	return &OpenAIClient{
		chatModel:        &contextModel{inner: cm, modelName: cfg.Model, declared: maxCtx, output: *extra.MaxCompletionTokens, policy: contextPolicy, options: &extra},
		outputLimit:      *extra.MaxCompletionTokens,
		model:            cfg.Model,
		maxContextLength: maxCtx,
	}, nil
}

// ChatModel 返回底层 eino ChatModel，满足 model.ToolCallingChatModel 接口
func (c *OpenAIClient) ChatModel() model.ToolCallingChatModel {
	return c.chatModel
}

// ModelName 返回具体模型名（用于编码解析 / tiktoken / 指标分桶），
// 对用户配置的自定义模型别名不做猜测。已知模型名（gpt-4o / qwen-* / deepseek-chat ...）可通过
// tiktoken.EncodingForModel 拿到真 BPE；未知模型回退 cl100k_base。
func (c *OpenAIClient) InputLimit() int  { return c.chatModel.(*contextModel).InputLimit() }
func (c *OpenAIClient) OutputLimit() int { return c.outputLimit }

func (c *OpenAIClient) CheckAgentTools() error {
	return c.chatModel.(*contextModel).options.CheckTools()
}

func (c *OpenAIClient) ModelName() string {
	if c == nil {
		return ""
	}
	return c.model
}

// MaxContextLength 返回模型最大上下文 token 长度
func (c *OpenAIClient) MaxContextLength() int {
	return c.maxContextLength
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
