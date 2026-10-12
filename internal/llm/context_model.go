package llm

import (
	"context"
	"encoding/json"
	"fmt"

	einoOpenai "github.com/cloudwego/eino-ext/components/model/openai"
	aclOpenai "github.com/cloudwego/eino-ext/libs/acl/openai"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/pkg/config"
	"solvify-agent/pkg/logger"
	"solvify-agent/pkg/tokenutil"
)

// contextModel 在序列化请求后核对实际消息、工具与输出选项，覆盖扩展选项。
// 原始模型仅在客户端内部保存，调用方得到的总是受保护对象。
type contextModel struct {
	inner     model.ToolCallingChatModel
	modelName string
	declared  int
	output    int
	policy    config.ContextPolicy
}

func (m *contextModel) ContextWindow() int       { return m.declared }
func (m *contextModel) OutputLimit() int         { return m.output }
func (m *contextModel) FinalBudgetChecked() bool { return true }
func (m *contextModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	inner, err := m.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	next := *m
	next.inner = inner
	return &next, nil
}
func (m *contextModel) callOptions(opts []model.Option) []model.Option {
	next := append([]model.Option(nil), opts...)
	common := model.GetCommonOptions(nil, opts...)
	// common MaxTokens 的显式覆盖同时更新实际 completion 上限。
	if common.MaxTokens != nil {
		next = append(next, einoOpenai.WithMaxCompletionTokens(*common.MaxTokens), model.WithMaxTokens(0))
	}
	return append(next, aclOpenai.WithRequestPayloadModifier(m.checkPayload))
}
func (m *contextModel) Generate(ctx context.Context, msgs []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	return m.inner.Generate(ctx, msgs, m.callOptions(opts)...)
}
func (m *contextModel) Stream(ctx context.Context, msgs []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return m.inner.Stream(ctx, msgs, m.callOptions(opts)...)
}
func (m *contextModel) checkPayload(ctx context.Context, _ []*schema.Message, raw []byte) ([]byte, error) {
	var req struct {
		Model               string `json:"model"`
		MaxTokens           int    `json:"max_tokens"`
		MaxCompletionTokens int    `json:"max_completion_tokens"`
		Messages            []struct {
			Role       schema.RoleType   `json:"role"`
			Content    json.RawMessage   `json:"content"`
			Name       string            `json:"name"`
			ToolCallID string            `json:"tool_call_id"`
			ToolCalls  []schema.ToolCall `json:"tool_calls"`
		} `json:"messages"`
		Tools json.RawMessage `json:"tools"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("%w: 请求解析失败", ErrContextConfig)
	}
	declared := m.declared
	if req.Model != m.modelName {
		declared = 0
	}
	modelID := req.Model
	if identity, ok := ctx.Value(budgetIdentityKey{}).(budgetIdentity); ok && req.Model == m.modelName {
		modelID = identity.modelID
		declared = min(declared, identity.declared)
	}
	output := max(req.MaxTokens, req.MaxCompletionTokens)
	budget, err := ResolveContextBudget(modelID, req.Model, declared, output, false, m.policy)
	if err != nil {
		return nil, err
	}
	used := 0
	for _, message := range req.Messages {
		var content string
		if len(message.Content) > 0 && string(message.Content) != "null" {
			if err = json.Unmarshal(message.Content, &content); err != nil {
				return nil, fmt.Errorf("%w: 暂不支持多模态预算", ErrContextConfig)
			}
		}
		used += MessageTokens(&schema.Message{Role: message.Role, Content: content, Name: message.Name, ToolCallID: message.ToolCallID, ToolCalls: message.ToolCalls}, req.Model)
	}
	if len(req.Tools) > 0 && string(req.Tools) != "null" {
		used += tokenutil.CountTokens(string(req.Tools), req.Model)
	}
	logger.Infof("最终模型预算：模型=%s 窗口=%d 输出=%d 裕量=%d 输入=%d 硬上限=%d 口径=估算", req.Model, budget.Limit, budget.Output, budget.Margin, used, budget.Hard)
	if err = budget.Check(used); err != nil {
		return nil, err
	}
	return raw, nil
}
