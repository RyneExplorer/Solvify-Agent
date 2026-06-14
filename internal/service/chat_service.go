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

// initContext 并行加载模型客户端和历史对话，两种模式共用
func (s *chatService) initContext(ctx context.Context, userID, sessionID, modelID, modelType string, maxHistoryTokens int) (*llm.OpenAIClient, []entity.ChatMessage, error) {
	t0 := time.Now()
	var client *llm.OpenAIClient
	var history []entity.ChatMessage
	g, gctx := errgroup.WithContext(ctx)

	g.Go(func() error {
		t1 := time.Now()
		c, err := s.resolveClient(gctx, userID, modelID, modelType)
		logger.Infof("[Timing] resolveClient: modelID=%s, cost=%dms", modelID, time.Since(t1).Milliseconds())
		if err != nil {
			logger.Errorf("模型解析失败, modelID=%s, modelType=%s: %v", modelID, modelType, err)
			return fmt.Errorf("模型配置无效或无权访问")
		}
		client = c
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
		history = truncateHistoryByTokens(msg, maxHistoryTokens)
		logger.Infof("历史消息: 加载 %d 条, 截断后保留 %d 条", len(msg), len(history))
		return nil
	})

	if err := g.Wait(); err != nil {
		return nil, nil, err
	}
	logger.Infof("[Timing] initContext 总耗时: cost=%dms", time.Since(t0).Milliseconds())
	return client, history, nil
}

// resolveClient 根据模型配置解析 LLM 客户端
func (s *chatService) resolveClient(ctx context.Context, userID, modelID, modelType string) (*llm.OpenAIClient, error) {
	var cfg llm.ModelConfig
	switch modelType {
	case "user":
		uc, err := s.userModelConfigRepo.GetByID(ctx, modelID, userID)
		if err != nil {
			return nil, fmt.Errorf("查询用户模型配置失败: %w", err)
		}
		cfg = llm.ModelConfig{Provider: uc.APIFormat, ModelID: uc.ModelID, BaseURL: uc.BaseURL, APIKey: uc.APIKey, Config: uc.Config}
	case "system":
		m, err := s.modelRepo.GetByID(ctx, modelID)
		if err != nil {
			return nil, fmt.Errorf("查询系统模型失败: %w", err)
		}
		cfg = llm.ModelConfig{Provider: m.Provider, ModelID: m.ModelID, BaseURL: m.BaseURL, APIKey: m.APIKey, Config: m.Config}
	default:
		return nil, fmt.Errorf("不支持的模型类型: %s", modelType)
	}

	return llm.NewClientFromModelConfig(ctx, cfg)
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
