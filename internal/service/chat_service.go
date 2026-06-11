package service

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"gorm.io/datatypes"

	"golang.org/x/sync/errgroup"

	"solvify-agent/internal/agent"
	"solvify-agent/internal/llm"
	requestdto "solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/rag"
	"solvify-agent/internal/repository"
	"solvify-agent/pkg/config"
	apperrors "solvify-agent/pkg/errors"
	"solvify-agent/pkg/logger"
)

const (
	sessionStatusActive = "active"
	sessionStatusClosed = "closed"
)

// chatService 封装聊天业务用例实现
type chatService struct {
	sessionRepo         repository.ChatSessionRepo
	messageRepo         repository.ChatMessageRepo
	retriever           rag.Retriever
	modelRepo           repository.ModelRepo
	userModelConfigRepo repository.UserModelConfigRepo
	agentEngine         *agent.Engine
}

// NewChatService 创建聊天业务服务
func NewChatService(
	sessionRepo repository.ChatSessionRepo,
	messageRepo repository.ChatMessageRepo,
	retriever rag.Retriever,
	modelRepo repository.ModelRepo,
	userModelConfigRepo repository.UserModelConfigRepo,
	agentEngine *agent.Engine,
) ChatServiceInterface {
	return &chatService{
		sessionRepo:         sessionRepo,
		messageRepo:         messageRepo,
		retriever:           retriever,
		modelRepo:           modelRepo,
		userModelConfigRepo: userModelConfigRepo,
		agentEngine:         agentEngine,
	}
}

// CreateSession 创建聊天会话
func (s *chatService) CreateSession(ctx context.Context, userID string, req requestdto.CreateSessionRequest) (dto.SessionResponse, error) {
	session := entity.ChatSession{
		ID:      uuid.New().String(),
		UserID:  userID,
		Title:   req.Title,
		ModelID: req.ModelID,
		Status:  sessionStatusActive,
	}

	if err := s.sessionRepo.Create(ctx, &session); err != nil {
		return dto.SessionResponse{}, fmt.Errorf("创建会话失败: %w", err)
	}

	return sessionResponse(session), nil
}

// GetSession 获取会话详情
func (s *chatService) GetSession(ctx context.Context, userID, sessionID string) (dto.SessionResponse, error) {
	session, err := s.sessionRepo.FindByID(ctx, sessionID)
	if err != nil {
		return dto.SessionResponse{}, apperrors.NewDefault(apperrors.CodeSessionNotFound)
	}
	if session.UserID != userID {
		return dto.SessionResponse{}, apperrors.NewDefault(apperrors.CodeSessionNotFound)
	}
	return sessionResponse(*session), nil
}

// ListSessions 获取用户会话列表
func (s *chatService) ListSessions(ctx context.Context, userID string) ([]dto.SessionResponse, error) {
	sessions, err := s.sessionRepo.ListByUserID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("查询会话列表失败: %w", err)
	}

	results := make([]dto.SessionResponse, 0, len(sessions))
	for _, session := range sessions {
		results = append(results, sessionResponse(session))
	}
	return results, nil
}

// UpdateSessionTitle 更新会话标题
func (s *chatService) UpdateSessionTitle(ctx context.Context, userID, sessionID string, req requestdto.UpdateSessionRequest) error {
	if err := s.validateSession(ctx, userID, sessionID); err != nil {
		return err
	}
	if err := s.sessionRepo.UpdateTitle(ctx, sessionID, req.Title); err != nil {
		return fmt.Errorf("更新会话标题失败: %w", err)
	}
	return nil
}

// DeleteSession 删除会话及其所有消息
func (s *chatService) DeleteSession(ctx context.Context, userID, sessionID string) error {
	if err := s.validateSession(ctx, userID, sessionID); err != nil {
		return err
	}
	// 先删消息，再删会话
	if err := s.messageRepo.DeleteBySessionID(ctx, sessionID); err != nil {
		return fmt.Errorf("删除会话消息失败: %w", err)
	}
	if err := s.sessionRepo.Delete(ctx, sessionID); err != nil {
		return fmt.Errorf("删除会话失败: %w", err)
	}
	return nil
}

// SendMessage 发送消息并获取流式响应
func (s *chatService) SendMessage(ctx context.Context, userID, sessionID string, req requestdto.SendMessageRequest) (<-chan dto.StreamEvent, error) {
	// 验证会话归属
	if err := s.validateSession(ctx, userID, sessionID); err != nil {
		return nil, err
	}

	// 保存用户消息
	if err := s.saveUserMessage(ctx, sessionID, req); err != nil {
		return nil, err
	}

	// 创建流式响应通道
	eventCh := make(chan dto.StreamEvent, 100)

	go func() {
		defer close(eventCh)
		if req.SearchMode == "smart-reasoning" {
			s.processDeepMode(ctx, userID, sessionID, req, eventCh)
		} else {
			s.processMessage(ctx, userID, sessionID, req, eventCh)
		}
	}()

	return eventCh, nil
}

// processMessage 处理消息的核心流程（可被深度模式复用）
func (s *chatService) processMessage(ctx context.Context, userID, sessionID string, req requestdto.SendMessageRequest, eventCh chan<- dto.StreamEvent) {
	// Step 1 & 2: 并行初始化 LLM 客户端 + 加载历史对话
	eventCh <- dto.StreamEvent{Type: "progress", Content: "正在初始化模型..."}
	var llmClient llm.Client
	var history []entity.ChatMessage
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		client, err := s.resolveLLMClient(gctx, userID, req.ModelID, req.ModelType)
		if err != nil {
			logger.Errorf("模型解析失败, modelID=%s, modelType=%s: %v", req.ModelID, req.ModelType, err)
			return fmt.Errorf("模型配置无效或无权访问")
		}
		llmClient = client
		return nil
	})
	g.Go(func() error {
		msg, err := s.messageRepo.FindRecent(gctx, sessionID, 5)
		if err != nil {
			logger.Errorf("加载历史对话失败, sessionID=%s: %v", sessionID, err)
			return fmt.Errorf("加载历史对话失败")
		}
		history = msg
		return nil
	})
	if err := g.Wait(); err != nil {
		eventCh <- dto.StreamEvent{Type: "error", Error: err.Error(), Done: true}
		return
	}

	// Step 3: 查询改写（用最近 5 条历史 + LLM 改写为独立检索查询）
	searchQuery := req.Content
	if len(history) > 0 {
		rewritten, err := s.rewriteQuery(ctx, llmClient, history, req.Content)
		if err != nil {
			logger.Warnf("查询改写失败，使用原始问题, sessionID=%s: %v", sessionID, err)
		} else if rewritten != "" {
			searchQuery = rewritten
		}
	}

	// Step 4: RAG 检索（用改写后的查询做 embedding + 向量检索）
	eventCh <- dto.StreamEvent{Type: "progress", Content: "正在检索知识库..."}
	sources, retrieveResult, err := s.retrieveContext(ctx, userID, searchQuery, req.KnowledgeBaseIDs)
	if err != nil {
		logger.Errorf("知识库检索失败, sessionID=%s: %v", sessionID, err)
		eventCh <- dto.StreamEvent{Type: "error", Error: "知识库检索失败", Done: true}
		return
	}

	// Step 5: 组装 Prompt（用原始问题，改写后的查询仅用于检索）
	eventCh <- dto.StreamEvent{Type: "progress", Content: "正在整理资料..."}
	messages := buildMessages(history, req.Content, retrieveResult)

	// Step 6: LLM 流式生成
	eventCh <- dto.StreamEvent{Type: "progress", Content: "正在生成回答..."}
	assistantMsgID := uuid.New().String()
	fullContent, err := s.streamAndCollect(ctx, llmClient, messages, assistantMsgID, sources, eventCh)

	// Step 7: 保存助手消息
	if err != nil {
		if fullContent != "" {
			// 用户暂停：保存已生成的部分内容
			logger.Infof("用户暂停，保存部分内容, messageID=%s, chars=%d", assistantMsgID, len(fullContent))
		} else {
			// LLM 调用完全失败，streamAndCollect 内部已发送 error 事件
			return
		}
	}
	if fullContent != "" {
		// 发送完成事件（仅在有内容时才表示完成）
		eventCh <- dto.StreamEvent{
			Type:      "done",
			MessageID: assistantMsgID,
			Done:      true,
		}
		go func() {
			saveCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := s.saveAssistantMessage(saveCtx, sessionID, assistantMsgID, fullContent, req, sources); err != nil {
				logger.Errorf("保存助手消息失败, messageID=%s: %v", assistantMsgID, err)
			}
		}()
	}
}

// processDeepMode 深度思考模式处理流程
// Service 只负责：解析LLM客户端、加载历史、调用Agent、转发事件、保存消息
func (s *chatService) processDeepMode(ctx context.Context, userID, sessionID string, req requestdto.SendMessageRequest, eventCh chan<- dto.StreamEvent) {
	// 解析模型配置并创建 LLM 客户端
	eventCh <- dto.StreamEvent{Type: "progress", Content: "正在初始化模型..."}
	llmClient, err := s.resolveLLMClient(ctx, userID, req.ModelID, req.ModelType)
	if err != nil {
		logger.Errorf("模型解析失败, modelID=%s, modelType=%s: %v", req.ModelID, req.ModelType, err)
		eventCh <- dto.StreamEvent{Type: "error", Error: "模型配置无效或无权访问", Done: true}
		return
	}

	// 加载历史对话
	history, err := s.messageRepo.FindRecent(ctx, sessionID, 5)
	if err != nil {
		logger.Errorf("加载历史对话失败, sessionID=%s: %v", sessionID, err)
		eventCh <- dto.StreamEvent{Type: "error", Error: "加载历史对话失败", Done: true}
		return
	}

	// 委托 Agent 执行深度模式（检索→判断→搜索→ReAct 全在 Agent 层）
	agentEventCh, err := s.agentEngine.Execute(ctx, agent.Request{
		Query:            req.Content,
		UserID:           userID,
		SessionID:        sessionID,
		KnowledgeBaseIDs: req.KnowledgeBaseIDs,
		History:          history,
		LLMClient:        llmClient,
	})
	if err != nil {
		logger.Errorf("Agent 执行失败, sessionID=%s: %v", sessionID, err)
		eventCh <- dto.StreamEvent{Type: "error", Error: "深度思考模式执行失败", Done: true}
		return
	}

	// 转发 Agent 事件到 SSE 事件流
	var fullContent string
	var sources []dto.SourceInfo
	for agentEvent := range agentEventCh {
		streamEvent := dto.StreamEvent{
			Type:    agentEvent.Type,
			Content: agentEvent.Content,
			Error:   agentEvent.Error,
			Done:    agentEvent.Done,
		}
		if len(agentEvent.ToolCalls) > 0 {
			streamEvent.ToolCalls = make([]dto.ToolCallInfo, 0, len(agentEvent.ToolCalls))
			for _, tc := range agentEvent.ToolCalls {
				streamEvent.ToolCalls = append(streamEvent.ToolCalls, dto.ToolCallInfo{
					ID:   tc.ID,
					Name: tc.Name,
				})
			}
		}
		if agentEvent.ToolResult != nil {
			streamEvent.ToolResult = &dto.ToolResultInfo{
				Name:    agentEvent.ToolResult.Name,
				Content: agentEvent.ToolResult.Content,
				Error:   agentEvent.ToolResult.Error,
			}
		}
		if len(agentEvent.Sources) > 0 {
			streamEvent.Sources = agentEvent.Sources
			sources = agentEvent.Sources
		}
		eventCh <- streamEvent

		if agentEvent.Type == "answer" {
			fullContent = agentEvent.Content
		}
	}

	// 保存助手消息
	if fullContent != "" {
		assistantMsgID := uuid.New().String()
		eventCh <- dto.StreamEvent{Type: "done", MessageID: assistantMsgID, Done: true}
		go func() {
			saveCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := s.saveAssistantMessage(saveCtx, sessionID, assistantMsgID, fullContent, req, sources); err != nil {
				logger.Errorf("保存助手消息失败, messageID=%s: %v", assistantMsgID, err)
			}
		}()
	}
}

// validateSession 验证会话归属和状态
func (s *chatService) validateSession(ctx context.Context, userID, sessionID string) error {
	session, err := s.sessionRepo.FindByID(ctx, sessionID)
	if err != nil {
		return apperrors.NewDefault(apperrors.CodeSessionNotFound)
	}
	if session.UserID != userID {
		return apperrors.NewDefault(apperrors.CodeSessionNotFound)
	}
	if session.Status != sessionStatusActive {
		return apperrors.NewDefault(apperrors.CodeSessionClosed)
	}
	return nil
}

// saveUserMessage 保存用户消息并更新计数
func (s *chatService) saveUserMessage(ctx context.Context, sessionID string, req requestdto.SendMessageRequest) error {
	userMsg := entity.ChatMessage{
		ID:               uuid.New().String(),
		SessionID:        sessionID,
		Role:             "user",
		Content:          req.Content,
		SearchMode:       req.SearchMode,
		KnowledgeBaseIDs: datatypes.JSON(mustMarshal(req.KnowledgeBaseIDs)),
	}
	return s.messageRepo.Create(ctx, &userMsg)
}

// resolveLLMClient 根据模型类型解析配置并创建 LLM 客户端
func (s *chatService) resolveLLMClient(ctx context.Context, userID, modelID, modelType string) (llm.Client, error) {
	switch modelType {
	case "user":
		cfg, err := s.userModelConfigRepo.GetByID(ctx, modelID, userID)
		if err != nil {
			return nil, fmt.Errorf("查询用户模型配置失败: %w", err)
		}
		return llm.NewClientFromModelConfig(ctx, llm.ModelConfig{
			Provider: cfg.APIFormat,
			ModelID:  cfg.ModelID,
			BaseURL:  cfg.BaseURL,
			APIKey:   cfg.APIKey,
		})
	case "system":
		model, err := s.modelRepo.GetByID(ctx, modelID)
		if err != nil {
			return nil, fmt.Errorf("查询系统模型失败: %w", err)
		}
		return llm.NewClientFromModelConfig(ctx, llm.ModelConfig{
			Provider: model.Provider,
			ModelID:  model.ModelID,
			BaseURL:  model.BaseURL,
			APIKey:   model.APIKey,
		})
	default:
		return nil, fmt.Errorf("不支持的模型类型: %s", modelType)
	}
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

	// 按文档分组
	docMap := make(map[string]*dto.SourceInfo)
	docOrder := make([]string, 0)
	for _, doc := range retrieveResult.Documents {
		if _, exists := docMap[doc.DocumentID]; !exists {
			docMap[doc.DocumentID] = &dto.SourceInfo{
				DocumentID:      doc.DocumentID,
				KnowledgeBaseID: doc.KnowledgeBaseID,
				Title:           doc.Title,
			}
			docOrder = append(docOrder, doc.DocumentID)
		}
		docMap[doc.DocumentID].Chunks = append(docMap[doc.DocumentID].Chunks, dto.ChunkSource{
			ID:      doc.ID,
			Content: doc.Content,
			Score:   doc.Score,
		})
		// 文档分数取最高分
		if doc.Score > docMap[doc.DocumentID].Score {
			docMap[doc.DocumentID].Score = doc.Score
		}
	}

	sources := make([]dto.SourceInfo, 0, len(docMap))
	for _, docID := range docOrder {
		sources = append(sources, *docMap[docID])
	}

	logger.Infof("RAG 检索完成: hit=%v, 命中 %d 篇文档", retrieveResult.Hit, len(sources))
	return sources, retrieveResult, nil
}

// streamAndCollect 流式生成并推送 SSE 事件，返回已收集的内容（用户暂停时返回部分内容）
func (s *chatService) streamAndCollect(ctx context.Context, llmClient llm.Client, messages []*schema.Message, assistantMsgID string, sources []dto.SourceInfo, eventCh chan<- dto.StreamEvent) (string, error) {
	stream, err := llmClient.GenerateStream(ctx, llm.GenerateRequest{Messages: messages})
	if err != nil {
		logger.Errorf("LLM 调用失败: %v", err)
		eventCh <- dto.StreamEvent{Type: "error", Error: "LLM 调用失败", Done: true}
		return "", err
	}

	// 发送开始事件
	eventCh <- dto.StreamEvent{
		Type:      "start",
		Sources:   sources,
		MessageID: assistantMsgID,
	}

	// 收集完整回复
	var fullContent string
	for chunk := range stream {
		if chunk.Error != nil {
			// 用户暂停（context 取消）时返回已收集的内容
			if ctx.Err() != nil {
				logger.Infof("用户暂停，已收集 %d 字符", len(fullContent))
				return fullContent, chunk.Error
			}
			logger.Errorf("LLM 流式生成错误: %v", chunk.Error)
			eventCh <- dto.StreamEvent{Type: "error", Error: chunk.Error.Error(), Done: true}
			return fullContent, chunk.Error
		}
		if chunk.Done {
			break
		}
		fullContent += chunk.Content
		eventCh <- dto.StreamEvent{
			Type:    "content",
			Content: chunk.Content,
		}
	}

	return fullContent, nil
}

// saveAssistantMessage 保存助手消息并更新计数
func (s *chatService) saveAssistantMessage(ctx context.Context, sessionID, msgID, content string, req requestdto.SendMessageRequest, sources []dto.SourceInfo) error {
	assistantMsg := entity.ChatMessage{
		ID:               msgID,
		SessionID:        sessionID,
		Role:             "assistant",
		Content:          content,
		ModelID:          req.ModelID,
		SearchMode:       req.SearchMode,
		KnowledgeBaseIDs: datatypes.JSON(mustMarshal(req.KnowledgeBaseIDs)),
		Sources:          datatypes.JSON(mustMarshal(sources)),
	}
	return s.messageRepo.Create(ctx, &assistantMsg)
}

// GetMessages 获取会话消息列表
func (s *chatService) GetMessages(ctx context.Context, userID, sessionID string) ([]dto.MessageResponse, error) {
	if err := s.validateSession(ctx, userID, sessionID); err != nil {
		return nil, err
	}

	messages, err := s.messageRepo.FindBySessionID(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("查询消息列表失败: %w", err)
	}

	results := make([]dto.MessageResponse, 0, len(messages))
	for _, msg := range messages {
		results = append(results, messageResponse(msg))
	}
	return results, nil
}

// rewriteQuery 用 LLM 结合历史对话改写用户问题，生成更适合检索的独立查询
func (s *chatService) rewriteQuery(ctx context.Context, llmClient llm.Client, history []entity.ChatMessage, question string) (string, error) {
	// 改写调用单独设置超时，避免阻塞太久
	rewriteCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	messages := buildRewritePrompt(history, question)
	resp, err := llmClient.Generate(rewriteCtx, llm.GenerateRequest{Messages: messages})
	if err != nil {
		return "", err
	}
	if resp.Message == nil {
		return "", nil
	}
	return resp.Message.Content, nil
}
