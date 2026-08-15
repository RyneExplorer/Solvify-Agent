package observability

import (
	"context"
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
	"github.com/cloudwego/eino/schema"
)

type compMapping struct {
	component Component
	label     string
}

var compMap = map[string]compMapping{
	string(components.ComponentOfChatModel):    {ComponentLLMClient, "chat_model"},
	string(components.ComponentOfAgenticModel):  {ComponentLLMClient, "agentic_model"},
	string(components.ComponentOfRetriever):     {ComponentRAGRetriever, "retriever"},
	string(components.ComponentOfTool):          {ComponentAgentTool, "tool"},
	string(components.ComponentOfEmbedding):     {ComponentLLMClient, "embedding"},
	string(adk.ComponentOfAgent):               {ComponentAgentEngine, "agent"},
	string(adk.ComponentOfAgenticAgent):        {ComponentAgentEngine, "agent"},
	"Graph":    {ComponentAgentEngine, "graph"},
	"Chain":    {ComponentAgentEngine, "chain"},
	"Workflow": {ComponentAgentEngine, "workflow"},
}

func mapComponent(comp string) Component {
	if m, ok := compMap[comp]; ok {
		return m.component
	}
	return ComponentAgentEngine
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
		OnStartWithStreamInputFn(einoOnStreamStart).
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
		comp := mapComponent(string(info.Component))
		attrs := Attrs{
			"eino_name": info.Name,
			"eino_type": info.Type,
		}
		// 按 component 提取细粒度 attrs
		switch info.Component {
		case components.ComponentOfChatModel, components.ComponentOfAgenticModel:
			mergeChatModelStartAttrs(attrs, input, rec)
		case components.ComponentOfRetriever:
			mergeRetrieverStartAttrs(attrs, input, rec)
		case components.ComponentOfTool:
			mergeToolStartAttrs(attrs, input, rec)
		case components.ComponentOfEmbedding:
			mergeEmbeddingStartAttrs(attrs, input, rec)
		}

		spanName := info.Name
		if spanName == "" {
			spanName = "eino." + componentLabel(string(info.Component))
		}
		ctxWithSpan, span := rec.StartSpan(ctx, spanName, comp, attrs)
		return context.WithValue(ctxWithSpan, einoSpanKey{}, &einoSpanState{
			startAt: time.Now(),
			span:    span,
		})
	}
}

func mergeChatModelStartAttrs(attrs Attrs, input callbacks.CallbackInput, rec Recorder) {
	mi := model.ConvCallbackInput(input)
	if mi == nil {
		return
	}
	attrs["messages_n"] = len(mi.Messages)
	attrs["tools_n"] = len(mi.Tools)
	if toolNames := collectToolNames(mi.Tools); len(toolNames) > 0 {
		attrs["tools_list"] = rec.PreviewAttr(joinShortList(toolNames, 3), 200)
	}
	if last := lastMessageByRole(mi.Messages, "user"); last != nil {
		attrs["last_user_msg_preview"] = rec.PreviewAttr(last.Content, 300)
	}
	if first := firstMessageByRole(mi.Messages, "system"); first != nil {
		attrs["system_prompt_preview"] = rec.PreviewAttr(first.Content, 300)
	}
	if mi.Config != nil {
		if mi.Config.Model != "" {
			attrs["model_id"] = mi.Config.Model
		}
		if mi.Config.Temperature != 0 {
			attrs["temperature"] = mi.Config.Temperature
		}
		if mi.Config.MaxTokens > 0 {
			attrs["max_tokens"] = mi.Config.MaxTokens
		}
		if mi.Config.TopP > 0 {
			attrs["top_p"] = mi.Config.TopP
		}
		if len(mi.Config.Stop) > 0 {
			attrs["stop"] = rec.PreviewAttr(joinShortList(mi.Config.Stop, 5), 200)
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
		attrs["query"] = rec.PreviewAttr(ri.Query, 300)
	}
	if ri.Filter != "" {
		attrs["filter"] = rec.PreviewAttr(ri.Filter, 200)
	}
}

func mergeToolStartAttrs(attrs Attrs, input callbacks.CallbackInput, rec Recorder) {
	ti := tool.ConvCallbackInput(input)
	if ti == nil {
		return
	}
	attrs["args_len"] = len(ti.ArgumentsInJSON)
	if ti.ArgumentsInJSON != "" {
		attrs["args_preview"] = rec.PreviewAttr(ti.ArgumentsInJSON, 300)
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
	}
	if len(ei.Texts) > 0 {
		attrs["first_text_preview"] = rec.PreviewAttr(ei.Texts[0], 300)
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
		case adk.ComponentOfAgent, adk.ComponentOfAgenticAgent, "Graph", "Chain", "Workflow":
			rec.Incr(ctx, "eino_agent_runs_total", baseLabels, 1)
			rec.Observe(ctx, "eino_agent_duration_seconds", baseLabels, dur.Seconds())
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
	if mo.Message.Content != "" {
		attrs["reply_preview"] = rec.PreviewAttr(mo.Message.Content, 500)
	}
	if len(mo.Message.ToolCalls) > 0 {
		attrs["tool_calls_list"] = rec.PreviewAttr(
			joinShortList(extractToolCallNames(mo.Message.ToolCalls), 5), 200,
		)
	}
	var prompt, completion, total int
	modelID := ""
	if mo.TokenUsage != nil {
		prompt = mo.TokenUsage.PromptTokens
		completion = mo.TokenUsage.CompletionTokens
		total = mo.TokenUsage.TotalTokens
		attrs["prompt_tokens"] = prompt
		attrs["completion_tokens"] = completion
		attrs["total_tokens"] = total
		if mo.TokenUsage.PromptTokenDetails.CachedTokens > 0 {
			attrs["cached_tokens"] = mo.TokenUsage.PromptTokenDetails.CachedTokens
		}
		if mo.TokenUsage.CompletionTokensDetails.ReasoningTokens > 0 {
			attrs["reasoning_tokens"] = mo.TokenUsage.CompletionTokensDetails.ReasoningTokens
		}
	}
	if mo.Config != nil {
		modelID = mo.Config.Model
	}
	llmLabels := cloneLabels(baseLabels)
	if modelID != "" {
		llmLabels["model_id"] = modelID
		attrs["model_id"] = modelID
	}
	return llmLabels
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
			attrs["response_preview"] = rec.PreviewAttr(to.Response, 500)
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
	modelID := ""
	if eo.TokenUsage != nil {
		attrs["prompt_tokens"] = eo.TokenUsage.PromptTokens
		attrs["total_tokens"] = eo.TokenUsage.TotalTokens
	}
	if eo.Config != nil {
		modelID = eo.Config.Model
		if modelID != "" {
			attrs["model_id"] = modelID
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

		endSpanIfPresent(rec, ctx, state, Attrs{
			"eino_name": info.Name,
			"eino_type": info.Type,
		}, SpanStatusError, err)
		return ctx
	}
}

func einoOnStreamStart(ctx context.Context, info *callbacks.RunInfo, input *schema.StreamReader[callbacks.CallbackInput]) context.Context {
	if input != nil {
		go safeDrainAndCloseReader(input)
	}
	return ctx
}

// einoOnStreamEnd 流式输出结束后补 EndSpan。
func einoOnStreamEnd(ctx context.Context, info *callbacks.RunInfo, output *schema.StreamReader[callbacks.CallbackOutput]) context.Context {
	if output != nil {
		go safeDrainAndCloseReader(output)
	}
	if info == nil {
		return ctx
	}
	state, _ := ctx.Value(einoSpanKey{}).(*einoSpanState)
	if state == nil || state.span == nil {
		return ctx
	}
	// 异步等 reader 读完再 EndSpan，让 span 覆盖到最后一个 token
	go func() {
		defer func() { _ = recover() }()
		if output != nil {
			for {
				if _, err := output.Recv(); err != nil {
					break
				}
			}
		}
		rec := RecorderFromContext(ctx)
		if rec == nil {
			return
		}
		dur := time.Since(state.startAt)
		labels := map[string]string{"component": componentLabel(string(info.Component))}
		if info.Name != "" {
			labels["name"] = info.Name
		}
		rec.EndSpan(ctx, state.span, SpanStatusOK, nil, Attrs{
			"streaming":   true,
			"duration_ms":  strconv.FormatInt(dur.Milliseconds(), 10),
			"eino_name":    info.Name,
			"eino_type":    info.Type,
			"eino_comp":    string(info.Component),
		})
		rec.Incr(ctx, "eino_stream_end_total", labels, 1)
		rec.Observe(ctx, "eino_stream_end_seconds", labels, dur.Seconds())
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

// buildTopDocsPreview 拼接前 N 条 doc 的预览，每条 snippet 限 200 rune，整体限 800 rune
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
			sb.WriteString(rec.PreviewAttr(d.Content, 200))
		}
	}
	return rec.PreviewAttr(sb.String(), 800)
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
