package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/schema"
)

// Client 定义模型调用边界
type Client interface {
	Generate(ctx context.Context, req GenerateRequest) (GenerateResponse, error)
	GenerateStream(ctx context.Context, req GenerateRequest) (<-chan StreamChunk, error)
}

// StreamChunk 描述流式生成的单个数据块
type StreamChunk struct {
	Content   string
	ToolCalls []ToolCall
	Done      bool
	Error     error
}

// GenerateRequest 描述一次 LLM 生成请求
type GenerateRequest struct {
	Messages []*schema.Message
	Model    string
	Tools    []Tool
}

// GenerateResponse 描述一次 LLM 生成响应
type GenerateResponse struct {
	Message   *schema.Message
	ToolCalls []ToolCall
	Model     string
}

// Tool 描述一个可被 LLM 调用的工具
type Tool struct {
	Name        string
	Description string
	Parameters  map[string]any
}

// ToolCall 描述一次工具调用
type ToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// MockClient 提供可离线运行的 Eino 消息模型示例
type MockClient struct {
	model string
}

// NewMockClient 创建 Mock LLM 客户端
func NewMockClient(model string) *MockClient {
	return &MockClient{model: model}
}

// Generate 基于输入消息生成可预测回答
func (c *MockClient) Generate(ctx context.Context, req GenerateRequest) (GenerateResponse, error) {
	if err := ctx.Err(); err != nil {
		return GenerateResponse{}, err
	}
	if len(req.Messages) == 0 {
		return GenerateResponse{}, errors.New("messages are required")
	}

	question := lastUserContent(req.Messages)
	if strings.TrimSpace(question) == "" {
		return GenerateResponse{}, errors.New("user question is required")
	}

	model := req.Model
	if model == "" {
		model = c.model
	}

	// 如果有工具且问题包含特定关键词，模拟工具调用
	if len(req.Tools) > 0 {
		for _, tool := range req.Tools {
			if tool.Name == "final_answer" {
				continue
			}
			if strings.Contains(question, tool.Name) || strings.Contains(question, "搜索") || strings.Contains(question, "search") {
				return GenerateResponse{
					Message: schema.AssistantMessage("", []schema.ToolCall{
						{
							ID:   fmt.Sprintf("call_%s_mock", tool.Name),
							Type: "function",
							Function: schema.FunctionCall{
								Name:      tool.Name,
								Arguments: fmt.Sprintf(`{"query": "%s"}`, question),
							},
						},
					}),
					ToolCalls: []ToolCall{
						{
							ID:        fmt.Sprintf("call_%s_mock", tool.Name),
							Name:      tool.Name,
							Arguments: fmt.Sprintf(`{"query": "%s"}`, question),
						},
					},
					Model: model,
				}, nil
			}
		}
	}

	content := fmt.Sprintf("基于当前上下文，我会按知识检索、工具结果和问题意图综合回答：%s", question)
	return GenerateResponse{
		Message: schema.AssistantMessage(content, nil),
		Model:   model,
	}, nil
}

// GenerateStream 基于输入消息生成流式回答
func (c *MockClient) GenerateStream(ctx context.Context, req GenerateRequest) (<-chan StreamChunk, error) {
	if len(req.Messages) == 0 {
		return nil, errors.New("messages are required")
	}

	question := lastUserContent(req.Messages)
	if strings.TrimSpace(question) == "" {
		return nil, errors.New("user question is required")
	}

	ch := make(chan StreamChunk, 1)
	go func() {
		defer close(ch)
		content := fmt.Sprintf("基于当前上下文，我会按知识检索、工具结果和问题意图综合回答：%s", question)
		// 模拟逐字输出
		for _, char := range content {
			select {
			case <-ctx.Done():
				ch <- StreamChunk{Error: ctx.Err(), Done: true}
				return
			case ch <- StreamChunk{Content: string(char)}:
			}
		}
		ch <- StreamChunk{Done: true}
	}()

	return ch, nil
}

// lastUserContent 提取最后一条用户消息内容
func lastUserContent(messages []*schema.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i] != nil && messages[i].Role == schema.User {
			return messages[i].Content
		}
	}
	return ""
}
