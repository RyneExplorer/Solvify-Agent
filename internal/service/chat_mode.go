package service

import (
	"context"
	"io"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
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
func (s *chatService) processMessage(ctx context.Context, userID, sessionID string, req requestdto.SendMessageRequest, eventCh chan<- dto.StreamEvent) {
	// Step 1: 并行加载模型 + 历史对话
	eventCh <- dto.StreamEvent{Type: "progress", Content: "正在加载上下文..."}
	client, history, err := s.initContext(ctx, userID, sessionID, req.ModelID, req.ModelType, 600)
	if err != nil {
		eventCh <- dto.StreamEvent{Type: "error", Error: err.Error(), Done: true}
		return
	}

	// Step 2: 查询改写（用历史 + LLM 改写为独立检索查询）
	searchQuery := req.Content
	chatModel := client.ChatModel()
	if len(history) > 0 {
		eventCh <- dto.StreamEvent{Type: "progress", Content: "正在理解问题..."}
		rewritten, err := s.rewriteQuery(ctx, chatModel, history, req.Content)
		if err != nil {
			logger.Warnf("查询改写失败，使用原始问题, sessionID=%s: %v", sessionID, err)
		} else if rewritten != "" {
			searchQuery = rewritten
		}
	}

	// Step 3: RAG 检索（用改写后的查询做 embedding + 向量检索）
	eventCh <- dto.StreamEvent{Type: "progress", Content: "正在检索知识库..."}
	sources, retrieveResult, err := s.retrieveContext(ctx, userID, searchQuery, req.KnowledgeBaseIDs)
	if err != nil {
		logger.Errorf("知识库检索失败, sessionID=%s: %v", sessionID, err)
		eventCh <- dto.StreamEvent{Type: "error", Error: "知识库检索失败", Done: true}
		return
	}

	// Step 4: 组装 Prompt（用原始问题，改写后的查询仅用于检索）
	eventCh <- dto.StreamEvent{Type: "progress", Content: "正在整理资料..."}
	messages := buildMessages(history, req.Content, retrieveResult)

	// Step 5: LLM 流式生成
	assistantMsgID := uuid.New().String()
	fullContent, err := s.streamAndCollect(ctx, chatModel, messages, assistantMsgID, eventCh)

	// Step 6: 保存助手消息
	if err != nil && fullContent == "" {
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
	// Step 1: 并行加载模型 + 历史对话
	eventCh <- dto.StreamEvent{Type: "progress", Content: "正在加载上下文..."}
	client, history, err := s.initContext(ctx, userID, sessionID, req.ModelID, req.ModelType, 2000)
	if err != nil {
		eventCh <- dto.StreamEvent{Type: "error", Error: err.Error(), Done: true}
		return
	}

	// Step 2: 委托 eino ReAct Agent 执行
	eventCh <- dto.StreamEvent{Type: "progress", Content: "正在深度推理..."}
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
		eventCh <- dto.StreamEvent{Type: "error", Error: "深度思考模式执行失败", Done: true}
		return
	}

	// Step 3: 转发 Agent 事件到 SSE 事件流 + 收集推理步骤和最终答案
	var fullContent string
	var agentSources []dto.SourceInfo
	var reasoningSteps []dto.ReasoningStep

	for agentEvent := range agentEventCh {
		eventCh <- toStreamEvent(agentEvent)

		if len(agentEvent.Sources) > 0 {
			agentSources = agentEvent.Sources
		}
		if step := collectReasoningStep(agentEvent); step != nil {
			reasoningSteps = append(reasoningSteps, *step)
		}
		// done 事件的 Content 是完整答案（包含 <kb> 标签）
		if agentEvent.Type == agent.EventDone && agentEvent.Content != "" {
			fullContent = agentEvent.Content
		}
	}

	// Step 4: 保存助手消息（含推理步骤）
	if fullContent == "" {
		return
	}
	var metadata datatypes.JSON
	if len(reasoningSteps) > 0 {
		metadata = datatypes.JSON(mustMarshal(map[string]any{
			"reasoning_steps": reasoningSteps,
		}))
	}
	s.emitDoneAndSave(eventCh, sessionID, uuid.New().String(), fullContent, req, agentSources, metadata)
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
	eventCh <- dto.StreamEvent{Type: "progress", Content: "正在生成回答..."}
	streamReader, err := chatModel.Stream(ctx, messages)
	if err != nil {
		logger.Errorf("LLM 调用失败: %v", err)
		eventCh <- dto.StreamEvent{Type: "error", Error: "LLM 调用失败", Done: true}
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
			eventCh <- dto.StreamEvent{Type: "error", Error: recvErr.Error(), Done: true}
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
		Type:    e.Type,
		Title:   e.Title,
		Detail:  e.Detail,
		Status:  e.Status,
		Content: e.Content,
		Error:   e.Error,
		Done:    e.Done,
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

// collectReasoningStep 从 Agent 事件中收集推理步骤（用于持久化）
func collectReasoningStep(e agent.Event) *dto.ReasoningStep {
	switch e.Type {
	case agent.EventThinking, agent.EventPlan, agent.EventToolCall, agent.EventToolResult, agent.EventWarning:
		return &dto.ReasoningStep{Type: e.Type, Content: e.Title}
	default:
		return nil
	}
}
