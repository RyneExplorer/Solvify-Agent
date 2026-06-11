package llm

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	einoOpenai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/eino-contrib/jsonschema"
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
	APIKey              string
	BaseURL             string
	Model               string
	Temperature         float32
	MaxCompletionTokens int
	Timeout             int
}

// NewOpenAIClient 创建 OpenAI 兼容客户端
func NewOpenAIClient(ctx context.Context, cfg OpenAIClientConfig) (*OpenAIClient, error) {
	config := &einoOpenai.ChatModelConfig{
		APIKey:     cfg.APIKey,
		BaseURL:    cfg.BaseURL,
		Model:      cfg.Model,
		HTTPClient: getSharedHTTPClient(),
	}

	if cfg.Temperature > 0 {
		config.Temperature = &cfg.Temperature
	}
	if cfg.MaxCompletionTokens > 0 {
		maxCompletionTokens := cfg.MaxCompletionTokens
		config.MaxCompletionTokens = &maxCompletionTokens
	}
	if cfg.Timeout > 0 {
		config.Timeout = toDuration(cfg.Timeout)
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

// Generate 调用 OpenAI 兼容 API 生成回答
func (c *OpenAIClient) Generate(ctx context.Context, req GenerateRequest) (GenerateResponse, error) {
	opts := buildOptions(req)
	msg, err := c.chatModel.Generate(ctx, req.Messages, opts...)
	if err != nil {
		return GenerateResponse{}, err
	}
	return GenerateResponse{
		Message:   msg,
		ToolCalls: convertToolCalls(msg.ToolCalls),
		Model:     req.Model,
	}, nil
}

// GenerateStream 调用 OpenAI 兼容 API 流式生成回答
func (c *OpenAIClient) GenerateStream(ctx context.Context, req GenerateRequest) (<-chan StreamChunk, error) {
	opts := buildOptions(req)
	streamReader, err := c.chatModel.Stream(ctx, req.Messages, opts...)
	if err != nil {
		return nil, err
	}

	ch := make(chan StreamChunk, 100)
	go func() {
		defer close(ch)
		defer streamReader.Close()
		for {
			msg, recvErr := streamReader.Recv()
			if recvErr != nil {
				if recvErr == io.EOF {
					ch <- StreamChunk{Done: true}
				} else {
					ch <- StreamChunk{Error: recvErr, Done: true}
				}
				return
			}
			if msg != nil {
				chunk := StreamChunk{Content: msg.Content}
				if len(msg.ToolCalls) > 0 {
					chunk.ToolCalls = convertToolCalls(msg.ToolCalls)
				}
				ch <- chunk
			}
		}
	}()

	return ch, nil
}

func buildOptions(req GenerateRequest) []model.Option {
	var opts []model.Option
	if req.Model != "" {
		opts = append(opts, model.WithModel(req.Model))
	}
	if len(req.Tools) > 0 {
		opts = append(opts, model.WithTools(toolsToSchema(req.Tools)))
	}
	return opts
}

// toolsToSchema 将 llm.Tool 转换为 eino schema.ToolInfo
func toolsToSchema(tools []Tool) []*schema.ToolInfo {
	result := make([]*schema.ToolInfo, 0, len(tools))
	for _, t := range tools {
		info := &schema.ToolInfo{
			Name: t.Name,
			Desc: t.Description,
		}
		if t.Parameters != nil {
			info.ParamsOneOf = schema.NewParamsOneOfByJSONSchema(buildJSONSchema(t.Parameters))
		}
		result = append(result, info)
	}
	return result
}

// buildJSONSchema 从 map 构建 JSON Schema
func buildJSONSchema(params map[string]any) *jsonschema.Schema {
	s := &jsonschema.Schema{
		Type:       "object",
		Properties: jsonschema.NewProperties(),
	}
	required := make([]string, 0)
	for name, p := range params {
		if m, ok := p.(map[string]any); ok {
			prop := &jsonschema.Schema{}
			if t, ok := m["type"].(string); ok {
				prop.Type = t
			}
			if d, ok := m["description"].(string); ok {
				prop.Description = d
			}
			if items, ok := m["items"].(map[string]any); ok {
				if itemType, ok := items["type"].(string); ok {
					prop.Items = &jsonschema.Schema{Type: itemType}
				}
			}
			s.Properties.Set(name, prop)
			if req, ok := m["required"].(bool); ok && req {
				required = append(required, name)
			}
		}
	}
	if len(required) > 0 {
		s.Required = required
	}
	return s
}

// convertToolCalls 将 eino schema.ToolCall 转换为 llm.ToolCall
func convertToolCalls(calls []schema.ToolCall) []ToolCall {
	if len(calls) == 0 {
		return nil
	}
	result := make([]ToolCall, 0, len(calls))
	for _, tc := range calls {
		result = append(result, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}
	return result
}

func toDuration(seconds int) time.Duration {
	return time.Duration(seconds) * time.Second
}
