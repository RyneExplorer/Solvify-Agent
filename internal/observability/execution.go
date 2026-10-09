package observability

import (
	"context"
	"encoding/json"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// ExecutionMetadata 只携带可跨请求关联的业务标识，不保存模型、工具或执行上下文。
type ExecutionMetadata struct {
	PlanID       string
	CheckpointID string
	TaskID       string
	Phase        string
	PlanVersion  int
	Round        int
	Resumed      bool
}

type executionMetadataKey struct{}

func ExecutionMetadataFromContext(ctx context.Context) ExecutionMetadata {
	metadata, _ := ctx.Value(executionMetadataKey{}).(ExecutionMetadata)
	return metadata
}

// WithExecutionMetadata 使用值副本隔离同一批并行任务的标识，并补充当前原生 span。
func WithExecutionMetadata(ctx context.Context, metadata ExecutionMetadata) context.Context {
	ctx = context.WithValue(ctx, executionMetadataKey{}, metadata)
	annotateExecution(ctx)
	return ctx
}

func annotateExecution(ctx context.Context) {
	m := ExecutionMetadataFromContext(ctx)
	var attrs []attribute.KeyValue
	for key, value := range map[string]string{
		"plan_id": m.PlanID, "checkpoint_id": m.CheckpointID, "task_id": m.TaskID, "phase": m.Phase,
	} {
		if value != "" {
			attrs = append(attrs, attribute.String("langfuse.observation.metadata."+key, value))
		}
	}
	if m.PlanVersion > 0 {
		attrs = append(attrs, attribute.Int("langfuse.observation.metadata.plan_version", m.PlanVersion))
	}
	if m.Round > 0 {
		attrs = append(attrs, attribute.Int("langfuse.observation.metadata.execution_round", m.Round))
	}
	if m.CheckpointID != "" {
		attrs = append(attrs, attribute.Bool("langfuse.observation.metadata.resumed", m.Resumed))
	}
	trace.SpanFromContext(ctx).SetAttributes(attrs...)
}

// executionHandler 装饰官方 handler，不另建 span 或改变 Eino 回调/流的生命周期。
// 将标识写入官方 OnStart 返回的 span，避免依赖不同 handler 之间的调用顺序。
type executionHandler struct{ callbacks.Handler }

// WithExecutionTracing 为官方 callback 补充业务关联字段；仍由原 handler 管理导出和 span。
func WithExecutionTracing(handler callbacks.Handler) callbacks.Handler {
	return executionHandler{Handler: handler}
}

func (h executionHandler) Needed(ctx context.Context, info *callbacks.RunInfo, timing callbacks.CallbackTiming) bool {
	if checker, ok := h.Handler.(callbacks.TimingChecker); ok {
		return checker.Needed(ctx, info, timing)
	}
	return true
}

func (h executionHandler) OnStart(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
	if info != nil && info.Component == components.ComponentOfTool {
		if in := tool.ConvCallbackInput(input); in != nil {
			var args struct {
				TaskID string `json:"task_id"`
			}
			if json.Unmarshal([]byte(in.ArgumentsInJSON), &args) == nil && args.TaskID != "" {
				metadata := ExecutionMetadataFromContext(ctx)
				metadata.TaskID = args.TaskID
				// 此时仍是父 span，只传递上下文，不修改父任务标识。
				ctx = context.WithValue(ctx, executionMetadataKey{}, metadata)
			}
		}
	}
	ctx = h.Handler.OnStart(ctx, info, input)
	annotateExecution(ctx)
	return ctx
}

func (h executionHandler) OnStartWithStreamInput(ctx context.Context, info *callbacks.RunInfo, input *schema.StreamReader[callbacks.CallbackInput]) context.Context {
	ctx = h.Handler.OnStartWithStreamInput(ctx, info, input)
	annotateExecution(ctx)
	return ctx
}
