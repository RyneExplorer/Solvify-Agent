package service

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
	"gorm.io/datatypes"

	"solvify-agent/internal/agent"
	"solvify-agent/internal/llm"
	requestdto "solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/rag"
	"solvify-agent/pkg/config"
	"solvify-agent/pkg/logger"
)

// ─── 快速检索模式 ───────────────────────────────────────────

// processMessage 处理消息的核心流程（快速检索模式）
//
// 速度优化策略：
// 1. 仅当问题含指代/省略时才触发 LLM 查询改写，否则跳过改写直接检索
// 2. 需要改写时：改写与原始检索并行；改写有变化则以改写结果为准（不污染 merge）
// 3. history 剔除本轮刚落库的 user 消息，避免 Prompt 重复
func (s *chatService) processMessage(ctx context.Context, userID, sessionID string, req requestdto.SendMessageRequest, eventCh chan<- dto.StreamEvent) {
	// Step 1: 加载模型 + 增强历史对话
	sendProgressEvent(eventCh, "正在加载上下文...")
	client, enhancedCtx, err := s.initContext(ctx, userID, sessionID, req.ModelID, req.ModelType, req.Content)
	if err != nil {
		sendErrorEvent(eventCh, err, err.Error())
		return
	}
	// 剔除本轮刚保存的 user 消息，避免 buildMessages 中问题重复
	history := excludeCurrentUserMessage(enhancedCtx.History, req.Content)

	chatModel := client.ChatModel()

	// Step 2+3: 检索（条件改写，优先速度）
	sendProgressEvent(eventCh, "正在检索知识库...")

	var sources []dto.SourceInfo
	var retrieveResult rag.Result

	needRewrite := len(history) > 0 && needsQueryRewrite(req.Content)
	if needRewrite {
		// 并行：查询改写 + 原始查询先行检索
		g, gCtx := errgroup.WithContext(ctx)

		var rewrittenQuery string
		g.Go(func() error {
			rewritten, err := s.rewriteQuery(gCtx, chatModel, history, req.Content)
			if err != nil {
				logger.Warnf("查询改写失败，使用原始问题, sessionID=%s: %v", sessionID, err)
				return nil // 改写失败不阻断流程
			}
			rewrittenQuery = strings.TrimSpace(rewritten)
			return nil
		})

		g.Go(func() error {
			var retrieveErr error
			sources, retrieveResult, retrieveErr = s.retrieveContext(gCtx, userID, req.Content, req.KnowledgeBaseIDs)
			return retrieveErr
		})

		if err := g.Wait(); err != nil {
			logger.Errorf("知识库检索失败, sessionID=%s: %v", sessionID, err)
			sendErrorEvent(eventCh, err, "知识库检索失败")
			return
		}

		// 改写有变化时：以改写检索结果为准（避免指代误召回污染上下文）
		if rewrittenQuery != "" && !strings.EqualFold(rewrittenQuery, strings.TrimSpace(req.Content)) {
			logger.Infof("查询已改写: %q -> %q，使用改写结果覆盖检索", req.Content, rewrittenQuery)
			sources2, result2, err2 := s.retrieveContext(ctx, userID, rewrittenQuery, req.KnowledgeBaseIDs)
			if err2 == nil {
				sources, retrieveResult = sources2, result2
			} else {
				logger.Warnf("改写查询检索失败，保留原始检索结果: %v", err2)
			}
		}
	} else {
		sources, retrieveResult, err = s.retrieveContext(ctx, userID, strings.TrimSpace(req.Content), req.KnowledgeBaseIDs)
		if err != nil {
			logger.Errorf("知识库检索失败, sessionID=%s: %v", sessionID, err)
			sendErrorEvent(eventCh, err, "知识库检索失败")
			return
		}
	}

	// Step 4: 组装 Prompt（用原始问题，改写后的查询仅用于检索）
	sendProgressEvent(eventCh, "正在整理资料...")
	messages := buildMessages(history, req.Content, retrieveResult, enhancedCtx.Summary, enhancedCtx.Memories, enhancedCtx.RetrievalBudget)

	// Step 5: LLM 流式生成
	assistantMsgID := uuid.New().String()
	fullContent, err := s.streamAndCollect(ctx, chatModel, messages, assistantMsgID, eventCh)

	// Step 6: 保存助手消息
	// LLM 流式生成失败时已发送 error 事件，此处直接返回，避免再发 done 导致前端重复显示
	if err != nil {
		llm.ReduceContextBudgetOnError(req.ModelID, err)
		return
	}
	// 即使用户中断导致空内容，也结束 SSE（避免前端挂起）
	if fullContent == "" {
		eventCh <- dto.StreamEvent{Type: "done", MessageID: assistantMsgID, Content: "", Sources: sources, Done: true}
		return
	}
	s.emitDoneAndSave(eventCh, sessionID, assistantMsgID, fullContent, req, sources, nil)

	// Step 7: 异步更新摘要和提取记忆
	s.refreshContextAsync(ctx, userID, sessionID, enhancedCtx.History, chatModel)
}

// ─── 深度思考模式 ───────────────────────────────────────────

// processDeepMode 深度思考模式处理流程
// 使用 eino ReAct Agent，自动管理 Think → Act → Observe 循环
func (s *chatService) processDeepMode(ctx context.Context, userID, sessionID string, req requestdto.SendMessageRequest, eventCh chan<- dto.StreamEvent) {
	// 提前生成助手消息 ID，贯穿整个 SSE 生命周期
	assistantMsgID := uuid.New().String()

	// Step 1: 加载模型 + 增强历史对话
	sendProgressEvent(eventCh, "正在加载上下文...")
	client, enhancedCtx, err := s.initContext(ctx, userID, sessionID, req.ModelID, req.ModelType, req.Content)
	if err != nil {
		sendErrorEvent(eventCh, err, err.Error())
		return
	}
	history := excludeCurrentUserMessage(enhancedCtx.History, req.Content)

	chatModel := client.ChatModel()

	// Step 2: 发送 start 事件（前端用 message_id 关联后续更新）
	eventCh <- dto.StreamEvent{Type: "start", MessageID: assistantMsgID}

	// Step 3: 委托 eino ReAct Agent 执行
	sendProgressEvent(eventCh, "正在深度推理...")
	agentEventCh, err := s.agentEngine.Execute(ctx, agent.Request{
		UserID:           userID,
		Query:            req.Content,
		History:          history,
		KnowledgeBaseIDs: req.KnowledgeBaseIDs,
		ModelID:          req.ModelID,
		ModelType:        req.ModelType,
	}, chatModel)
	if err != nil {
		logger.Errorf("Agent 执行失败, sessionID=%s: %v", sessionID, err)
		llm.ReduceContextBudgetOnError(req.ModelID, err)
		sendErrorEvent(eventCh, err, "Agent 执行失败")
		return
	}

	// Step 4: 转发 Agent 事件到 SSE 事件流 + 收集推理步骤和最终答案
	var fullContent string
	var agentSources []dto.SourceInfo
	var reasoningSteps []dto.ReasoningStep
	toolEventSeen := false
	agentErrorSeen := false

	for agentEvent := range agentEventCh {
		// Agent 的 done 事件仅用于收集完整答案，不透传到 SSE
		// （SSE 的终止由 Service 层的 emitDoneAndSave 统一负责）
		if agentEvent.Type == agent.EventDone {
			if agentEvent.Content != "" {
				fullContent = agentEvent.Content
			}
			if len(agentEvent.Sources) > 0 {
				agentSources = agentEvent.Sources
			}
			continue
		}

		// 实时累积答案内容，确保用户中断时也能保存已生成的部分
		if agentEvent.Type == agent.EventAnswer {
			fullContent += agentEvent.Content
		}
		if agentEvent.Type == agent.EventToolCall || agentEvent.Type == agent.EventToolResult {
			toolEventSeen = true
		}
		if agentEvent.Type == agent.EventError {
			agentErrorSeen = true
			llm.ReduceContextBudgetOnError(req.ModelID, fmt.Errorf("%s", agentEvent.Error))
		}

		eventCh <- toStreamEvent(agentEvent)

		if len(agentEvent.Sources) > 0 {
			agentSources = agentEvent.Sources
		}
		applyReasoningStep(&reasoningSteps, agentEvent)
	}

	// Step 5: 保存助手消息（含推理步骤）
	if agentErrorSeen {
		return
	}
	if !toolEventSeen && looksLikeExecutionPlan(fullContent) {
		logger.Warnf("深度模式未产生工具调用，仅返回执行计划，sessionID=%s, content=%q", sessionID, fullContent)
		eventCh <- dto.StreamEvent{
			Type:      "error",
			Title:     "深度推理未完成",
			Detail:    "当前模型没有正确发起工具调用，请重试或切换支持工具调用的模型",
			Retryable: true,
			Done:      true,
		}
		return
	}
	// 用户中断时可能没有最终答案，但有推理步骤也应保存
	if fullContent == "" && len(reasoningSteps) == 0 {
		return
	}

	var metadata datatypes.JSON
	if len(reasoningSteps) > 0 {
		metadata = datatypes.JSON(mustMarshal(map[string]any{
			"reasoning_steps": reasoningSteps,
		}))
	}
	s.emitDoneAndSave(eventCh, sessionID, assistantMsgID, fullContent, req, agentSources, metadata)

	// 异步更新摘要和提取记忆
	s.refreshContextAsync(ctx, userID, sessionID, enhancedCtx.History, chatModel)
}

// refreshContextAsync 异步更新会话摘要和提取用户记忆
func (s *chatService) refreshContextAsync(ctx context.Context, userID, sessionID string, history []entity.ChatMessage, chatModel model.BaseChatModel) {
	if s.contextSvc == nil {
		return
	}
	go func() {
		refreshCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if _, err := s.contextSvc.SummarizeSession(refreshCtx, sessionID, chatModel); err != nil {
			logger.Warnf("生成会话摘要失败: %v", err)
		}
		if _, err := s.contextSvc.ExtractMemories(refreshCtx, userID, sessionID, history, chatModel); err != nil {
			logger.Warnf("提取用户记忆失败: %v", err)
		}
	}()
}

// ─── 共享辅助方法 ───────────────────────────────────────────

// emitDoneAndSave 发送 done 事件并异步保存助手消息
// 注意：保存失败只记日志，禁止再向 eventCh 写事件（外层 defer close 后会 panic）
func (s *chatService) emitDoneAndSave(eventCh chan<- dto.StreamEvent, sessionID, msgID, content string, req requestdto.SendMessageRequest, sources []dto.SourceInfo, metadata datatypes.JSON) {
	eventCh <- dto.StreamEvent{Type: "done", MessageID: msgID, Content: content, Sources: sources, Done: true}
	go func() {
		saveCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.saveAssistantMessage(saveCtx, sessionID, msgID, content, req, sources, metadata); err != nil {
			logger.Errorf("保存助手消息失败, messageID=%s: %v", msgID, err)
		}
	}()
}

// rewriteQuery 用 LLM 结合历史对话改写用户问题，生成更适合检索的独立查询
func (s *chatService) rewriteQuery(ctx context.Context, chatModel interface {
	Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error)
}, history []entity.ChatMessage, question string) (string, error) {
	// 快速模式：改写超时收紧，避免拖慢首 token
	rewriteCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	messages := buildRewritePrompt(history, question)
	msg, err := chatModel.Generate(rewriteCtx, messages)
	if err != nil {
		return "", err
	}
	if msg == nil {
		return "", nil
	}
	return strings.TrimSpace(msg.Content), nil
}

// excludeCurrentUserMessage 去掉 history 末尾与本轮问题相同的 user 消息
// SendMessage 会先落库 user 消息，FindRecent 会把它带回 history
func excludeCurrentUserMessage(history []entity.ChatMessage, current string) []entity.ChatMessage {
	if len(history) == 0 {
		return history
	}
	last := history[len(history)-1]
	if last.Role == "user" && strings.TrimSpace(last.Content) == strings.TrimSpace(current) {
		return history[:len(history)-1]
	}
	return history
}

// needsQueryRewrite 启发式判断是否需要 LLM 改写（指代/省略）
// 无指代时直接跳过改写，省掉 1 次 LLM 调用，显著加快快速检索
func needsQueryRewrite(question string) bool {
	q := strings.TrimSpace(question)
	if q == "" {
		return false
	}
	// 注意：不要用单字「这/那/其」等过宽匹配，会误伤大量正常问句
	pronouns := []string{
		"它", "他", "她", "这个", "那个", "这些", "那些", "上面", "上述", "前面", "刚才",
		"该问题", "该方法", "该方案", "其优势", "其缺点", "其原理",
		"怎么样", "如何呢", "怎么说", "呢？", "呢?",
		"it ", " this", " that", " these", " those", " they", " them", "the above",
	}
	lower := strings.ToLower(q)
	for _, p := range pronouns {
		if strings.Contains(lower, strings.ToLower(p)) {
			return true
		}
	}
	// 极短追问（如“为什么？”“呢？”）也需要上下文补全
	if utf8.RuneCountInString(q) <= 6 {
		return true
	}
	return false
}

// retrieveContext 执行 RAG 检索并转换为引用来源
func (s *chatService) retrieveContext(ctx context.Context, userID, question string, knowledgeBaseIDs []string) ([]dto.SourceInfo, rag.Result, error) {
	logger.Infof("RAG 检索开始: userID=%s, question=%q, kbIDs=%v", userID, question, knowledgeBaseIDs)
	ragCfg := config.Get().RAG
	// 有 Rerank 时扩大召回量，让重排有足够候选；无 Rerank 时直接用 TopK 保证速度
	topK := ragCfg.TopK
	if ragCfg.Reranker.Enabled {
		if ragCfg.RecallK > 0 {
			topK = ragCfg.RecallK
		} else if topK > 0 {
			topK = topK * 5
		} else {
			topK = 20
		}
	}
	retrieveResult, err := s.retriever.Retrieve(ctx, rag.Query{
		Question:         question,
		TopK:             topK,
		KnowledgeBaseIDs: knowledgeBaseIDs,
		UserID:           userID,
	})
	if err != nil {
		return nil, rag.Result{}, err
	}

	sources := groupDocumentsToSources(retrieveResult.Documents)

	logger.Infof("RAG 检索完成: hit=%v, 命中 %d 篇文档, 共 %d 个 chunk",
		retrieveResult.Hit, len(sources), len(retrieveResult.Documents))
	return sources, retrieveResult, nil
}

// streamAndCollect 流式生成并推送 SSE 事件，返回已收集的内容（用户暂停时返回部分内容）
func (s *chatService) streamAndCollect(ctx context.Context, chatModel interface {
	Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error)
}, messages []*schema.Message, assistantMsgID string, eventCh chan<- dto.StreamEvent) (string, error) {
	sendProgressEvent(eventCh, "正在生成回答...")
	streamReader, err := chatModel.Stream(ctx, messages)
	if err != nil {
		logger.Errorf("LLM 调用失败: %v", err)
		sendErrorEvent(eventCh, err, "LLM 调用失败")
		return "", err
	}
	defer streamReader.Close()

	eventCh <- dto.StreamEvent{
		Type:      "start",
		MessageID: assistantMsgID,
	}

	var fullContent string
	for {
		msg, recvErr := streamReader.Recv()
		if recvErr != nil {
			if recvErr == io.EOF {
				break
			}
			if ctx.Err() != nil {
				logger.Infof("用户暂停，已收集 %d 字符", len(fullContent))
				return fullContent, recvErr
			}
			logger.Errorf("LLM 流式生成错误: %v", recvErr)
			sendErrorEvent(eventCh, recvErr, "LLM 流式生成错误")
			return fullContent, recvErr
		}
		if msg == nil || msg.Content == "" {
			continue
		}
		fullContent += msg.Content
		eventCh <- dto.StreamEvent{
			Type:    "content",
			Content: msg.Content,
		}
	}

	return fullContent, nil
}

// toStreamEvent 将 Agent 事件转换为 SSE 流式事件
func toStreamEvent(e agent.Event) dto.StreamEvent {
	se := dto.StreamEvent{
		Type:      e.Type,
		Title:     e.Title,
		Detail:    e.Detail,
		Status:    e.Status,
		Content:   e.Content,
		Error:     e.Error,
		Done:      e.Done,
		Retryable: e.Retryable,
	}
	if len(e.Sources) > 0 {
		se.Sources = e.Sources
	}
	// citation 事件的字段映射
	if e.Type == agent.EventCitation {
		se.CitationID = e.CitationID
		se.CitationChunkID = e.CitationChunkID
		se.CitationFileName = e.CitationFileName
		se.CitationContent = e.CitationContent
	}
	return se
}

func looksLikeExecutionPlan(content string) bool {
	text := strings.TrimSpace(content)
	if text == "" {
		return false
	}
	if len([]rune(text)) > 120 {
		return false
	}
	planPrefixes := []string{"我会先", "我将先", "我先", "先查一下", "我会查", "我将查"}
	for _, prefix := range planPrefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

// applyReasoningStep 从 Agent 事件更新推理步骤列表（用于持久化）
//
// thinking (running) → 追加新步骤
// thinking (success) → 将上一个 matching 步骤标记为 success（不再追加新条目）
// plan / tool_call / tool_result / warning → 直接追加
func applyReasoningStep(steps *[]dto.ReasoningStep, e agent.Event) {
	switch e.Type {
	case agent.EventThinking:
		if e.Status == "success" {
			// 反向查找最后一条 thinking running 步骤，将其标记为 success
			for i := len(*steps) - 1; i >= 0; i-- {
				if (*steps)[i].Type == agent.EventThinking && (*steps)[i].Status == "running" {
					(*steps)[i].Status = "success"
					break
				}
			}
			return
		}
		*steps = append(*steps, dto.ReasoningStep{
			Type:    e.Type,
			Content: e.Title,
			Detail:  e.Detail,
			Status:  e.Status,
		})
	case agent.EventToolCall, agent.EventToolResult, agent.EventWarning:
		*steps = append(*steps, dto.ReasoningStep{
			Type:    e.Type,
			Content: e.Title,
			Detail:  e.Detail,
			Status:  e.Status,
		})
	}
}
