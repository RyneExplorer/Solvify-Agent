package observability

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/embedding"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/retriever"
	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// einoSpanKey 把 span 和 startAt 绑定在单次回调的 context 链上（同一个 handler OnStart → OnEnd 内部传递）。
type einoSpanKey struct{}

type einoSpanState struct {
	startAt time.Time
	span    *Span
}

// ─── Component 映射 ────────────────────────────────────────────────────────────────────────────

func mapComponent(comp string) Component {
	switch comp {
	case string(components.ComponentOfChatModel), string(components.ComponentOfAgenticModel):
		return ComponentLLMClient
	case string(components.ComponentOfRetriever):
		return ComponentRAGRetriever
	case string(components.ComponentOfTool):
		return ComponentAgentTool
	case string(components.ComponentOfEmbedding):
		return ComponentLLMClient
	case string(adk.ComponentOfAgent), string(adk.ComponentOfAgenticAgent):
		return ComponentAgentEngine
	case "Graph", "Chain", "Workflow":
		return ComponentAgentEngine
	}
	return ComponentAgentEngine
}

func componentLabel(comp string) string {
	switch comp {
	case string(components.ComponentOfChatModel):
		return "chat_model"
	case string(components.ComponentOfAgenticModel):
		return "agentic_model"
	case string(components.ComponentOfRetriever):
		return "retriever"
	case string(components.ComponentOfTool):
		return "tool"
	case string(components.ComponentOfEmbedding):
		return "embedding"
	case string(adk.ComponentOfAgent), string(adk.ComponentOfAgenticAgent):
		return "agent"
	case "Graph":
		return "graph"
	case "Chain":
		return "chain"
	case "Workflow":
		return "workflow"
	}
	if comp == "" {
		return "unknown"
	}
	return strings.ToLower(comp)
}

// ─── 对外构造 / 注册 ───────────────────────────────────────────────────────────────────────────

// NewEinoCallbackHandler 创建一个 eino callbacks.Handler。
// 把 OnStart/OnEnd/OnError/Stream 回调桥接到 Recorder：自动打 span、统计组件耗时、
// 输出 LLM token、Retriever 命中数、Tool 调用数等通用指标。
//
// 流式输入/输出回调会主动关闭 StreamReader（读完丢弃），避免 tee 分支没人消费导致 goroutine 泄漏。
func NewEinoCallbackHandler(rec Recorder) callbacks.Handler {
	return callbacks.NewHandlerBuilder().
		OnStartFn(einoOnStart(rec)).
		OnEndFn(einoOnEnd(rec)).
		OnErrorFn(einoOnError(rec)).
		OnStartWithStreamInputFn(einoOnStreamStart).
		OnEndWithStreamOutputFn(einoOnStreamEnd).
		Build()
}

// RegisterGlobalEinoCallback 通过 AppendGlobalHandlers 注册为全局 callback。
// 应该在应用启动早期（NewApp 里 observability 初始化之后）调用一次。
//
// 不是线程安全，不要并发调用。
func RegisterGlobalEinoCallback(rec Recorder) {
	if rec == nil {
		return
	}
	callbacks.AppendGlobalHandlers(NewEinoCallbackHandler(rec))
}

// ─── OnStart ───────────────────────────────────────────────────────────────────────────────────

func einoOnStart(rec Recorder) func(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
	if rec == nil {
		return func(ctx context.Context, _ *callbacks.RunInfo, _ callbacks.CallbackInput) context.Context { return ctx }
	}
	return func(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
		if info == nil {
			return ctx
		}
		comp := mapComponent(string(info.Component))

		attrs := Attrs{
			"eino_name": info.Name,
			"eino_type": info.Type,
		}

		switch info.Component {
		case components.ComponentOfChatModel, components.ComponentOfAgenticModel:
			if mi := model.ConvCallbackInput(input); mi != nil {
				attrs["messages_n"] = len(mi.Messages)
				attrs["tools_n"] = len(mi.Tools)
				if mi.Config != nil {
					if mi.Config.Model != "" {
						attrs["model_id"] = mi.Config.Model
					}
					if mi.Config.Temperature != 0 {
						attrs["temperature"] = mi.Config.Temperature
					}
				}
			}
		case components.ComponentOfRetriever:
			if ri := retriever.ConvCallbackInput(input); ri != nil {
				attrs["top_k"] = ri.TopK
				if ri.ScoreThreshold != nil {
					attrs["score_threshold"] = *ri.ScoreThreshold
				}
			}
		case components.ComponentOfTool:
			if ti := tool.ConvCallbackInput(input); ti != nil {
				attrs["args_len"] = len(ti.ArgumentsInJSON)
			}
		case components.ComponentOfEmbedding:
			if ei := embedding.ConvCallbackInput(input); ei != nil {
				attrs["texts_n"] = len(ei.Texts)
				if ei.Config != nil && ei.Config.Model != "" {
					attrs["model_id"] = ei.Config.Model
				}
			}
		}

		startAt := time.Now()
		spanName := info.Name
		if spanName == "" {
			spanName = "eino." + componentLabel(string(info.Component))
		}
		_, span := rec.StartSpan(ctx, spanName, comp, attrs)
		state := &einoSpanState{startAt: startAt, span: span}
		return context.WithValue(ctx, einoSpanKey{}, state)
	}
}

// ─── OnEnd ─────────────────────────────────────────────────────────────────────────────────────

func einoOnEnd(rec Recorder) func(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
	if rec == nil {
		return func(ctx context.Context, _ *callbacks.RunInfo, _ callbacks.CallbackOutput) context.Context { return ctx }
	}
	return func(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
		if info == nil {
			return ctx
		}
		state, _ := ctx.Value(einoSpanKey{}).(*einoSpanState)
		if state == nil {
			state = &einoSpanState{startAt: time.Now()}
		}
		dur := time.Since(state.startAt)
		attrs := Attrs{}
		labels := map[string]string{
			"component": componentLabel(string(info.Component)),
		}
		if info.Name != "" {
			labels["name"] = info.Name
		}

		switch info.Component {
		case components.ComponentOfChatModel, components.ComponentOfAgenticModel:
			if mo := model.ConvCallbackOutput(output); mo != nil {
				if mo.Message != nil {
					attrs["has_tool_calls"] = len(mo.Message.ToolCalls) > 0
					attrs["role"] = string(mo.Message.Role)
				}
				var (
					prompt, completion, total int
					modelID                   string
				)
				if mo.TokenUsage != nil {
					prompt = mo.TokenUsage.PromptTokens
					completion = mo.TokenUsage.CompletionTokens
					total = mo.TokenUsage.TotalTokens
					attrs["prompt_tokens"] = prompt
					attrs["completion_tokens"] = completion
					attrs["total_tokens"] = total
				}
				if mo.Config != nil {
					modelID = mo.Config.Model
				}
				llmLabels := cloneLabels(labels)
				if modelID != "" {
					llmLabels["model_id"] = modelID
					attrs["model_id"] = modelID
				}
				rec.Incr(ctx, "eino_llm_requests_total", llmLabels, 1)
				rec.Observe(ctx, "eino_llm_duration_seconds", llmLabels, dur.Seconds())
				if total > 0 {
					rec.Observe(ctx, "eino_llm_total_tokens", llmLabels, float64(total))
				}
				if prompt > 0 {
					rec.Observe(ctx, "eino_llm_prompt_tokens", llmLabels, float64(prompt))
				}
				if completion > 0 {
					rec.Observe(ctx, "eino_llm_completion_tokens", llmLabels, float64(completion))
				}
				// 兼容旧指标名
				rec.Incr(ctx, "eino_llm_stream_requests_total", llmLabels, 1)
			}
		case components.ComponentOfRetriever:
			if ro := retriever.ConvCallbackOutput(output); ro != nil {
				hitN := len(ro.Docs)
				attrs["hit_n"] = hitN
				if hitN > 0 {
					var sumScore float64
					for _, d := range ro.Docs {
						sumScore += d.Score()
					}
					attrs["avg_score"] = sumScore / float64(hitN)
				}
				rl := cloneLabels(labels)
				rec.Incr(ctx, "eino_retriever_requests_total", rl, 1)
				rec.Observe(ctx, "eino_retriever_duration_seconds", rl, dur.Seconds())
				rec.Observe(ctx, "eino_retriever_hit_count", rl, float64(hitN))
			}
		case components.ComponentOfTool:
			if to := tool.ConvCallbackOutput(output); to != nil {
				attrs["response_len"] = len(to.Response)
				if to.ToolOutput != nil {
					attrs["tool_output_parts_n"] = len(to.ToolOutput.Parts)
				}
			}
			tl := cloneLabels(labels)
			if info.Name != "" {
				tl["tool_name"] = info.Name
			}
			rec.Incr(ctx, "eino_tool_calls_total", tl, 1)
			rec.Observe(ctx, "eino_tool_duration_seconds", tl, dur.Seconds())
			// 兼容旧指标名 agent_tool_calls_total：旧仪表盘用到了 tool + status 标签
			oldLabels := map[string]string{"status": "success"}
			if info.Name != "" {
				oldLabels["tool"] = info.Name
			}
			rec.Incr(ctx, "agent_tool_calls_total", oldLabels, 1)
		case components.ComponentOfEmbedding:
			if eo := embedding.ConvCallbackOutput(output); eo != nil {
				attrs["embeddings_n"] = len(eo.Embeddings)
				var (
					prompt, total int
					modelID       string
				)
				if eo.TokenUsage != nil {
					prompt = eo.TokenUsage.PromptTokens
					total = eo.TokenUsage.TotalTokens
					attrs["prompt_tokens"] = prompt
					attrs["total_tokens"] = total
				}
				if eo.Config != nil {
					modelID = eo.Config.Model
					if modelID != "" {
						attrs["model_id"] = modelID
					}
				}
				el := cloneLabels(labels)
				if modelID != "" {
					el["model_id"] = modelID
				}
				rec.Incr(ctx, "eino_embed_requests_total", el, 1)
				rec.Observe(ctx, "eino_embed_duration_seconds", el, dur.Seconds())
				if total > 0 {
					rec.Observe(ctx, "eino_embed_total_tokens", el, float64(total))
				}
				if prompt > 0 {
					rec.Observe(ctx, "eino_embed_prompt_tokens", el, float64(prompt))
				}
			}
		case adk.ComponentOfAgent, adk.ComponentOfAgenticAgent, "Graph", "Chain", "Workflow":
			al := cloneLabels(labels)
			rec.Incr(ctx, "eino_agent_runs_total", al, 1)
			rec.Observe(ctx, "eino_agent_duration_seconds", al, dur.Seconds())
		}

		if state.span != nil {
			rec.EndSpan(ctx, state.span, SpanStatusOK, nil, attrs)
		}
		return ctx
	}
}

// ─── OnError ───────────────────────────────────────────────────────────────────────────────────

func einoOnError(rec Recorder) func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
	if rec == nil {
		return func(ctx context.Context, _ *callbacks.RunInfo, _ error) context.Context { return ctx }
	}
	return func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
		if info == nil || err == nil {
			return ctx
		}
		state, _ := ctx.Value(einoSpanKey{}).(*einoSpanState)
		if state == nil {
			state = &einoSpanState{startAt: time.Now()}
		}
		labels := map[string]string{
			"component": componentLabel(string(info.Component)),
		}
		if info.Name != "" {
			labels["name"] = info.Name
		}
		switch info.Component {
		case components.ComponentOfChatModel, components.ComponentOfAgenticModel:
			ll := cloneLabels(labels)
			rec.Incr(ctx, "eino_llm_errors_total", ll, 1)
		case components.ComponentOfRetriever:
			rl := cloneLabels(labels)
			rec.Incr(ctx, "eino_retriever_errors_total", rl, 1)
		case components.ComponentOfTool:
			tl := cloneLabels(labels)
			if info.Name != "" {
				tl["tool_name"] = info.Name
			}
			rec.Incr(ctx, "eino_tool_errors_total", tl, 1)
			// 兼容旧指标名 agent_tool_calls_total tool + status=error
			oldLabels := map[string]string{"status": "error"}
			if info.Name != "" {
				oldLabels["tool"] = info.Name
			}
			rec.Incr(ctx, "agent_tool_calls_total", oldLabels, 1)
		case components.ComponentOfEmbedding:
			el := cloneLabels(labels)
			rec.Incr(ctx, "eino_embed_errors_total", el, 1)
		case adk.ComponentOfAgent, adk.ComponentOfAgenticAgent, "Graph", "Chain", "Workflow":
			al := cloneLabels(labels)
			rec.Incr(ctx, "eino_agent_errors_total", al, 1)
		}

		if state.span != nil {
			rec.EndSpan(ctx, state.span, SpanStatusError, err, Attrs{
				"eino_name": info.Name,
				"eino_type": info.Type,
			})
		}
		return ctx
	}
}

// ─── Stream 回调：只负责消费并关闭 StreamReader，避免 goroutine 泄漏 ────────────────────

func einoOnStreamStart(ctx context.Context, info *callbacks.RunInfo, input *schema.StreamReader[callbacks.CallbackInput]) context.Context {
	if input != nil {
		go safeDrainAndCloseReader(input)
	}
	return ctx
}

func einoOnStreamEnd(ctx context.Context, info *callbacks.RunInfo, output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
	if output != nil {
		go safeDrainAndCloseReader(output)
	}
	return ctx
}

// ─── helpers ───────────────────────────────────────────────────────────────────────────────────

// safeDrainAndCloseReader 读完 StreamReader 直到 EOF/错误，再 Close，避免 tee 另一端阻塞。
// 做了幂等：同一个 reader 不会被重复 drain。
var closeOnce sync.Map

func safeDrainAndCloseReader[T any](r *schema.StreamReader[T]) {
	if r == nil {
		return
	}
	key := any(r)
	if _, loaded := closeOnce.LoadOrStore(key, struct{}{}); loaded {
		return
	}
	defer closeOnce.Delete(key)
	go func() {
		defer func() { _ = recover() }()
		for {
			_, err := r.Recv()
			if err != nil {
				r.Close()
				return
			}
		}
	}()
}

// 让 safeCloseReader（上面定义的旧名）也不报错，防止别的地方未来引用。
func safeCloseReader[T any](r *schema.StreamReader[T]) {
	safeDrainAndCloseReader(r)
}

func cloneLabels(src map[string]string) map[string]string {
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}
