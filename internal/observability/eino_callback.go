package observability

import (
	"context"
	"encoding/json"
	"strconv"
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
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/pkg/logger"
)

type compMapping struct {
	component Component
	label     string
}

// compMap 覆盖 eino 组件字符串的**全部三个来源**（v0.9.1 共 20 个）：
//   - components 包 10 个（components/types.go）
//   - compose 包 8 个（compose/types.go）
//   - adk 包 2 个（adk/interface.go）
//
// ⚠️ 键一律用 eino 导出的常量而不是字符串字面量：eino 改名时编译期就会报错，
// 不会退化成「运行期静默兜底」（历史上 Chain/Graph/Workflow 就是手写字的量，
// 而 ToolsNode / Lambda 因为没写进来直接落到兜底分支）。
var compMap = map[string]compMapping{
	// components 包
	string(components.ComponentOfPrompt):        {ComponentLLMClient, "chat_template"},
	string(components.ComponentOfAgenticPrompt): {ComponentLLMClient, "agentic_chat_template"},
	string(components.ComponentOfChatModel):     {ComponentLLMClient, "chat_model"},
	string(components.ComponentOfAgenticModel):  {ComponentLLMClient, "agentic_model"},
	string(components.ComponentOfEmbedding):     {ComponentLLMClient, "embedding"},
	string(components.ComponentOfIndexer):       {ComponentRAGIndexer, "indexer"},
	string(components.ComponentOfRetriever):     {ComponentRAGRetriever, "retriever"},
	string(components.ComponentOfLoader):        {ComponentRAGIndexer, "loader"},
	string(components.ComponentOfTransformer):   {ComponentRAGIndexer, "document_transformer"},
	string(components.ComponentOfTool):          {ComponentAgentTool, "tool"},
	// compose 包
	string(compose.ComponentOfUnknown):          {ComponentUnknown, "unknown"},
	string(compose.ComponentOfGraph):            {ComponentAgentEngine, "graph"},
	string(compose.ComponentOfWorkflow):         {ComponentAgentEngine, "workflow"},
	string(compose.ComponentOfChain):            {ComponentAgentEngine, "chain"},
	string(compose.ComponentOfPassthrough):      {ComponentAgentEngine, "passthrough"},
	string(compose.ComponentOfToolsNode):        {ComponentAgentTool, "tools_node"},
	string(compose.ComponentOfAgenticToolsNode): {ComponentAgentTool, "agentic_tools_node"},
	string(compose.ComponentOfLambda):           {ComponentAgentEngine, "lambda"},
	// adk 包
	string(adk.ComponentOfAgent):                {ComponentAgentEngine, "agent"},
	string(adk.ComponentOfAgenticAgent):         {ComponentAgentEngine, "agent"},
}

// unknownCompLogged 保证同一个未知组件名只告警一次。
// 用 sync.Map 而不是普通 map：流式路径的 eino 回调在各自 goroutine 里跑。
var unknownCompLogged sync.Map

// mapComponent 把 eino 组件字符串映射成 span 的 component 标签。
//
// ⚠️ 未知组件**不能**兜底成有业务含义的值：该标签会被前端原样渲染成
// 「组件：xxx」（design/vue/src/components/TraceSpanNode.vue），拿 agent.engine
// 兜底等于把「我不认识」伪装成「这是 Agent」。历史实测 24 个 span 里 10 个
// （41.7%）就是这么来的。现在兜底成中性的 ComponentUnknown，并留一条告警 ——
// eino 新增组件类型时必须能被发现，而不是静默混进某个已有分类。
func mapComponent(comp string) Component {
	if m, ok := compMap[comp]; ok {
		return m.component
	}
	if comp != "" {
		if _, loaded := unknownCompLogged.LoadOrStore(comp, struct{}{}); !loaded {
			logger.Warnf("eino 回调收到未映射的组件类型 %q，已按 unknown 记录；请补进 compMap", comp)
		}
	}
	return ComponentUnknown
}

func componentLabel(comp string) string {
	if m, ok := compMap[comp]; ok {
		return m.label
	}
	if comp == "" {
		return "unknown"
	}
	return strings.ToLower(comp)
}

// genAIOperationName 把 eino 组件类型映射成 gen_ai.operation.name 取值。
// 三方追踪平台靠这个属性判断 span 属于 LLM 调用、工具执行还是检索，必须显式给出。
// 返回空字符串表示该组件类型没有合适的 gen_ai 操作名，调用方跳过该属性。
func genAIOperationName(comp components.Component) string {
	switch comp {
	case components.ComponentOfChatModel, components.ComponentOfAgenticModel:
		return genAIOpChat
	case components.ComponentOfTool:
		return genAIOpExecuteTool
	case components.ComponentOfRetriever:
		return genAIOpRetrieval
	case components.ComponentOfEmbedding:
		return genAIOpEmbeddings
	case adk.ComponentOfAgent, adk.ComponentOfAgenticAgent:
		return genAIOpInvokeAgent
	// 编排类节点统统归 invoke_workflow：它们本身就是「一段流程」，
	// 在 gen_ai 语义规范里没有更细的对应操作名。
	case compose.ComponentOfGraph, compose.ComponentOfWorkflow, compose.ComponentOfChain,
		compose.ComponentOfLambda, compose.ComponentOfPassthrough:
		return genAIOpInvokeFlow
	// ToolsNode 的职责就是执行工具，给 execute_tool 才能让三方平台把它
	// 识别成 tool 卡片；留空会让这一层在平台上退化成无名节点。
	case compose.ComponentOfToolsNode, compose.ComponentOfAgenticToolsNode:
		return genAIOpExecuteTool
	}
	// 其余（ChatTemplate / Indexer / Loader / DocumentTransformer / Unknown）
	// 在 gen_ai 语义规范里没有对应操作名，刻意返回空 —— 硬套一个反而是错标。
	return ""
}

// agentIterKey 承载「本次 agent run 的模型调用轮次计数器」。
type agentIterKey struct{}

// agentIterCounter 跟着 ctx 在**同一个 agent run** 内递增，给 ChatModel span 提供
// 「这是第几轮」。
//
// 为什么不加锁：ADK 的 ReAct 循环是串行的（同一时刻只有一个 ChatModel 在跑），
// 同一个 run 内不存在并发递增。
type agentIterCounter struct{ n int }

// nextIteration 递增并返回本轮轮次（从 1 开始）。
// ctx 里没有计数器时返回 0 —— 表示这次 ChatModel 调用不在任何 Agent 之下
// （例如被当成独立组件直接调用），此时不给轮次属性。
func nextIteration(ctx context.Context) int {
	c, _ := ctx.Value(agentIterKey{}).(*agentIterCounter)
	if c == nil {
		return 0
	}
	c.n++
	return c.n
}

type einoSpanKey struct{}

// einoSpanState 跨 OnStart→OnEnd/OnError/OnStreamEnd 传递 span 引用。
type einoSpanState struct {
	startAt time.Time
	span    *Span
}

func stateFromCtx(ctx context.Context) *einoSpanState {
	s, _ := ctx.Value(einoSpanKey{}).(*einoSpanState)
	if s == nil {
		s = &einoSpanState{startAt: time.Now()}
	}
	return s
}

// NewEinoCallbackHandler 创建 eino callbacks.Handler，桥接到 Recorder。
func NewEinoCallbackHandler(rec Recorder) callbacks.Handler {
	return callbacks.NewHandlerBuilder().
		OnStartFn(einoOnStart(rec)).
		OnEndFn(einoOnEnd(rec)).
		OnErrorFn(einoOnError(rec)).
		OnStartWithStreamInputFn(einoOnStreamStart(rec)).
		OnEndWithStreamOutputFn(einoOnStreamEnd).
		Build()
}

// RegisterGlobalEinoCallback 通过 AppendGlobalHandlers 注册为全局 callback。启动早期调一次。
func RegisterGlobalEinoCallback(rec Recorder) {
	if rec == nil {
		return
	}
	callbacks.AppendGlobalHandlers(NewEinoCallbackHandler(rec))
}

func einoOnStart(rec Recorder) func(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
	if rec == nil {
		return func(ctx context.Context, _ *callbacks.RunInfo, _ callbacks.CallbackInput) context.Context { return ctx }
	}
	return func(ctx context.Context, info *callbacks.RunInfo, input callbacks.CallbackInput) context.Context {
		if info == nil {
			return ctx
		}
		return beginEinoSpan(ctx, rec, info, input, nil)
	}
}

// beginEinoSpan 是「OnStart」与「OnStartWithStreamInput」两条 timing 的**唯一**建 span 入口。
//
// ⚠️ 为什么必须收口在一个函数里：eino 这两条 timing 是**互斥**的，不是先后关系
// （compose/utils.go:120-126 按「入参是不是 *schema.StreamReader」二选一）。
// 历史上只有 OnStart 建 span，于是走流式输入的组件（Collect 范式、流式 Lambda、
// 以流为入参的子图）在追踪里整段没有节点；后果不止是「少一个 span」——它的
// OnEnd 会取到**上一层**留在 state 里的 span 引用，把子节点自己的身份属性
// （eino_comp / gen_ai.operation.name）写到父 span 上，父 span 被冒名。
//
// extra 用于标注该 span 走的是哪条 timing（如流式输入），避免两条路径再次分叉。
func beginEinoSpan(ctx context.Context, rec Recorder, info *callbacks.RunInfo, input callbacks.CallbackInput, extra Attrs) context.Context {
	comp := mapComponent(string(info.Component))
	attrs := Attrs{
		"eino_name": info.Name,
		"eino_type": info.Type,
		// 原始组件字符串必须无条件落盘：mapComponent 会把多个 eino 组件
		// 折叠成同一个业务 component（如 Graph/Chain/Lambda 都进 agent.engine），
		// 没有它就无法反查这个 span 究竟是哪一类节点。
		"eino_comp": string(info.Component),
	}
	for k, v := range extra {
		attrs[k] = v
	}
	if op := genAIOperationName(info.Component); op != "" {
		attrs[AttrGenAIOperationName] = op
	}
	// 按 component 提取细粒度 attrs
	switch info.Component {
	case components.ComponentOfChatModel, components.ComponentOfAgenticModel:
		mergeChatModelStartAttrs(attrs, input, rec)
		// 「第几轮」：计数器由 Agent 级 OnStart 放进 ctx，再经 eino 的组件链传到这里。
		// 没有它就只能靠「同一层里若干个同名 ChatModel span 排排坐」去数轮数，
		// 多轮自纠 / 重试无法复盘（框架不提供 per-iteration 的轮次边界回调）。
		if n := nextIteration(ctx); n > 0 {
			attrs["agent.iteration"] = n
		}
	case components.ComponentOfRetriever:
		mergeRetrieverStartAttrs(attrs, input, rec)
	case components.ComponentOfTool:
		mergeToolStartAttrs(attrs, input, info, rec)
	case components.ComponentOfEmbedding:
		mergeEmbeddingStartAttrs(attrs, input, rec)
	case adk.ComponentOfAgent, adk.ComponentOfAgenticAgent:
		mergeAgentStartAttrs(attrs, input)
	}

	spanName := info.Name
	if spanName == "" {
		spanName = "eino." + componentLabel(string(info.Component))
	}
	// 挂树/OTel parent 由 StartSpan 内部从 ctx 的 currentSpanKey 取，自研轨与
	// OTel 轨的父子关系因此天然一致（见 recorder.go 的 StartSpan 注释）。
	ctxWithSpan, span := rec.StartSpan(ctx, spanName, comp, attrs)
	ctxWithSpan = context.WithValue(ctxWithSpan, einoSpanKey{}, &einoSpanState{
		startAt: time.Now(),
		span:    span,
	})
	if info.Component == adk.ComponentOfAgent || info.Component == adk.ComponentOfAgenticAgent {
		// 为这一跑挂一个轮次计数器，跟着 ctx 往下传给 ChatModel。
		// 刻意**不**新建 iteration span：那一层的 parent 插不进 eino 的节点树
		// （ADK 把 ReAct 循环编译成 Chain+Graph，模型节点挂在 chain span 之下），
		// 硬建只能得到与 ChatModel 平级的轮次节点，层级图反而更乱。
		// 轮次落表侧见 internal/agent/agent_step_tracker.go。
		ctxWithSpan = context.WithValue(ctxWithSpan, agentIterKey{}, &agentIterCounter{})
	}
	return ctxWithSpan
}

// mergeAgentStartAttrs 记录「这次是全新执行还是从中断处恢复」。
//
// 判据是 AgentCallbackInput.ResumeInfo（adk/flow.go 的 Resume 分支才会填）：
// 不读它的话，追踪上完全分不清「首次执行」和「恢复执行」—— 而项目里
// runner.ResumeWithParams 是真实用到的（见 internal/agent/runner_adapter.go）。
//
// ⚠️ 这个信息**不需要**消费 AgentCallbackOutput.Events 就能拿到：
// ResumeInfo 在 OnStart 的 input 上，而 Events 是 OnEnd 的 output。
func mergeAgentStartAttrs(attrs Attrs, input callbacks.CallbackInput) {
	ai := adk.ConvAgentCallbackInput(input)
	if ai == nil {
		return
	}
	if ai.ResumeInfo == nil {
		attrs["agent.resumed"] = false
		return
	}
	attrs["agent.resumed"] = true
	attrs["agent.was_interrupted"] = ai.ResumeInfo.WasInterrupted
	attrs["agent.is_resume_target"] = ai.ResumeInfo.IsResumeTarget
}

func mergeChatModelStartAttrs(attrs Attrs, input callbacks.CallbackInput, rec Recorder) {
	mi := model.ConvCallbackInput(input)
	if mi == nil {
		return
	}
	attrs["messages_n"] = len(mi.Messages)
	attrs["tools_n"] = len(mi.Tools)
	if toolNames := collectToolNames(mi.Tools); len(toolNames) > 0 {
		// 不再折叠成 "(+N more)"：工具清单是判断「模型为何选错 / 选不到工具」的
		// 直接依据，折叠会让 21 个工具里的 18 个不可见。
		attrs["tools_list"] = rec.PreviewAttr(strings.Join(toolNames, ", "), contentLenMedium)
	}
	// 完整输入消息：三方平台靠 gen_ai.input.messages 渲染 Generation 卡片的 Input，
	// 本项目前端读同一份（span_tree.attrs），不再另存一份自研命名的副本。
	if payload := buildInputMessagesPayload(mi.Messages, rec); payload != "" {
		attrs[AttrGenAIInputMessages] = payload
	}
	if last := lastMessageByRole(mi.Messages, "user"); last != nil {
		attrs["last_user_msg_preview"] = rec.PreviewAttr(last.Content, contentLenMedium)
	}
	if first := firstMessageByRole(mi.Messages, "system"); first != nil {
		attrs["system_prompt_preview"] = rec.PreviewAttr(first.Content, contentLenMedium)
	}
	if mi.Config != nil {
		if mi.Config.Model != "" {
			attrs["model_id"] = mi.Config.Model
			attrs[AttrGenAIRequestModel] = mi.Config.Model
			if provider := genAIProviderFor(mi.Config.Model); provider != "" {
				attrs[AttrGenAIProviderName] = provider
			}
		}
		if mi.Config.Temperature != 0 {
			attrs["temperature"] = mi.Config.Temperature
			attrs[AttrGenAIRequestTemperature] = mi.Config.Temperature
		}
		if mi.Config.MaxTokens > 0 {
			attrs["max_tokens"] = mi.Config.MaxTokens
			attrs[AttrGenAIRequestMaxTokens] = mi.Config.MaxTokens
		}
		if mi.Config.TopP > 0 {
			attrs["top_p"] = mi.Config.TopP
			attrs[AttrGenAIRequestTopP] = mi.Config.TopP
		}
		if len(mi.Config.Stop) > 0 {
			attrs["stop"] = rec.PreviewAttr(joinShortList(mi.Config.Stop, 5), contentLenShort)
			attrs[AttrGenAIRequestStopSequences] = mi.Config.Stop
		}
	}
	attrs["role_counter"] = countMessageRoles(mi.Messages)
}

func mergeRetrieverStartAttrs(attrs Attrs, input callbacks.CallbackInput, rec Recorder) {
	ri := retriever.ConvCallbackInput(input)
	if ri == nil {
		ri = &retriever.CallbackInput{}
	}
	// Graph 包装的 CallbackInput Conv 后 TopK=0，真实 attrs 在 EinoRetrieverAdapter.Retrieve 补
	if ri.TopK > 0 {
		attrs["top_k"] = ri.TopK
	}
	if ri.ScoreThreshold != nil {
		attrs["score_threshold"] = *ri.ScoreThreshold
	}
	if ri.Query != "" {
		// gen_ai.retrieval.query.text 让三方平台把 span 渲染成「检索」卡片并显示检索词
		query := rec.PreviewAttr(ri.Query, contentLenMedium)
		attrs["query"] = query
		attrs[AttrGenAIRetrievalQueryText] = query
	}
	if ri.Filter != "" {
		attrs["filter"] = rec.PreviewAttr(ri.Filter, contentLenShort)
	}
}

func mergeToolStartAttrs(attrs Attrs, input callbacks.CallbackInput, info *callbacks.RunInfo, rec Recorder) {
	// eino 的工具名在 RunInfo.Name 上（工具作为节点执行时就是工具名），
	// gen_ai.tool.name 是三方平台渲染工具卡片的必需属性。
	if info != nil && info.Name != "" {
		attrs[AttrGenAIToolName] = info.Name
	}
	attrs[AttrGenAIToolType] = "function"

	ti := tool.ConvCallbackInput(input)
	if ti == nil {
		return
	}
	attrs["args_len"] = len(ti.ArgumentsInJSON)
	if ti.ArgumentsInJSON != "" {
		preview := rec.PreviewAttr(ti.ArgumentsInJSON, contentLenMedium)
		attrs["args_preview"] = preview
		attrs[AttrGenAIToolCallArguments] = preview
	}
}

func mergeEmbeddingStartAttrs(attrs Attrs, input callbacks.CallbackInput, rec Recorder) {
	ei := embedding.ConvCallbackInput(input)
	if ei == nil {
		return
	}
	attrs["texts_n"] = len(ei.Texts)
	if ei.Config != nil && ei.Config.Model != "" {
		attrs["model_id"] = ei.Config.Model
		attrs[AttrGenAIRequestModel] = ei.Config.Model
		if provider := genAIProviderFor(ei.Config.Model); provider != "" {
			attrs[AttrGenAIProviderName] = provider
		}
	}
	if len(ei.Texts) > 0 {
		attrs["first_text_preview"] = rec.PreviewAttr(ei.Texts[0], contentLenMedium)
	}
}

func einoOnEnd(rec Recorder) func(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
	if rec == nil {
		return func(ctx context.Context, _ *callbacks.RunInfo, _ callbacks.CallbackOutput) context.Context { return ctx }
	}
	return func(ctx context.Context, info *callbacks.RunInfo, output callbacks.CallbackOutput) context.Context {
		if info == nil {
			return ctx
		}
		state := stateFromCtx(ctx)
		dur := time.Since(state.startAt)
		attrs := Attrs{}
		baseLabels := baseMetricLabels(info)

		switch info.Component {
		case components.ComponentOfChatModel, components.ComponentOfAgenticModel:
			llmLabels := mergeChatModelEndAttrs(attrs, output, rec, baseLabels)
			rec.Incr(ctx, "eino_llm_requests_total", llmLabels, 1)
			rec.Observe(ctx, "eino_llm_duration_seconds", llmLabels, dur.Seconds())
			observeLLMTokens(rec, llmLabels, attrs)
			rec.Incr(ctx, "eino_llm_stream_requests_total", llmLabels, 1)
		case components.ComponentOfRetriever:
			rl := mergeRetrieverEndAttrs(attrs, output, rec, baseLabels)
			hitN := 0
			if v, ok := attrs["hit_n"].(int); ok {
				hitN = v
			}
			rec.Incr(ctx, "eino_retriever_requests_total", rl, 1)
			rec.Observe(ctx, "eino_retriever_duration_seconds", rl, dur.Seconds())
			rec.Observe(ctx, "eino_retriever_hit_count", rl, float64(hitN))
			if hitN == 0 {
				rec.Incr(ctx, "eino_retriever_empty_results_total", rl, 1)
			}
		case components.ComponentOfTool:
			tl := mergeToolEndAttrs(attrs, output, info, rec, baseLabels)
			rec.Incr(ctx, "eino_tool_calls_total", tl, 1)
			rec.Observe(ctx, "eino_tool_duration_seconds", tl, dur.Seconds())
			rec.Incr(ctx, "agent_tool_calls_total", agentToolCallLabels(info.Name, "success"), 1)
		case components.ComponentOfEmbedding:
			el := mergeEmbeddingEndAttrs(attrs, output, baseLabels)
			rec.Incr(ctx, "eino_embed_requests_total", el, 1)
			rec.Observe(ctx, "eino_embed_duration_seconds", el, dur.Seconds())
			if total, ok := attrs["total_tokens"].(int); ok && total > 0 {
				rec.Observe(ctx, "eino_embed_total_tokens", el, float64(total))
			}
			if prompt, ok := attrs["prompt_tokens"].(int); ok && prompt > 0 {
				rec.Observe(ctx, "eino_embed_prompt_tokens", el, float64(prompt))
			}
		case adk.ComponentOfAgent, adk.ComponentOfAgenticAgent,
			compose.ComponentOfGraph, compose.ComponentOfWorkflow, compose.ComponentOfChain,
			compose.ComponentOfLambda, compose.ComponentOfPassthrough:
			rec.Incr(ctx, "eino_agent_runs_total", baseLabels, 1)
			rec.Observe(ctx, "eino_agent_duration_seconds", baseLabels, dur.Seconds())
			consumeAgentEventsAsync(rec, ctx, output)
		}

		endSpanIfPresent(rec, ctx, state, attrs, SpanStatusOK, nil)
		// End 后把 ctx 的当前 span 恢复为 parent：eino 会把 OnEnd 返回的 ctx 继续传给
		// 下一个兄弟节点，不恢复的话兄弟会错误地挂到这个已 End 的组件下面（链状嵌套）。
		if state.span != nil && state.span.parent != nil {
			ctx = context.WithValue(ctx, currentSpanKey{}, state.span.parent)
		}
		return ctx
	}
}

// consumeAgentEventsAsync 异步读完 ADK 的 AgentEvent 事件流，统计事件数与中断数。
//
// ⚠️ 必须异步 —— eino 的明确要求（adk/callback.go:39-43 注释原文：
// "The Events iterator should be consumed asynchronously to avoid blocking the
// agent execution"）。同步 drain 会把 agent 收尾卡住。
//
// 为什么必须消费、不能放着不管：eino 为**每个 handler** 复制一份独立的事件迭代器
// （adk/callback.go 的 copyTypedEventIterator），fan-out 走 internal.NewUnboundedChan
// —— 无界、Send 不阻塞。没人消费时事件会一直缓冲在内存里，既不产生任何观测数据，
// 也不释放。消费掉是净收益：拿到「这一跑产生多少事件、中断了几次」，
// 同时让副本尽快退场。
//
// 落点是 metrics 而不是 span attrs：异步消费完成时 span 早已 End，
// attrs 写不进去（OTel span End 之后 SetAttributes 无效）。
func consumeAgentEventsAsync(rec Recorder, ctx context.Context, output callbacks.CallbackOutput) {
	ao := adk.ConvAgentCallbackOutput(output)
	if ao == nil || ao.Events == nil {
		return
	}
	// 用 WithoutCancel 兜住：agent 收尾时入参 ctx 可能已被取消，
	// 但计数本身与请求生命周期无关（跟取消走会漏掉最后一次统计）。
	statCtx := context.WithoutCancel(ctx)
	go func() {
		defer func() { _ = recover() }()
		var events, interrupts int64
		for {
			ev, ok := ao.Events.Next()
			if !ok {
				break
			}
			events++
			if ev != nil && ev.Action != nil && ev.Action.Interrupted != nil {
				interrupts++
			}
		}
		if events > 0 {
			rec.Incr(statCtx, "eino_agent_events_total", nil, events)
		}
		if interrupts > 0 {
			rec.Incr(statCtx, "eino_agent_interrupts_total", nil, interrupts)
		}
	}()
}

// baseMetricLabels 提取所有组件通用的 component + name 标签
func baseMetricLabels(info *callbacks.RunInfo) map[string]string {
	labels := map[string]string{"component": componentLabel(string(info.Component))}
	if info.Name != "" {
		labels["name"] = info.Name
	}
	return labels
}

func mergeChatModelEndAttrs(attrs Attrs, output callbacks.CallbackOutput, rec Recorder, baseLabels map[string]string) map[string]string {
	mo := model.ConvCallbackOutput(output)
	if mo == nil || mo.Message == nil {
		return cloneLabels(baseLabels)
	}
	attrs["has_tool_calls"] = len(mo.Message.ToolCalls) > 0
	attrs["role"] = string(mo.Message.Role)
	if mo.Message.ResponseMeta != nil && mo.Message.ResponseMeta.FinishReason != "" {
		attrs[AttrGenAIResponseFinishReasons] = []string{mo.Message.ResponseMeta.FinishReason}
	}
	if mo.Message.Content != "" {
		attrs["reply_preview"] = rec.PreviewAttr(mo.Message.Content, contentLenLong)
		attrs[AttrGenAIOutputMessages] = buildOutputMessagesPayload(mo.Message.Content, rec)
	}
	if len(mo.Message.ToolCalls) > 0 {
		attrs["tool_calls_list"] = rec.PreviewAttr(
			joinShortList(extractToolCallNames(mo.Message.ToolCalls), 5), 200,
		)
	}
	modelID := ""
	if mo.TokenUsage != nil {
		mergeTokenUsageAttrs(attrs, mo.TokenUsage)
	}
	if mo.Config != nil {
		modelID = mo.Config.Model
	}
	llmLabels := cloneLabels(baseLabels)
	if modelID != "" {
		llmLabels["model_id"] = modelID
		attrs["model_id"] = modelID
		attrs[AttrGenAIResponseModel] = modelID
	}
	return llmLabels
}

// buildInputMessagesPayload 把模型输入序列化成 semconv 的 messages 数组 JSON 字符串。
//
// 格式（gen_ai.input.messages）：
//
//	[{"role":"user","parts":[{"type":"text","content":"..."}]}]
//
// 为什么必须带工具调用：eino 的 ReAct 循环里，模型看到的不只是文本 ——
// assistant 的 tool_call 与 tool 的返回才是「模型为什么这么答」的关键上下文。
// 只留文本等于把最需要排查的那部分抹掉。
//
// 两条长度控制同时生效：单条消息限 contentLenMedium（防一条超长消息吃掉整个预算），
// 整体限 contentLenFull（防多轮工具结果叠加后把 span_tree 撑爆）。
func buildInputMessagesPayload(msgs []*schema.Message, rec Recorder) string {
	if len(msgs) == 0 {
		return ""
	}
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		if m == nil {
			continue
		}
		parts := make([]map[string]any, 0, 1+len(m.ToolCalls))
		if strings.TrimSpace(m.Content) != "" {
			parts = append(parts, map[string]any{
				"type":    "text",
				"content": rec.PreviewAttr(m.Content, contentLenMedium),
			})
		}
		for _, tc := range m.ToolCalls {
			parts = append(parts, map[string]any{
				"type":      "tool_call",
				"id":        tc.ID,
				"name":      tc.Function.Name,
				"arguments": json.RawMessage(orEmptyJSON(tc.Function.Arguments)),
			})
		}
		if len(parts) == 0 {
			continue
		}
		out = append(out, map[string]any{
			"role":  string(m.Role),
			"parts": parts,
		})
	}
	if len(out) == 0 {
		return ""
	}
	b, err := json.Marshal(out)
	if err != nil {
		return ""
	}
	return rec.PreviewAttr(string(b), contentLenFull)
}

// buildOutputMessagesPayload 把模型输出的正文序列化成 semconv 的 messages 数组 JSON，
// 供三方平台渲染 Generation 卡片的 Output。
//
// 非流式（mergeChatModelEndAttrs）与流式（einoOnStreamEnd）两条路径共用同一个构造函数，
// 避免两处各写一份格式、慢慢长歪（这是「两个来源」最常见的产生方式）。
func buildOutputMessagesPayload(text string, rec Recorder) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}
	b, err := json.Marshal([]map[string]any{{
		"role": "assistant",
		"parts": []map[string]any{{
			"type":    "text",
			"content": rec.PreviewAttr(text, contentLenLong),
		}},
	}})
	if err != nil {
		return ""
	}
	return string(b)
}

// orEmptyJSON 保证嵌进 JSON 的工具参数是合法 JSON 片段：非法时退化成字符串字面量。
// 直接用 json.RawMessage 装非法 JSON 会让一次 json.Marshal 整体失败，
// 结果是整份 messages 全部丢失 —— 那比丢掉一个参数更糟。
func orEmptyJSON(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "{}"
	}
	if json.Valid([]byte(s)) {
		return s
	}
	b, err := json.Marshal(s)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// mergeTokenUsageAttrs 把 token 用量同时写进本项目 attrs 和 gen_ai 语义约定 attrs。
//
// 两套名字并存的原因：本项目前端读的是 prompt_tokens / completion_tokens（chat_traces.span_tree），
// 三方追踪平台读的是 gen_ai.usage.input_tokens / output_tokens，都不能省。
// 入参 usage 为 nil 时不做任何事（部分模型实现不返回用量）。
func mergeTokenUsageAttrs(attrs Attrs, usage *model.TokenUsage) {
	if usage == nil {
		return
	}
	attrs["prompt_tokens"] = usage.PromptTokens
	attrs["completion_tokens"] = usage.CompletionTokens
	attrs["total_tokens"] = usage.TotalTokens
	attrs[AttrGenAIUsageInputTokens] = usage.PromptTokens
	attrs[AttrGenAIUsageOutputTokens] = usage.CompletionTokens
	if usage.PromptTokenDetails.CachedTokens > 0 {
		attrs["cached_tokens"] = usage.PromptTokenDetails.CachedTokens
		attrs[AttrGenAIUsageCacheReadTokens] = usage.PromptTokenDetails.CachedTokens
	}
	if usage.CompletionTokensDetails.ReasoningTokens > 0 {
		attrs["reasoning_tokens"] = usage.CompletionTokensDetails.ReasoningTokens
		attrs[AttrGenAIUsageReasoningTokens] = usage.CompletionTokensDetails.ReasoningTokens
	}
}

// observeLLMTokens 从 attrs 取 token 用量并观察对应 histogram
func observeLLMTokens(rec Recorder, labels map[string]string, attrs Attrs) {
	if total, ok := attrs["total_tokens"].(int); ok && total > 0 {
		rec.Observe(context.Background(), "eino_llm_total_tokens", labels, float64(total))
	}
	if prompt, ok := attrs["prompt_tokens"].(int); ok && prompt > 0 {
		rec.Observe(context.Background(), "eino_llm_prompt_tokens", labels, float64(prompt))
	}
	if completion, ok := attrs["completion_tokens"].(int); ok && completion > 0 {
		rec.Observe(context.Background(), "eino_llm_completion_tokens", labels, float64(completion))
	}
}

func mergeRetrieverEndAttrs(attrs Attrs, output callbacks.CallbackOutput, rec Recorder, baseLabels map[string]string) map[string]string {
	ro := retriever.ConvCallbackOutput(output)
	if ro == nil {
		ro = &retriever.CallbackOutput{}
	}
	// Graph 包装的 CallbackOutput Conv 后返回零 Docs，真实 attrs 在 EinoRetrieverAdapter.Retrieve 补
	FillDocScoreAttrs(attrs, ro.Docs)
	if len(ro.Docs) > 0 {
		if preview := buildTopDocsPreview(ro.Docs, 3, rec); preview != "" {
			attrs["top_docs_preview"] = preview
		}
	}
	return cloneLabels(baseLabels)
}

// FillDocScoreAttrs 计算 score 统计并写入 attrs，供 EinoRetrieverAdapter 复用。
func FillDocScoreAttrs(attrs Attrs, docs []*schema.Document) {
	n := len(docs)
	attrs["hit_n"] = n
	if n == 0 {
		return
	}
	var (
		sumScore float64
		minScore = 1.0
		maxScore = 0.0
	)
	for _, d := range docs {
		s := d.Score()
		sumScore += s
		if s < minScore {
			minScore = s
		}
		if s > maxScore {
			maxScore = s
		}
	}
	attrs["avg_score"] = sumScore / float64(n)
	if n > 1 {
		attrs["min_score"] = minScore
		attrs["max_score"] = maxScore
	}
}

func mergeToolEndAttrs(attrs Attrs, output callbacks.CallbackOutput, info *callbacks.RunInfo, rec Recorder, baseLabels map[string]string) map[string]string {
	if to := tool.ConvCallbackOutput(output); to != nil {
		attrs["response_len"] = len(to.Response)
		if to.ToolOutput != nil {
			attrs["tool_output_parts_n"] = len(to.ToolOutput.Parts)
		}
		if to.Response != "" {
			preview := rec.PreviewAttr(to.Response, contentLenLong)
			attrs["response_preview"] = preview
			attrs[AttrGenAIToolCallResult] = preview
		}
	}
	return withToolNameLabels(baseLabels, info.Name)
}

func mergeEmbeddingEndAttrs(attrs Attrs, output callbacks.CallbackOutput, baseLabels map[string]string) map[string]string {
	eo := embedding.ConvCallbackOutput(output)
	if eo == nil {
		return cloneLabels(baseLabels)
	}
	attrs["embeddings_n"] = len(eo.Embeddings)
	// 向量维度取第一条 embedding 的长度（eino 的 CallbackOutput 不带 dimension 配置）
	if len(eo.Embeddings) > 0 {
		attrs[AttrGenAIEmbeddingsDimensionCount] = len(eo.Embeddings[0])
	}
	// embedding 的 TokenUsage 是另一个类型（embedding.TokenUsage），且只有输入侧用量，
	// 不存在 completion/output tokens，所以不走 mergeTokenUsageAttrs。
	if eo.TokenUsage != nil {
		attrs["prompt_tokens"] = eo.TokenUsage.PromptTokens
		attrs["total_tokens"] = eo.TokenUsage.TotalTokens
		attrs[AttrGenAIUsageInputTokens] = eo.TokenUsage.PromptTokens
	}
	modelID := ""
	if eo.Config != nil {
		modelID = eo.Config.Model
		if modelID != "" {
			attrs["model_id"] = modelID
			attrs[AttrGenAIResponseModel] = modelID
		}
	}
	el := cloneLabels(baseLabels)
	if modelID != "" {
		el["model_id"] = modelID
	}
	return el
}

func einoOnError(rec Recorder) func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
	if rec == nil {
		return func(ctx context.Context, _ *callbacks.RunInfo, _ error) context.Context { return ctx }
	}
	return func(ctx context.Context, info *callbacks.RunInfo, err error) context.Context {
		if info == nil || err == nil {
			return ctx
		}
		state := stateFromCtx(ctx)
		labels := baseMetricLabels(info)

		switch info.Component {
		case components.ComponentOfChatModel, components.ComponentOfAgenticModel:
			rec.Incr(ctx, "eino_llm_errors_total", labels, 1)
		case components.ComponentOfRetriever:
			rec.Incr(ctx, "eino_retriever_errors_total", labels, 1)
		case components.ComponentOfTool:
			tl := withToolNameLabels(labels, info.Name)
			rec.Incr(ctx, "eino_tool_errors_total", tl, 1)
			rec.Incr(ctx, "agent_tool_calls_total", agentToolCallLabels(info.Name, "error"), 1)
		case components.ComponentOfEmbedding:
			rec.Incr(ctx, "eino_embed_errors_total", labels, 1)
		case adk.ComponentOfAgent, adk.ComponentOfAgenticAgent, "Graph", "Chain", "Workflow":
			rec.Incr(ctx, "eino_agent_errors_total", labels, 1)
		}

		errAttrs := Attrs{
			"eino_name": info.Name,
			"eino_type": info.Type,
		}
		if op := genAIOperationName(info.Component); op != "" {
			errAttrs[AttrGenAIOperationName] = op
		}
		endSpanIfPresent(rec, ctx, state, errAttrs, SpanStatusError, err)
		return ctx
	}
}

// einoOnStreamStart 处理「以流为入参」的组件（Collect 范式 / 流式 Lambda / 流式子图）。
//
// 与 einoOnStart 一样建 span 并存 state —— 两条 timing 互斥（见 beginEinoSpan 注释），
// 不建的话该组件在追踪里整段没有节点，且它的 OnEnd 会把身份属性写到上一层 span 上。
func einoOnStreamStart(rec Recorder) func(ctx context.Context, info *callbacks.RunInfo, input *schema.StreamReader[callbacks.CallbackInput]) context.Context {
	if rec == nil {
		return func(ctx context.Context, _ *callbacks.RunInfo, input *schema.StreamReader[callbacks.CallbackInput]) context.Context {
			if input != nil {
				go safeDrainAndCloseReader(input)
			}
			return ctx
		}
	}
	return func(ctx context.Context, info *callbacks.RunInfo, input *schema.StreamReader[callbacks.CallbackInput]) context.Context {
		if input == nil {
			return ctx
		}
		if info == nil {
			go safeDrainAndCloseReader(input)
			return ctx
		}
		// 建 span 必须在 drain 之前：drain 是异步的，而组件此刻已经开始执行了。
		// 细粒度 attrs 拿不到（流式入参不是 callbacks.CallbackInput，要读流才知道内容），
		// 所以只标注这条 timing，不阻塞组件启动。
		ctx = beginEinoSpan(ctx, rec, info, nil, Attrs{"stream_input": true})
		go safeDrainAndCloseReader(input)
		return ctx
	}
}

// einoOnStreamEnd 流式输出结束后补 EndSpan。
//
// 流式路径 eino 只触发 OnEndWithStreamOutput、不触发 OnEnd（见 eino-ext 的
// ChatModel.Stream 实现），所以 token 用量 / finish_reason / 响应模型这些「结束态」
// 信息只能在读完流之后自己聚合 —— 否则主链路（快速模式、深度模式都是流式）的
// gen_ai.usage.* 永远是空的，三方平台上的 LLM 卡片看不到任何成本数据。
//
// 关于并发：eino 会给每个 handler 一份独立的流副本
// （internal/callbacks.OnWithStreamHandle 里调 output.Copy(n)），所以这里可以放心
// 把整条流读完。但 StreamReader 是 read-once 的，这条副本只能有一个消费者 ——
// 聚合和 Close 都必须在同一个 goroutine 里做完，不能再另外起 goroutine 去 drain，
// 否则两个 goroutine 会把 chunk 抢散，聚合出的用量是错的。
func einoOnStreamEnd(ctx context.Context, info *callbacks.RunInfo, output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
	if info == nil {
		if output != nil {
			go safeDrainAndCloseReader(output)
		}
		return ctx
	}
	state, _ := ctx.Value(einoSpanKey{}).(*einoSpanState)
	if state == nil || state.span == nil {
		if output != nil {
			go safeDrainAndCloseReader(output)
		}
		return ctx
	}
	// 异步等 reader 读完再 EndSpan，让 span 覆盖到最后一个 token
	go func() {
		defer func() { _ = recover() }()
		var (
			usage        *model.TokenUsage
			respModel    string
			finishReason string
			content      strings.Builder
			firstChunkAt time.Time
		)
		if output != nil {
			for {
				chunk, err := output.Recv()
				if err != nil {
					break
				}
				mo := model.ConvCallbackOutput(chunk)
				if mo == nil {
					continue
				}
				// 首个带正文的 chunk 即「首字」。这是流式应用最关键的体验指标，
				// 无法从总耗时推算，错过这个位置就再也拿不到。
				if mo.Message != nil && mo.Message.Content != "" {
					if firstChunkAt.IsZero() {
						firstChunkAt = time.Now()
					}
					content.WriteString(mo.Message.Content)
				}
				if mo.TokenUsage != nil {
					usage = mo.TokenUsage
				}
				if mo.Config != nil && mo.Config.Model != "" {
					respModel = mo.Config.Model
				}
				if mo.Message != nil && mo.Message.ResponseMeta != nil && mo.Message.ResponseMeta.FinishReason != "" {
					finishReason = mo.Message.ResponseMeta.FinishReason
				}
			}
			// 必须 Close：eino 明确要求每个 handler 关掉自己那份副本，
			// 不关的话原始流无法释放，整条链路会泄漏 goroutine / 内存。
			output.Close()
		}

		rec := RecorderFromContext(ctx)
		if rec == nil {
			return
		}
		dur := time.Since(state.startAt)
		attrs := Attrs{
			"streaming":            true,
			"duration_ms":          strconv.FormatInt(dur.Milliseconds(), 10),
			"eino_name":            info.Name,
			"eino_type":            info.Type,
			"eino_comp":            string(info.Component),
			AttrGenAIRequestStream: true,
		}
		if op := genAIOperationName(info.Component); op != "" {
			attrs[AttrGenAIOperationName] = op
		}
		if finishReason != "" {
			attrs[AttrGenAIResponseFinishReasons] = []string{finishReason}
		}
		if respModel != "" {
			attrs[AttrGenAIResponseModel] = respModel
		}
		mergeTokenUsageAttrs(attrs, usage)

		// 流式正文：与 mergeChatModelEndAttrs 的非流式路径对齐。缺了这一步，
		// 主链路（快速模式与深度模式都是流式）的模型输出在本地 span_tree 与
		// 三方平台上都是空的 —— 只能看到 token 数，看不到模型说了什么。
		if text := content.String(); text != "" {
			attrs["reply_preview"] = rec.PreviewAttr(text, contentLenLong)
			attrs[AttrGenAIOutputMessages] = buildOutputMessagesPayload(text, rec)
		}
		// 首字延迟：既能填三方平台的 timeToFirstToken，也能本地量化流式体验。
		if !firstChunkAt.IsZero() {
			ttft := firstChunkAt.Sub(state.startAt)
			attrs["ttft_ms"] = strconv.FormatInt(ttft.Milliseconds(), 10)
			attrs[AttrGenAIServerTimeToFirstToken] = ttft.Seconds()
		}

		rec.EndSpan(ctx, state.span, SpanStatusOK, nil, attrs)

		labels := map[string]string{"component": componentLabel(string(info.Component))}
		if info.Name != "" {
			labels["name"] = info.Name
		}
		rec.Incr(ctx, "eino_stream_end_total", labels, 1)
		rec.Observe(ctx, "eino_stream_end_seconds", labels, dur.Seconds())

		// 流式 ChatModel 走不到 einoOnEnd，LLM 的请求数 / 耗时 / token 指标要在这里补，
		// 否则 /metrics 上的 eino_llm_* 只统计到非流式调用（少数），流式主链路全是 0。
		// 两处不会重复计数：ChatModel.Stream 只触发 OnEndWithStreamOutput，
		// ChatModel.Generate 只触发 OnEnd，同一次调用只会命中一边。
		if info.Component == components.ComponentOfChatModel || info.Component == components.ComponentOfAgenticModel {
			llmLabels := cloneLabels(labels)
			if respModel != "" {
				llmLabels["model_id"] = respModel
			}
			rec.Incr(ctx, "eino_llm_requests_total", llmLabels, 1)
			rec.Incr(ctx, "eino_llm_stream_requests_total", llmLabels, 1)
			rec.Observe(ctx, "eino_llm_duration_seconds", llmLabels, dur.Seconds())
			observeLLMTokens(rec, llmLabels, attrs)
		}
	}()
	return ctx
}

// endSpanIfPresent 统一处理 EndSpan。
func endSpanIfPresent(rec Recorder, ctx context.Context, state *einoSpanState, attrs Attrs, status SpanStatus, err error) {
	if state.span == nil {
		return
	}
	rec.EndSpan(ctx, state.span, status, err, attrs)
}

// safeDrainAndCloseReader 读完 StreamReader 直到 EOF 再 Close，幂等。
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
			if _, err := r.Recv(); err != nil {
				r.Close()
				return
			}
		}
	}()
}

func cloneLabels(src map[string]string) map[string]string {
	out := make(map[string]string, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// withToolNameLabels 复制 baseLabels 并追加 tool_name。
func withToolNameLabels(base map[string]string, name string) map[string]string {
	out := cloneLabels(base)
	if name != "" {
		out["tool_name"] = name
	}
	return out
}

// agentToolCallLabels 构造 agent_tool_calls_total 的 labels。
func agentToolCallLabels(name, status string) map[string]string {
	labels := map[string]string{"status": status}
	if name != "" {
		labels["tool"] = name
	}
	return labels
}

func collectToolNames(tools []*schema.ToolInfo) []string {
	if len(tools) == 0 {
		return nil
	}
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		if t != nil && t.Name != "" {
			out = append(out, t.Name)
		}
	}
	return out
}

func lastMessageByRole(msgs []*schema.Message, role string) *schema.Message {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i] != nil && string(msgs[i].Role) == role {
			return msgs[i]
		}
	}
	return nil
}

func firstMessageByRole(msgs []*schema.Message, role string) *schema.Message {
	for _, m := range msgs {
		if m != nil && string(m.Role) == role {
			return m
		}
	}
	return nil
}

func countMessageRoles(msgs []*schema.Message) map[string]int {
	counter := make(map[string]int, 5)
	for _, m := range msgs {
		if m == nil {
			continue
		}
		counter[string(m.Role)]++
	}
	return counter
}

func extractToolCallNames(tcs []schema.ToolCall) []string {
	if len(tcs) == 0 {
		return nil
	}
	out := make([]string, 0, len(tcs))
	for _, tc := range tcs {
		if tc.Function.Name != "" {
			out = append(out, tc.Function.Name)
		}
	}
	return out
}

// joinShortList 把字符串列表拼成 "a, b, c (+N more)" 风格
func joinShortList(items []string, maxShow int) string {
	if len(items) == 0 {
		return ""
	}
	if maxShow <= 0 {
		maxShow = 3
	}
	show := items
	more := 0
	if len(items) > maxShow {
		show = items[:maxShow]
		more = len(items) - maxShow
	}
	s := strings.Join(show, ", ")
	if more > 0 {
		s += " (+" + strconv.Itoa(more) + " more)"
	}
	return s
}

// DocsPreview 把 Retriever 返回的前 N 条 doc 拼成 "1. title(id) s=score: snippet; 2. …" 预览串。
// 对外暴露给 RAG/LLM adapter 在拿到检索结果后，手动补 top_docs_preview attrs。
func DocsPreview(docs []*schema.Document, topN int, rec Recorder) string {
	return buildTopDocsPreview(docs, topN, rec)
}

// buildTopDocsPreview 拼接前 N 条 doc 的预览，每条 snippet 限 contentLenShort rune，整体限 contentLenLong rune
func buildTopDocsPreview(docs []*schema.Document, topN int, rec Recorder) string {
	if len(docs) == 0 || rec == nil {
		return ""
	}
	if topN <= 0 {
		topN = 3
	}
	if len(docs) < topN {
		topN = len(docs)
	}
	var sb strings.Builder
	for i := 0; i < topN; i++ {
		d := docs[i]
		if d == nil {
			continue
		}
		if i > 0 {
			sb.WriteString(" | ")
		}
		// 形如: "#1 myDoc(chunk_123) s=0.9200: xxxx…"
		sb.WriteString("#")
		sb.WriteString(strconv.Itoa(i + 1))
		sb.WriteString(" ")
		title := docTitle(d)
		if title != "" {
			sb.WriteString(title)
		} else {
			sb.WriteString("(untitled)")
		}
		if d.ID != "" {
			sb.WriteString("(")
			sb.WriteString(d.ID)
			sb.WriteString(")")
		}
		sb.WriteString(" s=")
		sb.WriteString(strconv.FormatFloat(d.Score(), 'f', 4, 64))
		if d.Content != "" {
			sb.WriteString(": ")
			sb.WriteString(rec.PreviewAttr(d.Content, contentLenShort))
		}
	}
	return rec.PreviewAttr(sb.String(), contentLenLong)
}

func docTitle(d *schema.Document) string {
	if d == nil || d.MetaData == nil {
		return ""
	}
	for _, key := range []string{"title", "source"} {
		if v, ok := d.MetaData[key]; ok {
			if s, _ := v.(string); s != "" {
				return s
			}
		}
	}
	return ""
}
