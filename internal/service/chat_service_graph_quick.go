package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/components/retriever"
	einoCompose "github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"

	llmpkg "solvify-agent/internal/llm"
	requestdto "solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/observability"
	"solvify-agent/internal/rag"
	"solvify-agent/pkg/config"
	apperrors "solvify-agent/pkg/errors"
	"solvify-agent/pkg/logger"
	"solvify-agent/pkg/tokenutil"
)

// quickGraphInput 快速模式 Graph 入参，跨节点共享的上下文通过 Local State 传递。
type quickGraphInput struct {
	// OriginalQuery 用户原始问题
	OriginalQuery string
	// UserID 用户标识，注入到检索请求埋点
	UserID string
	// KnowledgeBaseIDs 限定检索范围
	KnowledgeBaseIDs []string
	// InputMsgs 已经组装好的 System + History + RewritePlaceholder
	InputMsgs []*schema.Message
	// UserQuestionIndex InputMsgs 里代表「用户问题」那条消息的下标（用于替换成改写后的 query）
	UserQuestionIndex int
	// ModelName 模型名，用于真 BPE token 截断
	ModelName string
	// RetrievalBudget 检索上下文 token 预算（真 BPE）
	RetrievalBudget int
}

// quickGraphState Graph Local State，通过 ProcessState 读写。
type quickGraphState struct {
	Input          *quickGraphInput
	RewrittenQuery string
	RetrievedDocs  []*schema.Document
}

const (
	graphQuickNodeRewrite   = "query_rewrite"
	graphQuickNodeRetrieve  = "retrieve"
	graphQuickNodeBuildMsgs = "build_prompt_messages"
	graphQuickNodeGenerate  = "generate"
)

// genState 每次 Graph 执行新建一个本地状态
func genState(_ context.Context) *quickGraphState {
	return &quickGraphState{}
}

// buildQuickGraph 构建 START → rewrite → retrieve → build_msgs → generate → END 流水线。
func buildQuickGraph(
	_ context.Context,
	einoRetriever *rag.EinoRetrieverAdapter,
) (*einoCompose.Graph[*quickGraphInput, *schema.StreamReader[*schema.Message]], error) {
	g := einoCompose.NewGraph[*quickGraphInput, *schema.StreamReader[*schema.Message]](
		einoCompose.WithGenLocalState(genState),
	)
	if err := addQuickRewriteNode(g); err != nil {
		return nil, wrapGraphErr("add rewrite node", err)
	}
	if err := addQuickRetrieveNode(g, einoRetriever); err != nil {
		return nil, wrapGraphErr("add retrieve node", err)
	}
	if err := addQuickBuildMsgsNode(g); err != nil {
		return nil, wrapGraphErr("add build msgs node", err)
	}
	if err := addQuickGenerateNode(g); err != nil {
		return nil, wrapGraphErr("add generate node", err)
	}
	if err := registerQuickGraphEdges(g); err != nil {
		return nil, wrapGraphErr("register edges", err)
	}
	return g, nil
}

// wrapGraphErr 统一包装"Graph 装配错误"为业务错误码，避免每个 AddXxx 节点都写一遍
func wrapGraphErr(stage string, err error) error {
	return apperrors.WrapDefault(apperrors.CodeInternalError, fmt.Errorf("%s: %w", stage, err))
}

// addQuickRewriteNode 节点 1：QueryRewrite（占位，返回原文）。
func addQuickRewriteNode(g *einoCompose.Graph[*quickGraphInput, *schema.StreamReader[*schema.Message]]) error {
	return g.AddLambdaNode(graphQuickNodeRewrite,
		einoCompose.InvokableLambda(quickRewriteFn),
		einoCompose.WithNodeName("QueryRewrite"),
	)
}

// quickRewriteFn 节点 1 实现：暂存输入到 State 并原样返回查询
func quickRewriteFn(ctx context.Context, input *quickGraphInput) (string, error) {
	if input == nil {
		return "", apperrors.NewDefault(apperrors.CodeInvalidParam)
	}
	if err := einoCompose.ProcessState(ctx, func(_ context.Context, state *quickGraphState) error {
		state.Input = input
		return nil
	}); err != nil {
		return "", err
	}
	startAt := time.Now()
	rewritten := input.OriginalQuery
	_ = einoCompose.ProcessState(ctx, func(_ context.Context, state *quickGraphState) error {
		state.RewrittenQuery = rewritten
		return nil
	})
	durMs := time.Since(startAt).Milliseconds()
	observability.SetSpanAttrs(ctx, observability.Attrs{
		"original_query":  input.OriginalQuery,
		"rewritten_query": rewritten,
		"rewrite_ms":      durMs,
		"model_id":        input.ModelName,
	})
	return rewritten, nil
}

// addQuickRetrieveNode 节点 2：Retrieve，PostHandler 把结果写回 State。
func addQuickRetrieveNode(g *einoCompose.Graph[*quickGraphInput, *schema.StreamReader[*schema.Message]], einoRetriever *rag.EinoRetrieverAdapter) error {
	return g.AddRetrieverNode(graphQuickNodeRetrieve, einoRetriever,
		einoCompose.WithNodeName("KnowledgeRetrieve"),
		einoCompose.WithStatePostHandler(func(_ context.Context, docs []*schema.Document, state *quickGraphState) ([]*schema.Document, error) {
			state.RetrievedDocs = docs
			return docs, nil
		}),
	)
}

// addQuickBuildMsgsNode 节点 3：BuildPromptMessages。
// 从 State 拿 Input，在 userQuestionIndex 前插入检索上下文。
func addQuickBuildMsgsNode(g *einoCompose.Graph[*quickGraphInput, *schema.StreamReader[*schema.Message]]) error {
	return g.AddLambdaNode(graphQuickNodeBuildMsgs,
		einoCompose.InvokableLambda(quickBuildMsgsFn),
		einoCompose.WithNodeName("BuildPromptMessages"),
	)
}
// quickBuildMsgsFn 节点 3 实现：在用户问题前插入检索上下文块

func quickBuildMsgsFn(ctx context.Context, docs []*schema.Document) ([]*schema.Message, error) {
	var input *quickGraphInput
	if err := einoCompose.ProcessState(ctx, func(_ context.Context, state *quickGraphState) error {
		input = state.Input
		return nil
	}); err != nil || input == nil {
		return nil, apperrors.NewDefault(apperrors.CodeInternalError)
	}
	msgs := make([]*schema.Message, 0, len(input.InputMsgs)+2)
	injected := false
	for i, m := range input.InputMsgs {
		if i == input.UserQuestionIndex && len(docs) > 0 {
			block := buildDocsContextBlock(docs, input.RetrievalBudget, input.ModelName)
			msgs = append(msgs, schema.UserMessage(block))
			injected = true
		}
		msgs = append(msgs, m)
	}
	if len(docs) > 0 && !injected {
		last := msgs[len(msgs)-1]
		block := buildDocsContextBlock(docs, input.RetrievalBudget, input.ModelName)
		msgs = append(append(msgs[:len(msgs)-1], schema.UserMessage(block)), last)
	}
	return msgs, nil
}

// addQuickGenerateNode 节点 4：ChatModelGenerate。
// 用 InvokableLambda 而非 StreamableLambda，因为返回值是 StreamReader 本身，
// StreamableLambda 会推断 O 为流内元素 Message，和 END 期望的 StreamReader[Message] 类型不匹配。
func addQuickGenerateNode(g *einoCompose.Graph[*quickGraphInput, *schema.StreamReader[*schema.Message]]) error {
	return g.AddLambdaNode(graphQuickNodeGenerate,
		einoCompose.InvokableLambda(quickGenerateFn),
		einoCompose.WithNodeName("ChatModelGenerate"),
	)
}

// quickGenerateFn 节点 4 实现：调用 ChatModel 流式生成回复
func quickGenerateFn(ctx context.Context, msgs []*schema.Message) (*schema.StreamReader[*schema.Message], error) {
	var cm einoModel.BaseChatModel
	var modelName string
	if err := einoCompose.ProcessState(ctx, func(_ context.Context, state *quickGraphState) error {
		if state.Input == nil {
			return errors.New("nil input in state")
		}
		cm, _ = graphChatModelFromContext(ctx)
		if state.Input != nil {
			modelName = state.Input.ModelName
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if cm == nil {
		return nil, apperrors.NewDefault(apperrors.CodeInternalError)
	}
	// 写 Generate span 的输入 attrs
	var (
		promptTokensEst int
		lastUser        = findLastMessageByRole(msgs, "user")
		firstSystem     = findFirstMessageByRole(msgs, "system")
	)
	// 粗估 prompt tokens（流式不返回 usage）
	if modelName == "" {
		modelName = "cl100k_base"
	}
	for _, m := range msgs {
		if m != nil && m.Content != "" {
			promptTokensEst += tokenutil.CountTokens(m.Content, modelName)
		}
	}
	inAttrs := observability.Attrs{
		"messages_n":    len(msgs),
		"prompt_tokens": promptTokensEst,
		"model_id":      modelName,
	}
	if lastUser != nil && lastUser.Content != "" {
		inAttrs["last_user_msg_preview"] = lastUser.Content
	}
	if firstSystem != nil && firstSystem.Content != "" {
		inAttrs["system_prompt_preview"] = firstSystem.Content
	}
	observability.SetSpanAttrs(ctx, inAttrs)
	sr, err := cm.Stream(ctx, msgs)
	if err != nil {
		return nil, err
	}
	return sr, nil
}

// findLastMessageByRole 找 msgs 中指定 role 的最后一条消息
func findLastMessageByRole(msgs []*schema.Message, role string) *schema.Message {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i] != nil && string(msgs[i].Role) == role {
			return msgs[i]
		}
	}
	return nil
}

// findFirstMessageByRole 找 msgs 中指定 role 的第一条消息
func findFirstMessageByRole(msgs []*schema.Message, role string) *schema.Message {
	for _, m := range msgs {
		if m != nil && string(m.Role) == role {
			return m
		}
	}
	return nil
}

// registerQuickGraphEdges 按 4 节点流水线一次性注册 5 条边
func registerQuickGraphEdges(g *einoCompose.Graph[*quickGraphInput, *schema.StreamReader[*schema.Message]]) error {
	edges := [][2]string{
		{einoCompose.START, graphQuickNodeRewrite},
		{graphQuickNodeRewrite, graphQuickNodeRetrieve},
		{graphQuickNodeRetrieve, graphQuickNodeBuildMsgs},
		{graphQuickNodeBuildMsgs, graphQuickNodeGenerate},
		{graphQuickNodeGenerate, einoCompose.END},
	}
	for _, e := range edges {
		if err := g.AddEdge(e[0], e[1]); err != nil {
			return fmt.Errorf("edge %s→%s: %w", e[0], e[1], err)
		}
	}
	return nil
}

// buildDocsContextBlock 按 score 占比分配 token 预算，格式化检索文档为引用上下文。
func buildDocsContextBlock(docs []*schema.Document, retrievalBudget int, modelName string) string {
	if len(docs) == 0 {
		return ""
	}
	// 预算非法时退化
	if retrievalBudget <= 0 {
		retrievalBudget = 2000
	}
	if modelName == "" {
		modelName = "cl100k_base"
	}

	header := "以下是知识库返回的参考资料（可能与问题相关）：\n" +
		"回答时必须把对应引用块的 chunk_id 插到对应句末，格式为 <kb doc=\"文档名\" chunk_id=\"chunk_id\" />。\n" +
		"【禁止】直接复制参考资料原文作为回答。\n\n"
	headerTokens := tokenutil.CountTokens(header, modelName)

	var sb strings.Builder
	sb.WriteString(header)

	// 按 score 排序 + 分配配额
	type scored struct {
		idx   int
		score float64
		title string
	}
	scoredDocs := make([]scored, 0, len(docs))
	totalScore := 0.0
	for i, d := range docs {
		title := ""
		if d.MetaData != nil {
			if v, ok := d.MetaData[rag.MetaTitle()].(string); ok {
				title = v
			}
		}
		// eino Score() 一般在 [0,1]，加 1e-6 避免除零
		s := d.Score()
		if s <= 0 {
			s = 1e-6
		}
		totalScore += s
		scoredDocs = append(scoredDocs, scored{idx: i, score: s, title: title})
	}
	// 降序：高分段先分配
	sort.Slice(scoredDocs, func(i, j int) bool {
		return scoredDocs[i].score > scoredDocs[j].score
	})

	perDocHeadBudget := 60 // chunk_id/score/文档名 行的粗估
	remainingBudget := retrievalBudget - headerTokens
	if remainingBudget < 200 {
		// 预算太少时直接按 rune 粗截
		sb2 := strings.Builder{}
		sb2.WriteString(header)
		for i, d := range docs {
			title := ""
			if d.MetaData != nil {
				if v, ok := d.MetaData[rag.MetaTitle()].(string); ok {
					title = v
				}
			}
			sb2.WriteString(fmt.Sprintf("--- 参考 %d [chunk_id=%s] [score=%.3f] ---\n", i+1, d.ID, d.Score()))
			if title != "" {
				sb2.WriteString(fmt.Sprintf("文档名：%s\n", title))
			}
			cut, _ := tokenutil.TruncateByTokens(d.Content, modelName, remainingBudget/(len(docs)+1))
			sb2.WriteString(cut)
			sb2.WriteString("\n\n")
		}
		return sb2.String()
	}

	// 按比例分配内容 token
	headTokensAll := perDocHeadBudget * len(docs)
	contentBudget := remainingBudget
	if contentBudget > headTokensAll+100 {
		contentBudget -= headTokensAll
	}
	const minContentPerDoc = 120

	for i, sd := range scoredDocs {
		d := docs[sd.idx]
		headStr := fmt.Sprintf("--- 参考 %d [chunk_id=%s] [score=%.3f] ---\n", sd.idx+1, d.ID, d.Score())
		if sd.title != "" {
			headStr += fmt.Sprintf("文档名：%s\n", sd.title)
		}
		sb.WriteString(headStr)
		headUsed := tokenutil.CountTokens(headStr, modelName)

		ratio := sd.score / totalScore
		bonus := 1.0
		if i == 0 {
			bonus = 1.15
		}
		share := int(float64(contentBudget) * ratio * bonus)
		if share < minContentPerDoc {
			share = minContentPerDoc
		}
		shareForContent := share - (headUsed - perDocHeadBudget)
		if shareForContent < minContentPerDoc/2 {
			shareForContent = minContentPerDoc / 2
		}
		if shareForContent > remainingBudget {
			shareForContent = remainingBudget
		}
		if shareForContent <= 0 {
			sb.WriteString("\n")
			continue
		}

		cut, used := tokenutil.TruncateByTokens(d.Content, modelName, shareForContent)
		sb.WriteString(cut)
		sb.WriteString("\n\n")

		usedTotal := headUsed + used
		if usedTotal > remainingBudget {
			remainingBudget = 0
		} else {
			remainingBudget -= usedTotal
		}
		if usedTotal+perDocHeadBudget > contentBudget {
			contentBudget = 0
		} else {
			contentBudget -= usedTotal + perDocHeadBudget
		}
		if remainingBudget < minContentPerDoc {
			break
		}
	}
	return sb.String()
}

// graphCtxChatModelKey 用作 context.WithValue 的 key，存放 per-request 的 ChatModel。
type graphCtxChatModelKeyType struct{}

var graphCtxChatModelKey = graphCtxChatModelKeyType{}

// withGraphChatModel 把 ChatModel 注入 context
func withGraphChatModel(ctx context.Context, cm einoModel.BaseChatModel) context.Context {
	return context.WithValue(ctx, graphCtxChatModelKey, cm)
}
// graphChatModelFromContext 从 context 取出 ChatModel

func graphChatModelFromContext(ctx context.Context) (einoModel.BaseChatModel, bool) {
	v := ctx.Value(graphCtxChatModelKey)
	if v == nil {
		return nil, false
	}
	cm, ok := v.(einoModel.BaseChatModel)
	return cm, ok
}

// processMessageGraphQuick 快速模式入口：QueryRewrite → Retrieve → BuildPrompt → Generate 四节点 Graph。
func (s *chatService) processMessageGraphQuick(
	ctx context.Context,
	userID, sessionID, userMsgID string,
	req requestdto.SendMessageRequest,
	eventCh chan<- dto.StreamEvent,
) {
	// 根 Span + panic recover（单独抽成 startQuickSpan 更清爽）
	ctx, span, obsOk := startQuickSpan(ctx, s.obs, sessionID, userID, req.ModelID)
	if obsOk {
		defer func() {
			status := observability.SpanStatusOK
			var errVal error
			if r := recover(); r != nil {
				status = observability.SpanStatusError
				errVal = fmt.Errorf("panic: %v", r)
				eventCh <- dto.StreamEvent{Type: "error", Detail: "处理过程中发生未预期错误", Done: true}
			}
			s.obs.EndSpan(ctx, span, status, errVal, nil)
		}()
		s.obs.Incr(ctx, "chat_quick_graph_requests_total", map[string]string{"model_id": req.ModelID}, 1)
	}

	// 1) 初始化上下文（历史/摘要/记忆/画像/预算）
	sendProgressEvent(eventCh, "正在加载上下文...")
	t0 := time.Now()
	client, enhancedCtx, err := s.initContext(ctx, userID, sessionID, req.ModelID, req.ModelType, req.Content)
	if err != nil {
		quickIncrError(ctx, s.obs, obsOk, "init_ctx")
		if obsOk {
			s.obs.MarkTraceError(ctx, err)
		}
		sendErrorEvent(eventCh, err, err.Error())
		return
	}
	chatModel := client.ChatModel()
	if obsOk {
		s.obs.Observe(ctx, "chat_quick_graph_init_ctx_seconds", map[string]string{"model_id": req.ModelID}, time.Since(t0).Seconds())
	}

	// 2~3) 组装 Graph Input：System Prompt / History / 模型名 / 检索预算
	graphInput := buildQuickInput(req, userID, userMsgID, enhancedCtx, client)

	// 4) 构建并编译 compose.Graph（内部已经 push error 事件）
	sendProgressEvent(eventCh, "正在组装快速检索链路...")
	graphCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	runnable, err := compileQuickGraphLocal(graphCtx, s.einoRetriever, req.ModelID, s.obs, obsOk, eventCh)
	if err != nil {
		if obsOk {
			s.obs.MarkTraceError(ctx, err)
		}
		return
	}

	// 5) 注入 per-request ChatModel + Retriever 节点选项
	graphCtx = withGraphChatModel(graphCtx, chatModel)
	callOpts := quickRetrieverCallOpts(req, userID)

	// 6) 生成助手消息 ID + 流式驱动 Graph 执行
	assistantMsgID := uuid.New().String()
	if obsOk {
		s.obs.AddRootAttrs(ctx, observability.Attrs{"assistant_message_id": assistantMsgID})
	}
	eventCh <- dto.StreamEvent{Type: "start", MessageID: assistantMsgID}

	fullContent, err := runQuickStream(
		graphCtx, runnable, graphInput, callOpts,
		eventCh, req.ModelID, assistantMsgID, s.obs, obsOk,
	)
	if err != nil {
		if obsOk {
			s.obs.MarkTraceError(ctx, err)
		}
		llmpkg.ReduceContextBudgetOnError(req.ModelID, err)
		return
	}

	// 7) 从 Graph State 取 RetrievedDocs → SourceInfo（引用展示用）
	sources, docsCount := extractSourcesFromStateLocal(graphCtx)
	if obsOk {
		s.obs.AddRootAttrs(ctx, observability.Attrs{
			"assistant_chars": fmt.Sprintf("%d", len([]rune(fullContent))),
			"retrieved_docs":  fmt.Sprintf("%d", docsCount),
		})
	}

	// 8) 结束事件 + 异步落库 + 异步刷新摘要记忆
	s.emitDoneAndSave(eventCh, sessionID, assistantMsgID, fullContent, req, sources, nil, func(meta map[string]any) {
		if obsOk && meta != nil {
			meta["trace_id"] = observability.TraceIDFromContext(ctx)
			meta["eino_quick_graph_mode"] = true
		}
	})
	s.refreshContextAsync(ctx, userID, sessionID, enhancedCtx.History, chatModel)
}

// startQuickSpan 创建根 Span，返回 (newCtx, span, ok)，避免主流程写一长串可观测性样板
func startQuickSpan(ctx context.Context, obs observability.Recorder, sessionID, userID, modelID string) (context.Context, *observability.Span, bool) {
	if obs == nil {
		return ctx, nil, false
	}
	newCtx, span := obs.StartSpan(ctx, "chat.quick.graph", observability.ComponentAgentEngine, observability.Attrs{
		"session_id":  sessionID,
		"user_id":     userID,
		"model_id":    modelID,
		"search_mode": "quick_graph",
	})
	if span == nil {
		return ctx, nil, false
	}
	return newCtx, span, true
}

// buildQuickInput 组装 quickGraphInput：System Prompt / History / 预算 / 模型名
func buildQuickInput(
	req requestdto.SendMessageRequest,
	userID, userMsgID string,
	enhancedCtx *EnhancedContext,
	client *llmpkg.OpenAIClient,
) *quickGraphInput {
	history := excludeByMessageID(enhancedCtx.History, userMsgID)
	pb := NewPromptBuilder(PromptModeQuick, quickModeAgentSystemPrompt, enhancedCtx.Summary, enhancedCtx.Memories, enhancedCtx.UserCtx).
		WithProfile(enhancedCtx.Profile).
		WithPreference(enhancedCtx.Preference)
	inputMsgs := append([]*schema.Message{schema.SystemMessage(pb.BuildSystem())}, pb.BuildHistory(history)...)
	inputMsgs = append(inputMsgs, schema.UserMessage(req.Content))
	return &quickGraphInput{
		OriginalQuery:     req.Content,
		UserID:            userID,
		KnowledgeBaseIDs:  req.KnowledgeBaseIDs,
		InputMsgs:         inputMsgs,
		UserQuestionIndex: len(inputMsgs) - 1,
		ModelName:         client.ModelName(),
		RetrievalBudget:   enhancedCtx.RetrievalBudget,
	}
}

// compileQuickGraphLocal build + compile graph，失败时自动推 error 事件
func compileQuickGraphLocal(
	ctx context.Context,
	einoRetriever *rag.EinoRetrieverAdapter,
	modelID string,
	obs observability.Recorder,
	obsOk bool,
	eventCh chan<- dto.StreamEvent,
) (einoCompose.Runnable[*quickGraphInput, *schema.StreamReader[*schema.Message]], error) {
	g, err := buildQuickGraph(ctx, einoRetriever)
	if err != nil {
		quickIncrError(ctx, obs, obsOk, "build_graph")
		sendErrorEvent(eventCh, err, "快速检索链路初始化失败")
		return nil, err
	}
	r, err := g.Compile(ctx, einoCompose.WithGraphName("quick_rag_pipeline"))
	if err != nil {
		quickIncrError(ctx, obs, obsOk, "compile_graph")
		sendErrorEvent(eventCh, err, "快速检索链路编译失败")
		return nil, err
	}
	return r, nil
}

// runQuickStream 驱动 Graph 执行并消费流式输出
func runQuickStream(
	graphCtx context.Context,
	runnable einoCompose.Runnable[*quickGraphInput, *schema.StreamReader[*schema.Message]],
	graphInput *quickGraphInput,
	callOpts []einoCompose.Option,
	eventCh chan<- dto.StreamEvent,
	modelID, assistantMsgID string,
	obs observability.Recorder,
	obsOk bool,
) (string, error) {
	sendProgressEvent(eventCh, "正在执行快速检索链路...")
	t0 := time.Now()
	reader, invErr := runnable.Invoke(graphCtx, graphInput, callOpts...)
	if obsOk {
		obs.Observe(graphCtx, "chat_quick_graph_run_seconds", map[string]string{"model_id": modelID}, time.Since(t0).Seconds())
	}
	if invErr != nil {
		sendErrorEvent(eventCh, invErr, "快速检索执行失败")
		return "", invErr
	}
	if reader == nil {
		sendErrorEvent(eventCh, fmt.Errorf("nil stream reader"), "快速检索未返回结果")
		return "", fmt.Errorf("nil stream reader")
	}
	defer reader.Close()
	return consumeQuickGraphStream(graphCtx, reader, assistantMsgID, eventCh)
}

// extractSourcesFromStateLocal 从 Graph Local State 取出 RetrievedDocs → SourceInfo
func extractSourcesFromStateLocal(graphCtx context.Context) ([]dto.SourceInfo, int) {
	var (
		sources   []dto.SourceInfo
		docsCount int
	)
	err := einoCompose.ProcessState(graphCtx, func(_ context.Context, state *quickGraphState) error {
		if len(state.RetrievedDocs) > 0 {
			sources = einoDocsToSourceInfos(state.RetrievedDocs)
			docsCount = len(state.RetrievedDocs)
		}
		return nil
	})
	if err != nil {
		logger.Warnf("quick graph ProcessState 取 RetrievedDocs 失败: %v", err)
	}
	return sources, docsCount
}

// consumeQuickGraphStream 消费 ChatModel 输出的 StreamReader[*schema.Message]
// 转成 dto.StreamEvent 推给前端，返回最终完整内容。
func consumeQuickGraphStream(
	ctx context.Context,
	sr *schema.StreamReader[*schema.Message],
	assistantMsgID string,
	eventCh chan<- dto.StreamEvent,
) (string, error) {
	var fullContent string
	var assistantSeen bool
	for {
		msg, err := sr.Recv()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			sendErrorEvent(eventCh, err, "快速检索流式生成失败")
			return "", err
		}
		if msg == nil {
			continue
		}
		if !assistantSeen {
			sendProgressEvent(eventCh, "正在生成回答...")
			assistantSeen = true
		}
		if msg.Content != "" {
			fullContent += msg.Content
			eventCh <- dto.StreamEvent{
				Type:      "content",
				MessageID: assistantMsgID,
				Content:   msg.Content,
			}
		}
		// 快速模式不期望 tool_calls，收到时打 warn 但不报错
		if len(msg.ToolCalls) > 0 {
			logger.Warnf("quick graph 模式收到模型 tool_calls（数量=%d），但快速模式没有挂工具链，已忽略。首条 tool name=%v",
				len(msg.ToolCalls), safeFirstToolName(msg.ToolCalls))
		}
	}
	_ = ctx
	return fullContent, nil
}

// safeFirstToolName 避免空指针
func safeFirstToolName(calls []schema.ToolCall) string {
	if len(calls) == 0 {
		return ""
	}
	return calls[0].Function.Name
}

// einoDocsToSourceInfos 把 eino schema.Document 列表转为前端 SourceInfo 结构
func einoDocsToSourceInfos(docs []*schema.Document) []dto.SourceInfo {
	ragDocs := make([]rag.Document, 0, len(docs))
	for _, d := range docs {
		ragDocs = append(ragDocs, rag.EinoDocToRagDoc(d))
	}
	return groupDocumentsToSources(ragDocs)
}

// ---------- 前向声明：可观测性 & 选项小工具（被上面拆分后的子函数调用） ----------

// quickIncrError 快速模式错误计数（避免 if obsOk 到处写）
func quickIncrError(ctx context.Context, obs observability.Recorder, obsOk bool, stage string) {
	if !obsOk {
		return
	}
	obs.Incr(ctx, "chat_quick_graph_errors_total", map[string]string{"stage": stage}, 1)
}

// quickRetrieverCallOpts 把 KBIDs/UserID/TopK 组合成只作用在 retrieve 节点的 compose.Option
func quickRetrieverCallOpts(req requestdto.SendMessageRequest, userID string) []einoCompose.Option {
	opts := []retriever.Option{}
	if len(req.KnowledgeBaseIDs) > 0 {
		opts = append(opts, rag.WithKnowledgeBaseIDs(req.KnowledgeBaseIDs))
	}
	if userID != "" {
		opts = append(opts, rag.WithUserID(userID))
	}
	if cfg := config.Get(); cfg != nil && cfg.RAG.TopK > 0 {
		opts = append(opts, retriever.WithTopK(cfg.RAG.TopK))
	}
	if len(opts) == 0 {
		return nil
	}
	return []einoCompose.Option{
		einoCompose.WithRetrieverOption(opts...).DesignateNode(graphQuickNodeRetrieve),
	}
}
