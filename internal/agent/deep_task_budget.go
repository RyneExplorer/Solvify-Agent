package agent

import (
	"context"
	"fmt"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// 每个专业任务独占预算，随子 checkpoint 保存；WithTools 副本共享同一计数。
// 模型重试同样经过此边界，不能获得额外额度。
type taskModelBudget struct {
	Limit int
	Used  int
}

type peTaskModel struct {
	inner  model.ToolCallingChatModel
	budget *taskModelBudget
}

func (m *peTaskModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	inner, err := m.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &peTaskModel{inner: inner, budget: m.budget}, nil
}

func (m *peTaskModel) Generate(ctx context.Context, msgs []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b := m.budget
	if b.Used >= b.Limit {
		return nil, fmt.Errorf("专业任务模型调用额度已用尽（%d/%d次）: %w", b.Used, b.Limit, adk.ErrExceedMaxIterations)
	}
	b.Used++
	remaining := b.Limit - b.Used
	notice := fmt.Sprintf("本任务模型调用额度：当前第%d/%d次，之后剩余%d次。工具结果需要后续模型调用处理；任务完成后及时输出结论。", b.Used, b.Limit, remaining)
	if remaining == 0 {
		notice += "这是最后一次调用，只能基于已有结果输出结论；缺少必要证据或操作未完成时明确说明限制，不得声称全部完成，不再请求工具。"
	}
	// 提示只写入调用副本；底层 budgetModel 仍会将它计入最终输入额度。
	input := append([]*schema.Message(nil), msgs...)
	if len(input) > 0 && input[0].Role == schema.System {
		instruction := *input[0]
		instruction.Content += "\n" + notice
		input[0] = &instruction
	} else {
		input = append([]*schema.Message{schema.SystemMessage(notice)}, input...)
	}
	ctx = context.WithValue(ctx, modelRecoveryBudgetKey{}, func() error {
		if b.Used >= b.Limit {
			return fmt.Errorf("专业任务没有上下文恢复额度（%d/%d次）: %w", b.Used, b.Limit, adk.ErrExceedMaxIterations)
		}
		b.Used++
		return nil
	})
	msg, err := m.inner.Generate(ctx, input, opts...)
	if err == nil && msg == nil {
		return nil, errModelOutputInvalid
	}
	if err == nil && len(msg.ToolCalls) > 0 {
		// 截断或结束状态异常的工具请求不得产生副作用。
		if err = validateModelCompletion(msg); err != nil {
			return nil, err
		}
	}
	if err == nil && b.Used == b.Limit && len(msg.ToolCalls) > 0 {
		// 保留最后一次处理结果的机会，不执行没有后续处理额度的工具（尤其是副作用工具）。
		return nil, fmt.Errorf("专业任务已达%d次模型调用上限，未执行新的工具请求: %w", b.Limit, adk.ErrExceedMaxIterations)
	}
	return msg, err
}

func (m *peTaskModel) Stream(ctx context.Context, msgs []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, msgs, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}
