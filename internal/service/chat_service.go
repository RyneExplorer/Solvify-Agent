package service

import (
	"context"
	"fmt"
	"time"

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
	apperrors "solvify-agent/pkg/errors"
	"solvify-agent/pkg/logger"
	"solvify-agent/pkg/tokenutil"
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

// SendMessage 发送消息并获取流式响应
func (s *chatService) SendMessage(ctx context.Context, userID, sessionID string, req requestdto.SendMessageRequest) (<-chan dto.StreamEvent, error) {
	if err := s.validateSession(ctx, userID, sessionID); err != nil {
		return nil, err
	}
	if err := s.saveUserMessage(ctx, sessionID, req); err != nil {
		return nil, err
	}

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

// ─── 会话 CRUD ───────────────────────────────────────────

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

func (s *chatService) UpdateSessionTitle(ctx context.Context, userID, sessionID string, req requestdto.UpdateSessionRequest) error {
	if err := s.validateSession(ctx, userID, sessionID); err != nil {
		return err
	}
	if err := s.sessionRepo.UpdateTitle(ctx, sessionID, req.Title); err != nil {
		return fmt.Errorf("更新会话标题失败: %w", err)
	}
	return nil
}

func (s *chatService) DeleteSession(ctx context.Context, userID, sessionID string) error {
	if err := s.validateSession(ctx, userID, sessionID); err != nil {
		return err
	}
	if err := s.messageRepo.DeleteBySessionID(ctx, sessionID); err != nil {
		return fmt.Errorf("删除会话消息失败: %w", err)
	}
	if err := s.sessionRepo.Delete(ctx, sessionID); err != nil {
		return fmt.Errorf("删除会话失败: %w", err)
	}
	return nil
}

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

// ─── 共享内部方法 ───────────────────────────────────────────

// initChatContext 并行初始化 LLM 客户端和加载历史对话
func (s *chatService) initChatContext(ctx context.Context, userID, sessionID, modelID, modelType string) (llm.Client, []entity.ChatMessage, error) {
	t0 := time.Now()
	var llmClient llm.Client
	var history []entity.ChatMessage
	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		t1 := time.Now()
		client, err := s.resolveLLMClient(gctx, userID, modelID, modelType)
		logger.Infof("[Timing] resolveLLMClient: modelID=%s, cost=%dms", modelID, time.Since(t1).Milliseconds())
		if err != nil {
			logger.Errorf("模型解析失败, modelID=%s, modelType=%s: %v", modelID, modelType, err)
			return fmt.Errorf("模型配置无效或无权访问")
		}
		llmClient = client
		return nil
	})

	g.Go(func() error {
		t1 := time.Now()
		msg, err := s.messageRepo.FindRecent(gctx, sessionID, 20)
		logger.Infof("[Timing] FindRecent history: cost=%dms", time.Since(t1).Milliseconds())
		if err != nil {
			logger.Errorf("加载历史对话失败, sessionID=%s: %v", sessionID, err)
			return fmt.Errorf("加载历史对话失败")
		}
		history = truncateHistoryByTokens(msg, 2000)
		logger.Infof("历史消息: 加载 %d 条, 截断后保留 %d 条", len(msg), len(history))
		return nil
	})

	if err := g.Wait(); err != nil {
		return nil, nil, err
	}
	logger.Infof("[Timing] initChatContext 总耗时: cost=%dms", time.Since(t0).Milliseconds())
	return llmClient, history, nil
}

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

func (s *chatService) saveAssistantMessage(ctx context.Context, sessionID, msgID, content string, req requestdto.SendMessageRequest, sources []dto.SourceInfo, metadata datatypes.JSON) error {
	assistantMsg := entity.ChatMessage{
		ID:               msgID,
		SessionID:        sessionID,
		Role:             "assistant",
		Content:          content,
		ModelID:          req.ModelID,
		SearchMode:       req.SearchMode,
		KnowledgeBaseIDs: datatypes.JSON(mustMarshal(req.KnowledgeBaseIDs)),
		Sources:          datatypes.JSON(mustMarshal(sources)),
		Metadata:         metadata,
	}
	return s.messageRepo.Create(ctx, &assistantMsg)
}

func (s *chatService) resolveLLMClient(ctx context.Context, userID, modelID, modelType string) (llm.Client, error) {
	switch modelType {
	case "user":
		t0 := time.Now()
		cfg, err := s.userModelConfigRepo.GetByID(ctx, modelID, userID)
		logger.Infof("[Timing] userModelConfigRepo.GetByID: modelID=%s, cost=%dms", modelID, time.Since(t0).Milliseconds())
		if err != nil {
			return nil, fmt.Errorf("查询用户模型配置失败: %w", err)
		}
		t1 := time.Now()
		client, err := llm.NewClientFromModelConfig(ctx, llm.ModelConfig{
			Provider: cfg.APIFormat,
			ModelID:  cfg.ModelID,
			BaseURL:  cfg.BaseURL,
			APIKey:   cfg.APIKey,
		})
		logger.Infof("[Timing] NewClientFromModelConfig(user): cost=%dms", time.Since(t1).Milliseconds())
		return client, err
	case "system":
		t0 := time.Now()
		model, err := s.modelRepo.GetByID(ctx, modelID)
		logger.Infof("[Timing] modelRepo.GetByID: modelID=%s, cost=%dms", modelID, time.Since(t0).Milliseconds())
		if err != nil {
			return nil, fmt.Errorf("查询系统模型失败: %w", err)
		}
		t1 := time.Now()
		client, err := llm.NewClientFromModelConfig(ctx, llm.ModelConfig{
			Provider: model.Provider,
			ModelID:  model.ModelID,
			BaseURL:  model.BaseURL,
			APIKey:   model.APIKey,
		})
		logger.Infof("[Timing] NewClientFromModelConfig(system): cost=%dms", time.Since(t1).Milliseconds())
		return client, err
	default:
		return nil, fmt.Errorf("不支持的模型类型: %s", modelType)
	}
}

// truncateHistoryByTokens 按 token 预算截断历史消息（从最新消息向前保留）
func truncateHistoryByTokens(messages []entity.ChatMessage, maxTokens int) []entity.ChatMessage {
	var total int
	var result []entity.ChatMessage
	for i := len(messages) - 1; i >= 0; i-- {
		msgTokens := tokenutil.Estimate(messages[i].Content)
		if total+msgTokens > maxTokens {
			break
		}
		total += msgTokens
		result = append([]entity.ChatMessage{messages[i]}, result...)
	}
	return result
}
