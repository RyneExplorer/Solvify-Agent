package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"

	"solvify-agent/internal/agent"
	"solvify-agent/internal/llm"
	requestdto "solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/rag"
	"solvify-agent/internal/repository"
	"solvify-agent/pkg/cache"
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
	userRepo            repository.UserRepository
	userCache           *cache.RedisCache
	agentEngine         *agent.Engine
	contextSvc          ContextServiceInterface
	prefSvc             UserPreferenceService
}

// NewChatService 创建聊天业务服务
func NewChatService(
	sessionRepo repository.ChatSessionRepo,
	messageRepo repository.ChatMessageRepo,
	retriever rag.Retriever,
	modelRepo repository.ModelRepo,
	userModelConfigRepo repository.UserModelConfigRepo,
	userRepo repository.UserRepository,
	userCache *cache.RedisCache,
	agentEngine *agent.Engine,
	contextSvc ContextServiceInterface,
	prefSvc UserPreferenceService,
) ChatServiceInterface {
	return &chatService{
		sessionRepo:         sessionRepo,
		messageRepo:         messageRepo,
		retriever:           retriever,
		modelRepo:           modelRepo,
		userModelConfigRepo: userModelConfigRepo,
		userRepo:            userRepo,
		userCache:           userCache,
		agentEngine:         agentEngine,
		contextSvc:          contextSvc,
		prefSvc:             prefSvc,
	}
}

// SendMessage 发送消息并获取流式响应
func (s *chatService) SendMessage(ctx context.Context, userID, sessionID string, req requestdto.SendMessageRequest) (<-chan dto.StreamEvent, error) {
	if err := s.validateSession(ctx, userID, sessionID); err != nil {
		return nil, err
	}
	userMsgID, err := s.saveUserMessage(ctx, sessionID, req)
	if err != nil {
		return nil, err
	}

	// 缓存比对：如果模型 ID 不一致，更新缓存和数据库
	if req.ModelID != "" {
		s.updateUserLastModel(ctx, userID, req.ModelID)
	}

	eventCh := make(chan dto.StreamEvent, 100)
	go func() {
		defer close(eventCh)
		if req.SearchMode == "smart-reasoning" {
			s.processDeepMode(ctx, userID, sessionID, userMsgID, req, eventCh)
		} else {
			s.processMessage(ctx, userID, sessionID, userMsgID, req, eventCh)
		}
	}()

	return eventCh, nil
}

// updateUserLastModel 更新用户上次使用的模型（缓存比对策略）
func (s *chatService) updateUserLastModel(ctx context.Context, userID, modelID string) {
	cacheKey := "user:model:" + userID

	// 1. 从缓存获取上次使用的模型
	var cachedModelID string
	found, err := s.userCache.Get(ctx, cacheKey, &cachedModelID)
	if err != nil {
		logger.Errorf("读取用户模型缓存失败: userID=%s, err=%v", userID, err)
	}

	// 2. 如果缓存命中且模型 ID 一致，不需要更新
	if found && cachedModelID == modelID {
		return
	}

	// 3. 更新缓存
	if err := s.userCache.Set(ctx, cacheKey, modelID, 24*time.Hour); err != nil {
		logger.Errorf("更新用户模型缓存失败: userID=%s, err=%v", userID, err)
	}

	// 4. 更新数据库
	go func() {
		if err := s.userRepo.Update(userID, map[string]interface{}{"last_model": modelID}); err != nil {
			logger.Errorf("更新用户模型失败: userID=%s, modelID=%s, err=%v", userID, modelID, err)
		}
	}()
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

// initContext 并行加载模型客户端和增强历史对话
// 根据模型最大上下文窗口自动计算历史消息、检索上下文和用户记忆的 token 预算
func (s *chatService) initContext(ctx context.Context, userID, sessionID, modelID, modelType, currentQuery string) (*llm.OpenAIClient, *EnhancedContext, error) {
	t0 := time.Now()

	// 1. 解析模型客户端
	t1 := time.Now()
	client, err := s.resolveClient(ctx, userID, modelID, modelType)
	logger.Infof("[Timing] resolveClient: modelID=%s, cost=%dms", modelID, time.Since(t1).Milliseconds())
	if err != nil {
		logger.Errorf("模型解析失败, modelID=%s, modelType=%s: %v", modelID, modelType, err)
		return nil, nil, fmt.Errorf("模型配置无效或无权访问")
	}

	// 2. 根据模型上下文窗口计算各组件预算
	// 优先使用运行时有效值（若曾触发上下文长度错误会被降低）
	maxCtx := llm.GetEffectiveMaxContextLength(modelID, client.MaxContextLength())
	historyBudget, retrievalBudget, memoryBudget := calculateContextBudgets(maxCtx)

	// 3. 加载用户基本信息
	userCtx := s.loadUserContext(ctx, userID)

	// 4. 使用 ContextService 构建增强上下文
	var enhancedCtx *EnhancedContext
	if s.contextSvc != nil {
		t1 = time.Now()
		enhancedCtx, err = s.contextSvc.BuildContext(ctx, userID, sessionID, currentQuery, BuildContextConfig{
			MaxTokens:         historyBudget,
			MaxMemories:       10,
			MaxRecentMessages: 20,
			RetrievalBudget:   retrievalBudget,
			MemoryBudget:      memoryBudget,
		}, client.ChatModel())
		if err != nil {
			logger.Warnf("构建增强上下文失败，降级为传统方式: %v", err)
		}
	}

	// 兜底：传统截断
	if enhancedCtx == nil {
		msg, _ := s.messageRepo.FindRecentForContext(ctx, sessionID, 20)
		enhancedCtx = &EnhancedContext{
			History:         truncateHistoryByTokens(msg, historyBudget),
			HistoryBudget:   historyBudget,
			RetrievalBudget: retrievalBudget,
		}
	}
	enhancedCtx.UserCtx = userCtx

	// 填充阶段二用户画像、偏好（任何失败不阻断主流程）
	if userEntity, err := s.userRepo.FindByID(userID); err == nil && userEntity != nil {
		enhancedCtx.Profile = userEntity
		if s.prefSvc != nil {
			if p, e := s.prefSvc.GetByUserID(ctx, userID); e == nil {
				enhancedCtx.Preference = p
			}
		}
	}

	logger.Infof("增强上下文: 历史 %d 条(预算 %d), 记忆 %d 条(预算 %d), 检索预算 %d, 摘要存在=%v, 模型窗口=%d, 用户=%s, 偏好=%v",
		len(enhancedCtx.History), enhancedCtx.HistoryBudget,
		len(enhancedCtx.Memories), memoryBudget,
		enhancedCtx.RetrievalBudget, enhancedCtx.Summary != nil, maxCtx, userCtx.Username,
		enhancedCtx.Preference != nil)
	logger.Infof("[Timing] initContext 总耗时: cost=%dms", time.Since(t0).Milliseconds())
	return client, enhancedCtx, nil
}

// loadUserContext 加载用户基本信息，失败时返回空上下文（不阻断主流程）
func (s *chatService) loadUserContext(ctx context.Context, userID string) UserContext {
	if s.userRepo == nil || userID == "" {
		return NewUserContext(entity.User{})
	}
	user, err := s.userRepo.FindByID(userID)
	if err != nil {
		logger.Warnf("加载用户信息失败, userID=%s: %v", userID, err)
		return NewUserContext(entity.User{})
	}
	return NewUserContext(*user)
}

// calculateContextBudgets 根据模型最大上下文窗口，分配历史、检索、记忆的 token 预算
func calculateContextBudgets(maxContextLength int) (historyBudget, retrievalBudget, memoryBudget int) {
	if maxContextLength <= 0 {
		maxContextLength = 8192
	}

	// 为 LLM 回复预留：不超过 4096 或上下文窗口的 1/4
	completionReserved := 4096
	if maxContextLength/4 < completionReserved {
		completionReserved = maxContextLength / 4
	}

	// 为 System Prompt + 当前问题 + 安全边距预留
	fixedReserved := 1500

	// 检索预算：优先保证至少 500，最多 3000
	retrievalBudget = 3000
	if remaining := maxContextLength - completionReserved - fixedReserved - retrievalBudget; remaining < 0 {
		retrievalBudget = max(maxContextLength-completionReserved-fixedReserved, 500)
	}

	// 记忆预算：与模型窗口成正比，但封顶
	memoryBudget = 800
	if maxContextLength >= 32000 {
		memoryBudget = 1200
	}

	// 历史消息预算 = 总窗口 - 回复 - 检索 - 固定预留 - 记忆
	historyBudget = maxContextLength - completionReserved - retrievalBudget - fixedReserved - memoryBudget
	historyBudget = max(historyBudget, 500)
	// 历史消息过多也会拖慢响应，封顶 6000
	if historyBudget > 6000 {
		historyBudget = 6000
	}

	return
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
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
		cfg = llm.ModelConfig{
			Provider:         uc.APIFormat,
			ModelID:          uc.ModelID,
			BaseURL:          uc.BaseURL,
			APIKey:           uc.APIKey,
			Config:           uc.Config,
			MaxContextLength: uc.MaxContextLength,
		}
	case "system":
		m, err := s.modelRepo.GetByID(ctx, modelID)
		if err != nil {
			return nil, fmt.Errorf("查询系统模型失败: %w", err)
		}
		cfg = llm.ModelConfig{
			Provider:         m.Provider,
			ModelID:          m.ModelID,
			BaseURL:          m.BaseURL,
			APIKey:           m.APIKey,
			Config:           m.Config,
			MaxContextLength: m.MaxContextLength,
		}
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

func (s *chatService) saveUserMessage(ctx context.Context, sessionID string, req requestdto.SendMessageRequest) (string, error) {
	userMsg := entity.ChatMessage{
		ID:               uuid.New().String(),
		SessionID:        sessionID,
		Role:             "user",
		Content:          req.Content,
		SearchMode:       req.SearchMode,
		KnowledgeBaseIDs: datatypes.JSON(mustMarshal(req.KnowledgeBaseIDs)),
	}
	if err := s.messageRepo.Create(ctx, &userMsg); err != nil {
		return "", err
	}
	return userMsg.ID, nil
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
// 如果最早一条消息单条超预算但剩余预算 >= 100 token，做内容头部截断保留，避免上下文彻底为空
func truncateHistoryByTokens(messages []entity.ChatMessage, maxTokens int) []entity.ChatMessage {
	var total int
	var result []entity.ChatMessage
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		msgTokens := tokenutil.Estimate(msg.Content)
		if total+msgTokens <= maxTokens {
			total += msgTokens
			result = append([]entity.ChatMessage{msg}, result...)
			continue
		}
		// 预算不够装整条，但还有至少 100 token 空间 -> 截断内容头部保留上下文主题
		remain := maxTokens - total
		if remain >= 100 {
			truncated := msg
			truncated.Content = truncateContentByTokens(truncated.Content, remain) + "\n\n（内容过长，已截断）"
			result = append([]entity.ChatMessage{truncated}, result...)
		}
		break
	}
	return result
}

// truncateContentByTokens 按 token 预算从文本头部截断，返回截断后的字符串
// 为避免切在半个 UTF-8 rune，采用「字符数 × 类型权重」反推一个安全长度，再按 rune 截取
func truncateContentByTokens(content string, maxTokens int) string {
	if content == "" {
		return ""
	}
	runes := []rune(content)
	var total int
	var cut int
	for i, r := range runes {
		var w float64
		switch {
		case r >= 0x4e00 && r <= 0x9fff:
			w = 1.5
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			w = 0.25
		default:
			w = 0.5
		}
		if total+int(w) > maxTokens {
			break
		}
		total += int(w)
		cut = i + 1
	}
	if cut == 0 {
		return ""
	}
	return string(runes[:cut])
}
