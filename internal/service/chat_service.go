package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/datatypes"

	"solvify-agent/internal/agent"
	"solvify-agent/internal/llm"
	requestdto "solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/observability"
	"solvify-agent/internal/rag"
	"solvify-agent/internal/repository"
	"solvify-agent/pkg/cache"
	"solvify-agent/pkg/config"
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
	obs                 observability.Recorder
	obsRepo             repository.ObservabilityRepo
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
	extra ...interface{},
) ChatServiceInterface {
	s := &chatService{
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
	for _, it := range extra {
		switch v := it.(type) {
		case observability.Recorder:
			s.obs = v
		case repository.ObservabilityRepo:
			s.obsRepo = v
		}
	}
	return s
}

func (s *chatService) SetObservability(obs observability.Recorder, repo repository.ObservabilityRepo) {
	s.obs = obs
	s.obsRepo = repo
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

	if req.ModelID != "" {
		s.updateUserLastModel(ctx, userID, req.ModelID)
	}

	if s.obs != nil {
		ctx = s.obs.WithTraceRoot(ctx, observability.TraceRootAttrs{
			UserID:     userID,
			SessionID:  sessionID,
			MessageID:  userMsgID,
			RequestID:  requestIDFromCtx(ctx),
			SearchMode: req.SearchMode,
			ModelID:    req.ModelID,
		})
	}

	eventCh := make(chan dto.StreamEvent, 100)
	go func() {
		defer close(eventCh)
		if req.SearchMode == "smart-reasoning" {
			s.processDeepMode(ctx, userID, sessionID, userMsgID, req, eventCh)
		} else {
			s.processMessage(ctx, userID, sessionID, userMsgID, req, eventCh)
		}
		if s.obs != nil {
			s.obs.FlushTrace(ctx, userID, sessionID, userMsgID)
		}
	}()

	return eventCh, nil
}

func requestIDFromCtx(ctx context.Context) string {
	type iKey string
	const key iKey = "request_id"
	if v := ctx.Value(key); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return uuid.New().String()
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

// ─── 可观测：反馈 / Trace / Metrics 查询接口 ───────────────────────────────────

func (s *chatService) SubmitFeedback(ctx context.Context, userID, messageID string, req FeedbackRequest) error {
	if req.Rating != 1 && req.Rating != -1 {
		return fmt.Errorf("rating 必须为 1 或 -1")
	}
	if messageID == "" || userID == "" {
		return fmt.Errorf("message_id / user_id 不能为空")
	}
	msg, err := s.messageRepo.FindByID(ctx, messageID)
	if err != nil {
		return fmt.Errorf("查询消息失败: %w", err)
	}
	if msg == nil {
		return fmt.Errorf("消息不存在或无权限")
	}
	if msg.SessionID != "" {
		if vErr := s.validateSession(ctx, userID, msg.SessionID); vErr != nil {
			return fmt.Errorf("消息不存在或无权限")
		}
	}
	var traceID string
	if raw := msg.Metadata; len(raw) > 0 {
		if meta := metadataAsMap(raw); meta != nil {
			if v, ok := meta["trace_id"].(string); ok {
				traceID = v
			}
		}
	}
	primaryTag := ""
	if len(req.Reasons) > 0 {
		primaryTag = req.Reasons[0]
	}
	fb := &entity.MessageFeedback{
		ID:        uuid.New().String(),
		MessageID: messageID,
		UserID:    userID,
		SessionID: msg.SessionID,
		Rating:    req.Rating,
		ReasonTag: primaryTag,
		Comment:   req.Comment,
		IsQuick:   req.IsQuick,
		TraceID:   traceID,
	}
	fb.SetReasons(req.Reasons)
	if s.obsRepo != nil {
		if e := s.obsRepo.CreateFeedback(ctx, fb); e != nil {
			return fmt.Errorf("保存反馈失败: %w", e)
		}
	}
	if s.obs != nil {
		s.obs.Incr(ctx, "chat_feedback_total", map[string]string{
			"rating":      ratingLabel(req.Rating),
			"reason_tag":  reasonTagOrDefault(primaryTag),
			"has_comment": boolLabel(req.Comment != ""),
		}, 1)
	}
	if s.obs != nil {
		s.obs.RecordFeedback(&observability.Feedback{
			MessageID: fb.MessageID,
			UserID:    fb.UserID,
			SessionID: fb.SessionID,
			Rating:    fb.Rating,
			Reasons:   fb.Reasons(),
			Comment:   fb.Comment,
			TraceID:   fb.TraceID,
			CreatedAt: fb.CreatedAt,
		})
	}
	return nil
}

func (s *chatService) ListFeedbacks(ctx context.Context, userID string, offset, limit int) (FeedbackListResponse, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	if s.obsRepo == nil {
		return FeedbackListResponse{Total: 0, Feedbacks: []any{}}, nil
	}
	list, total, err := s.obsRepo.ListByUser(ctx, userID, offset, limit)
	if err != nil {
		return FeedbackListResponse{}, err
	}
	type out struct {
		entity.MessageFeedback
		Reasons []string `json:"reasons"`
	}
	items := make([]any, 0, len(list))
	for _, f := range list {
		items = append(items, out{MessageFeedback: f, Reasons: f.Reasons()})
	}
	return FeedbackListResponse{Total: total, Feedbacks: items}, nil
}

func (s *chatService) GetTrace(ctx context.Context, userID, traceID string, isAdmin bool) (*TraceResponse, error) {
	if s.obsRepo == nil || traceID == "" {
		return nil, fmt.Errorf("trace 存储未启用")
	}
	t, err := s.obsRepo.FindByID(ctx, traceID)
	if err != nil {
		return nil, fmt.Errorf("trace 不存在: %w", err)
	}
	if !isAdmin && t.UserID != userID {
		return nil, fmt.Errorf("无权限访问该 trace")
	}
	resp := &TraceResponse{
		ID:         t.ID,
		RequestID:  t.RequestID,
		UserID:     t.UserID,
		SessionID:  t.SessionID,
		SampleRate: t.SampleRate,
		Sampled:    t.Sampled,
		DurationMs: t.DurationMs,
		Status:     t.Status,
		Error:      t.Error,
		Attrs:      t.Attrs,
		SpanTree:   t.SpanTree,
		CreatedAt:  t.CreatedAt.Format("2006-01-02 15:04:05"),
	}
	return resp, nil
}

func (s *chatService) ListSessionTraces(ctx context.Context, userID, sessionID string, isAdmin bool, offset, limit int) (TraceListResponse, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 200 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	if s.obsRepo == nil {
		return TraceListResponse{Total: 0, Traces: []any{}}, nil
	}
	if !isAdmin {
		if err := s.validateSession(ctx, userID, sessionID); err != nil {
			return TraceListResponse{}, err
		}
	}
	list, total, err := s.obsRepo.ListBySession(ctx, sessionID, userID, offset, limit)
	if isAdmin {
		list, total, err = s.obsRepo.ListAll(ctx, sessionID, "", offset, limit)
	}
	if err != nil {
		return TraceListResponse{}, err
	}
	items := make([]any, 0, len(list))
	for _, t := range list {
		items = append(items, TraceResponse{
			ID:         t.ID,
			RequestID:  t.RequestID,
			UserID:     t.UserID,
			SessionID:  t.SessionID,
			SampleRate: t.SampleRate,
			Sampled:    t.Sampled,
			DurationMs: t.DurationMs,
			Status:     t.Status,
			Error:      t.Error,
			Attrs:      t.Attrs,
			CreatedAt:  t.CreatedAt.Format("2006-01-02 15:04:05"),
		})
	}
	return TraceListResponse{Total: total, Traces: items}, nil
}

func (s *chatService) AdminListTraces(ctx context.Context, sessionID string, rating int, status string, offset, limit int) (TraceListResponse, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	if s.obsRepo == nil {
		return TraceListResponse{Total: 0, Traces: []any{}}, nil
	}
	list, total, err := s.obsRepo.ListAll(ctx, sessionID, status, offset, limit)
	if err != nil {
		return TraceListResponse{}, err
	}
	items := make([]any, 0, len(list))
	for _, t := range list {
		items = append(items, TraceResponse{
			ID:         t.ID,
			RequestID:  t.RequestID,
			UserID:     t.UserID,
			SessionID:  t.SessionID,
			SampleRate: t.SampleRate,
			Sampled:    t.Sampled,
			DurationMs: t.DurationMs,
			Status:     t.Status,
			Error:      t.Error,
			Attrs:      t.Attrs,
			CreatedAt:  t.CreatedAt.Format("2006-01-02 15:04:05"),
		})
	}
	return TraceListResponse{Total: total, Traces: items}, nil
}

func (s *chatService) GetMetricsSnapshot() (map[string]any, error) {
	if s.obs == nil {
		return nil, fmt.Errorf("observability 未启用")
	}
	raw, err := s.obs.MetricsSnapshot()
	if err != nil {
		return nil, err
	}
	rawCounters, _ := raw["counters"].([]any)
	rawGauges, _ := raw["gauges"].([]any)
	rawHistos, _ := raw["histograms"].([]any)
	labelDropped, _ := raw["label_cardinality_dropped_total"].(int64)
	var generatedTs string
	if ts, ok := raw["generated_at_seconds"].(int64); ok {
		generatedTs = time.Unix(ts, 0).Format(time.RFC3339)
	}

	samplingRate := 0.0
	labelCardLimit := 0
	bufferDropped := int64(0)
	piiMasked := int64(0)
	if ss, ok := raw["sink_stats"].(map[string]any); ok {
		if v, ok := ss["dropped_records_total"].(int64); ok {
			bufferDropped = v
		}
	}
	if c, ok := s.obs.(interface{ SamplingRate() float64 }); ok {
		samplingRate = c.SamplingRate()
	} else if cfg := s.cfgObservability(); cfg != nil {
		samplingRate = cfg.SamplingRate
	}

	labelsToMap := func(raw []any) map[string]string {
		out := map[string]string{}
		for _, r := range raw {
			if m, ok := r.(map[string]any); ok {
				k, _ := m["name"].(string)
				v, _ := m["value"].(string)
				if k != "" {
					out[k] = v
				}
			}
		}
		return out
	}
	type namedSamples struct {
		Name    string
		Help    string
		Samples []any
	}
	groupByMetric := func(rows []any) []namedSamples {
		groups := map[string]*namedSamples{}
		order := []string{}
		for _, r := range rows {
			m, ok := r.(map[string]any)
			if !ok {
				continue
			}
			name, _ := m["name"].(string)
			if name == "" {
				continue
			}
			if _, seen := groups[name]; !seen {
				groups[name] = &namedSamples{Name: name}
				order = append(order, name)
			}
			sample := map[string]any{}
			labelsRaw, _ := m["labels"].([]any)
			labelsM := labelsToMap(labelsRaw)
			if len(labelsM) > 0 {
				sample["labels"] = labelsM
			}
			switch {
			case m["value"] != nil:
				if v, ok := m["value"].(float64); ok {
					sample["value"] = int64(v)
				} else {
					sample["value"] = m["value"]
				}
			case m["count"] != nil:
				if v, ok := m["count"].(int64); ok {
					sample["count"] = v
				} else {
					sample["count"] = m["count"]
				}
				if sum, ok := m["sum"].(float64); ok {
					sample["sum"] = sum
				}
				if buckets, ok := m["buckets"].([]any); ok {
					outB := make([]any, 0, len(buckets))
					for _, b := range buckets {
						bm, ok := b.(map[string]any)
						if !ok {
							continue
						}
						le := bm["le"]
						if le == "+Inf" {
						}
						cnt, _ := bm["delta_count"].(int64)
						outB = append(outB, map[string]any{"le": le, "count": cnt})
					}
					sample["buckets"] = outB
				}
			}
			groups[name].Samples = append(groups[name].Samples, sample)
		}
		out := make([]namedSamples, 0, len(order))
		for _, n := range order {
			out = append(out, *groups[n])
		}
		return out
	}
	cGroups := groupByMetric(rawCounters)
	gGroups := groupByMetric(rawGauges)
	hGroups := groupByMetric(rawHistos)
	counters := make([]any, 0, len(cGroups))
	for _, c := range cGroups {
		counters = append(counters, map[string]any{"name": c.Name, "help": "", "samples": c.Samples})
	}
	gauges := make([]any, 0, len(gGroups))
	for _, g := range gGroups {
		gauges = append(gauges, map[string]any{"name": g.Name, "help": "", "samples": g.Samples})
	}
	histos := make([]any, 0, len(hGroups))
	for _, h := range hGroups {
		histos = append(histos, map[string]any{"name": h.Name, "help": "", "samples": h.Samples})
	}
	return map[string]any{
		"ts":                              generatedTs,
		"counters":                        counters,
		"gauges":                          gauges,
		"histograms":                      histos,
		"sampling_rate":                   samplingRate,
		"label_cardinality_limit":         labelCardLimit,
		"buffer_dropped_total":            bufferDropped,
		"pii_masked_total":                piiMasked,
		"label_cardinality_dropped_total": labelDropped,
	}, nil
}

func (s *chatService) cfgObservability() *config.ObservabilityConfig {
	if s.obs == nil {
		return nil
	}
	type cfgProvider interface{ Config() config.ObservabilityConfig }
	if p, ok := s.obs.(cfgProvider); ok {
		c := p.Config()
		return &c
	}
	return nil
}

func ratingLabel(r int) string {
	switch r {
	case 1:
		return "up"
	case -1:
		return "down"
	default:
		return "unknown"
	}
}

func boolLabel(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func reasonTagOrDefault(tag string) string {
	if tag == "" {
		return "none"
	}
	return tag
}

func metadataAsMap(raw datatypes.JSON) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}
