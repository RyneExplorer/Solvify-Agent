package service

import (
	"context"
	"io"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
	"gorm.io/datatypes"

	"solvify-agent/internal/agent"
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
// 优化：查询改写（LLM 调用）与 RAG 检索并行执行。
// 先用原始查询立即启动检索（~500ms），同时后台执行改写（~1-3s）。
// 改写完成后若查询有变化，补充检索并合并结果，兼顾速度与召回质量。
func (s *chatService) processMessage(ctx context.Context, userID, sessionID string, req requestdto.SendMessageRequest, eventCh chan<- dto.StreamEvent) {
	// Step 1: 并行加载模型 + 历史对话
	sendProgressEvent(eventCh, "正在加载上下文...")
	client, history, err := s.initContext(ctx, userID, sessionID, req.ModelID, req.ModelType, 600)
	if err != nil {
		sendErrorEvent(eventCh, err, err.Error())
		return
	}

	chatModel := client.ChatModel()
	searchQuery := req.Content

	// Step 2+3: 查询改写与 RAG 检索并行（快速路径优化）
	sendProgressEvent(eventCh, "正在检索知识库...")

	var sources []dto.SourceInfo
	var retrieveResult rag.Result

	if len(history) > 0 {
		// 并行：查询改写 + 原始查询先行检索
		g, gCtx := errgroup.WithContext(ctx)

		var rewrittenQuery string
		g.Go(func() error {
			rewritten, err := s.rewriteQuery(gCtx, chatModel, history, req.Content)
			if err != nil {
				logger.Warnf("查询改写失败，使用原始问题, sessionID=%s: %v", sessionID, err)
				return nil // 改写失败不阻断流程
			}
			if rewritten != "" {
				rewrittenQuery = rewritten
			}
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

		// 改写查询有变化且原始检索结果较少时，用改写查询补充检索
		if rewrittenQuery != "" && rewrittenQuery != req.Content &&
			(!retrieveResult.Hit || len(retrieveResult.Documents) < config.Get().RAG.TopK) {
			logger.Infof("原始检索结果不足 (%d 条)，用改写查询 %q 补充检索", len(retrieveResult.Documents), rewrittenQuery)
			sources2, result2, err2 := s.retrieveContext(ctx, userID, rewrittenQuery, req.KnowledgeBaseIDs)
			if err2 == nil && result2.Hit {
				sources, retrieveResult = mergeRetrieveResults(sources, retrieveResult, sources2, result2)
			}
		}
	} else {
		sendProgressEvent(eventCh, "正在检索知识库...")
		sources, retrieveResult, err = s.retrieveContext(ctx, userID, searchQuery, req.KnowledgeBaseIDs)
		if err != nil {
			logger.Errorf("知识库检索失败, sessionID=%s: %v", sessionID, err)
			sendErrorEvent(eventCh, err, "知识库检索失败")
			return
		}
	}

	// Step 4: 组装 Prompt（用原始问题，改写后的查询仅用于检索）
	sendProgressEvent(eventCh, "正在整理资料...")
	messages := buildMessages(history, req.Content, retrieveResult)

	// Step 5: LLM 流式生成
	assistantMsgID := uuid.New().String()
	fullContent, err := s.streamAndCollect(ctx, chatModel, messages, assistantMsgID, eventCh)

	// Step 6: 保存助手消息
	// LLM 流式生成失败时已发送 error 事件，此处直接返回，避免再发 done 导致前端重复显示
	if err != nil {
		return
	}
	if fullContent != "" {
		s.emitDoneAndSave(eventCh, sessionID, assistantMsgID, fullContent, req, sources, nil)
	}
}

// ─── 深度思考模式 ───────────────────────────────────────────

// processDeepMode 深度思考模式处理流程
// 使用 eino ReAct Agent，自动管理 Think → Act → Observe 循环
func (s *chatService) processDeepMode(ctx context.Context, userID, sessionID string, req requestdto.SendMessageRequest, eventCh chan<- dto.StreamEvent) {
	// 提前生成助手消息 ID，贯穿整个 SSE 生命周期
	assistantMsgID := uuid.New().String()

	// Step 1: 并行加载模型 + 历史对话
	sendProgressEvent(eventCh, "正在加载上下文...")
	client, history, err := s.initContext(ctx, userID, sessionID, req.ModelID, req.ModelType, 2000)
	if err != nil {
		sendErrorEvent(eventCh, err, err.Error())
		return
	}

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
	}, client.ChatModel())
	if err != nil {
		logger.Errorf("Agent 执行失败, sessionID=%s: %v", sessionID, err)
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
}

// ─── 共享辅助方法 ───────────────────────────────────────────

// emitDoneAndSave 发送 done 事件并异步保存助手消息
func (s *chatService) emitDoneAndSave(eventCh chan<- dto.StreamEvent, sessionID, msgID, content string, req requestdto.SendMessageRequest, sources []dto.SourceInfo, metadata datatypes.JSON) {
	eventCh <- dto.StreamEvent{Type: "done", MessageID: msgID, Content: content, Sources: sources, Done: true}
	go func() {
		saveCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.saveAssistantMessage(saveCtx, sessionID, msgID, content, req, sources, metadata); err != nil {
			logger.Errorf("保存助手消息失败, messageID=%s: %v", msgID, err)
			sendWarningEvent(eventCh, "消息保存失败", "回答已显示但可能未保存，请刷新页面确认")
		}
	}()
}

// rewriteQuery 用 LLM 结合历史对话改写用户问题，生成更适合检索的独立查询
func (s *chatService) rewriteQuery(ctx context.Context, chatModel interface {
	Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error)
}, history []entity.ChatMessage, question string) (string, error) {
	rewriteCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	messages := buildRewritePrompt(history, question)
	msg, err := chatModel.Generate(rewriteCtx, messages)
	if err != nil {
		return "", err
	}
	if msg == nil {
		return "", nil
	}
	return msg.Content, nil
}

// retrieveContext 执行 RAG 检索并转换为引用来源
func (s *chatService) retrieveContext(ctx context.Context, userID, question string, knowledgeBaseIDs []string) ([]dto.SourceInfo, rag.Result, error) {
	logger.Infof("RAG 检索开始: userID=%s, question=%q, kbIDs=%v", userID, question, knowledgeBaseIDs)
	retrieveResult, err := s.retriever.Retrieve(ctx, rag.Query{
		Question:         question,
		TopK:             config.Get().RAG.TopK,
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

// mergeRetrieveResults 合并两次检索结果，按 chunk ID 去重
// 改写查询的结果排前面（相关性更高），原始查询结果补充在后
func mergeRetrieveResults(originalSources []dto.SourceInfo, originalResult rag.Result,
	supplementSources []dto.SourceInfo, supplementResult rag.Result) ([]dto.SourceInfo, rag.Result) {

	seenChunkIDs := make(map[string]bool)
	mergedDocs := make([]rag.Document, 0, len(supplementResult.Documents)+len(originalResult.Documents))

	// 改写查询结果优先
	for _, doc := range supplementResult.Documents {
		if !seenChunkIDs[doc.ID] {
			seenChunkIDs[doc.ID] = true
			mergedDocs = append(mergedDocs, doc)
		}
	}

	// 原始查询结果补充
	for _, doc := range originalResult.Documents {
		if !seenChunkIDs[doc.ID] {
			seenChunkIDs[doc.ID] = true
			mergedDocs = append(mergedDocs, doc)
		}
	}

	// 合并 sources（按 document title 去重，改写查询的排前面）
	mergedSources := make([]dto.SourceInfo, 0, len(supplementSources)+len(originalSources))
	seenTitles := make(map[string]bool)

	for _, src := range supplementSources {
		if !seenTitles[src.Title] {
			seenTitles[src.Title] = true
			mergedSources = append(mergedSources, src)
		}
	}
	for _, src := range originalSources {
		if !seenTitles[src.Title] {
			seenTitles[src.Title] = true
			mergedSources = append(mergedSources, src)
		}
	}

	return mergedSources, rag.Result{
		Hit:       len(mergedDocs) > 0,
		Documents: mergedDocs,
	}
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
	case agent.EventPlan, agent.EventToolCall, agent.EventToolResult, agent.EventWarning:
		*steps = append(*steps, dto.ReasoningStep{
			Type:    e.Type,
			Content: e.Title,
			Detail:  e.Detail,
			Status:  e.Status,
		})
	}
}
