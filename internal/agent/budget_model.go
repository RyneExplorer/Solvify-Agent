package agent

import (
	"context"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// budgetModel 管理调用副本；生产客户端在序列化边界核对实际供应商选项。
type budgetModel struct {
	inner model.ToolCallingChatModel
	guard *budgetGuard
	tools []*schema.ToolInfo
}

func (m *budgetModel) OutputLimit() int { return m.guard.outputLimit }

func (m *budgetModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	inner, err := m.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &budgetModel{inner: inner, guard: m.guard, tools: append([]*schema.ToolInfo(nil), tools...)}, nil
}
func (m *budgetModel) prepare(ctx context.Context, messages []*schema.Message, opts []model.Option) (context.Context, []*schema.Message, error) {
	options := model.GetCommonOptions(nil, opts...)
	tools := m.tools
	if options.Tools != nil {
		tools = options.Tools
	}
	guard := *m.guard
	if options.MaxTokens != nil {
		guard.outputLimit = *options.MaxTokens
	}
	if options.Model != nil && *options.Model != guard.modelName {
		guard.modelName = *options.Model
		guard.declaredCtx = 0
	}
	if final, ok := m.inner.(interface{ FinalBudgetChecked() bool }); ok {
		guard.finalCheckElsewhere = final.FinalBudgetChecked()
	}
	ctx, state, err := guard.BeforeModelRewriteState(ctx, &adk.ChatModelAgentState{Messages: messages, ToolInfos: tools}, nil)
	return ctx, state.Messages, err
}
func (m *budgetModel) Generate(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	ctx, input, err := m.prepare(ctx, messages, opts)
	if err != nil {
		return nil, err
	}
	return m.inner.Generate(ctx, input, opts...)
}
func (m *budgetModel) Stream(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	ctx, input, err := m.prepare(ctx, messages, opts)
	if err != nil {
		return nil, err
	}
	return m.inner.Stream(ctx, input, opts...)
}
