package agent

import (
	"context"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// budgetModel 在模型边界覆盖 Planner、Executor、Replanner 和专业 Agent。
// 压缩使用消息副本，不修改框架 checkpoint 或异步追踪正在读取的消息。
type budgetModel struct {
	inner model.ToolCallingChatModel
	guard *budgetGuard
	tools []*schema.ToolInfo
}

func (m *budgetModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	inner, err := m.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &budgetModel{inner: inner, guard: m.guard, tools: append([]*schema.ToolInfo(nil), tools...)}, nil
}

func (m *budgetModel) Generate(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	options := model.GetCommonOptions(nil, opts...)
	tools := m.tools
	if options.Tools != nil {
		tools = options.Tools
	}
	ctx, state, err := m.guard.BeforeModelRewriteState(ctx, &adk.ChatModelAgentState{Messages: messages, ToolInfos: tools}, nil)
	if err != nil {
		return nil, err
	}
	return m.inner.Generate(ctx, state.Messages, opts...)
}

func (m *budgetModel) Stream(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	options := model.GetCommonOptions(nil, opts...)
	tools := m.tools
	if options.Tools != nil {
		tools = options.Tools
	}
	ctx, state, err := m.guard.BeforeModelRewriteState(ctx, &adk.ChatModelAgentState{Messages: messages, ToolInfos: tools}, nil)
	if err != nil {
		return nil, err
	}
	return m.inner.Stream(ctx, state.Messages, opts...)
}
