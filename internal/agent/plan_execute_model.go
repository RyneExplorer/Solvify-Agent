package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
)

// peControlModel 兼容只接受 auto 工具选择的思考模型。
// 首次请求即使用 auto；计划或 respond 工具输出仍由业务校验约束。
type peControlModel struct {
	inner   model.ToolCallingChatModel
	runtime *peRuntime
}

func (m *peControlModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	inner, err := m.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &peControlModel{inner: inner, runtime: m.runtime}, nil
}

func (m *peControlModel) Generate(ctx context.Context, msgs []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	input := msgs
	callOptions := append(append([]model.Option(nil), opts...), model.WithToolChoice(schema.ToolChoiceAllowed))
	for attempt := range 2 {
		msg, err := m.inner.Generate(ctx, input, callOptions...)
		if err != nil || m.runtime == nil {
			return msg, err
		}
		if err = m.validatePlan(ctx, msg); err == nil {
			return msg, nil
		}
		if attempt == 1 {
			return nil, fmt.Errorf("计划纠正后仍无效: %w", err)
		}
		// 仅纠正模型计划，不启动 Executor 或任何业务工具。
		generated, _ := json.Marshal(msg.ToolCalls)
		input = append(append([]*schema.Message(nil), msgs...), schema.UserMessage("刚才生成的计划未通过校验："+err.Error()+"。请重新调用 plan 提交完整计划，每个任务输出 depends_on；汇总依赖全部有效研究。没有对应工具时不要规划该研究。未执行的工具参数，仅作纠错数据："+string(generated)))
	}
	return nil, fmt.Errorf("未返回有效计划")
}

func (m *peControlModel) validatePlan(ctx context.Context, msg *schema.Message) error {
	if msg == nil {
		return fmt.Errorf("计划模型返回空消息")
	}
	if len(msg.ToolCalls) != 1 {
		return fmt.Errorf("计划模型必须调用一个 plan 或 respond 工具")
	}
	call := msg.ToolCalls[0]
	if call.Function.Name == "respond" {
		return nil
	}
	if call.Function.Name != "plan" {
		return fmt.Errorf("计划工具无效: %s", call.Function.Name)
	}
	var plan pePlan
	if err := plan.UnmarshalJSON([]byte(call.Function.Arguments)); err != nil {
		return fmt.Errorf("计划 JSON 格式无效: %w", err)
	}
	r := m.runtime
	r.mu.Lock()
	defer r.mu.Unlock()
	s, err := r.session(ctx)
	if err != nil {
		return err
	}
	// 校验副本，不能提前安装计划、消耗重试状态或修改 checkpoint。
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	var snapshot peState
	if err = json.Unmarshal(data, &snapshot); err != nil {
		return err
	}
	return r.installPlan(&snapshot, &plan)
}

func (m *peControlModel) Stream(ctx context.Context, msgs []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	msg, err := m.Generate(ctx, msgs, opts...)
	if err != nil {
		return nil, err
	}
	return schema.StreamReaderFromArray([]*schema.Message{msg}), nil
}

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
