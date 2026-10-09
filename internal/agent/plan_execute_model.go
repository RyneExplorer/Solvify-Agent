package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// peExecutorModel 在任何工具启动前验证整个调用集合，避免部分非法调用产生副作用。
type peExecutorModel struct {
	inner   model.ToolCallingChatModel
	runtime *peRuntime
}

func (m *peExecutorModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	inner, err := m.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &peExecutorModel{inner: inner, runtime: m.runtime}, nil
}
func (m *peExecutorModel) Generate(ctx context.Context, msgs []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	msg, err := m.inner.Generate(ctx, msgs, opts...)
	if err != nil {
		return nil, err
	}
	return msg, m.validate(ctx, msg)
}
func (m *peExecutorModel) Stream(ctx context.Context, msgs []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	stream, err := m.inner.Stream(ctx, msgs, opts...)
	if err != nil {
		return nil, err
	}
	defer stream.Close()
	msg, err := schema.ConcatMessageStream(stream)
	if err != nil {
		return nil, err
	}
	if err = m.validate(ctx, msg); err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}
func (m *peExecutorModel) validate(ctx context.Context, msg *schema.Message) error {
	r := m.runtime
	r.mu.Lock()
	defer r.mu.Unlock()
	s, err := r.session(ctx)
	if err != nil {
		return err
	}
	if msg == nil {
		return fmt.Errorf("Executor 返回空消息")
	}
	remaining := map[string]TaskSpec{}
	for _, task := range s.Batch {
		result, ok := s.Results[task.ID]
		if !ok || result.Status == TaskWaiting {
			remaining[task.ID] = task
		}
	}
	if len(msg.ToolCalls) == 0 {
		if len(remaining) > 0 || strings.TrimSpace(msg.Content) == "" {
			return fmt.Errorf("Executor 未委派全部任务")
		}
		return nil
	}
	seen := map[string]bool{}
	for _, call := range msg.ToolCalls {
		var args struct {
			TaskID string `json:"task_id"`
		}
		if json.Unmarshal([]byte(call.Function.Arguments), &args) != nil {
			return fmt.Errorf("委派参数无效")
		}
		task, ok := remaining[args.TaskID]
		if !ok || task.Agent != call.Function.Name || seen[args.TaskID] {
			return fmt.Errorf("非法或重复任务委派: %s", args.TaskID)
		}
		seen[args.TaskID] = true
	}
	if len(seen) != len(remaining) {
		return fmt.Errorf("Executor 必须同轮委派当前批次全部任务")
	}
	return nil
}
