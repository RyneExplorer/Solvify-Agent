package agent

import (
	"context"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/callbacks"

	"solvify-agent/internal/observability"
)

func (r *peRuntime) traceContext(ctx context.Context, phase, taskID string) context.Context {
	metadata := observability.ExecutionMetadataFromContext(ctx)
	metadata.Phase, metadata.TaskID = phase, taskID
	r.mu.Lock()
	s, err := r.session(ctx)
	if _, present := adk.GetSessionValue(ctx, peSessionKey); !present {
		// 根请求上下文位于 Runner 会话之外；收尾时使用本次运行的可信状态。
		s, err = r.state, nil
	}
	if err == nil && s != nil {
		metadata.PlanID = s.Plan.ID
		metadata.PlanVersion = s.Plan.Version
		metadata.Round = s.Round
	}
	r.mu.Unlock()
	return observability.WithExecutionMetadata(ctx, metadata)
}

// startPEProfessionalTrace 为直接调用的专业 ChatModelAgent 接入 Eino 原生回调。
// AgentTool 已有独立 Runner；这里保留父会话/中断状态，只增加专业 Agent 的观测边界。
func startPEProfessionalTrace(ctx context.Context, name string, input *adk.AgentInput, resume *adk.ResumeInfo) (context.Context, func(TaskResult, error)) {
	ctx = callbacks.ReuseHandlers(ctx, &callbacks.RunInfo{Name: name + ".professional", Type: "ChatModelAgent", Component: adk.ComponentOfAgent})
	ctx = callbacks.OnStart(ctx, &adk.AgentCallbackInput{Input: input, ResumeInfo: resume})
	return ctx, func(result TaskResult, err error) {
		if err != nil {
			callbacks.OnError(ctx, err)
			return
		}
		// 使用结果值而非共享事件迭代器，多个 callback 可安全读取同一结构化输出。
		callbacks.OnEnd(ctx, result)
	}
}
