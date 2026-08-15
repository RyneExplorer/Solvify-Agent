package service

import (
	"context"
	"fmt"
	"strings"
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
	// sessionStatusActive 表示会话处于活跃状态
	sessionStatusActive = "active"
)

// chatService 封装聊天业务用例实现
type chatService struct {
	sessionRepo         repository.ChatSessionRepo
	messageRepo         repository.ChatMessageRepo
	retriever           rag.Retriever
	einoRetriever       *rag.EinoRetrieverAdapter
	modelRepo           repository.ModelRepo
	userModelConfigRepo repository.UserModelConfigRepo
	userRepo            repository.UserRepository
	userCache           *cache.RedisCache
	agentEngine         *agent.Engine
	contextSvc          ContextServiceInterface
	prefSvc             UserPreferenceService
	obs                 observability.Recorder
	obsRepo             repository.ObservabilityRepo
	embedClient         *llm.EmbeddingClient
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
	defaultTopK := 10
	if cfg := config.Get(); cfg != nil && cfg.RAG.TopK > 0 {
		defaultTopK = cfg.RAG.TopK
	}
	s := &chatService{
		sessionRepo:         sessionRepo,
		messageRepo:         messageRepo,
		retriever:           retriever,
		einoRetriever:       rag.NewEinoRetrieverAdapter(retriever, defaultTopK),
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

// SetObservability 注入可观测性记录器和仓储
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

	searchMode := req.SearchMode
	if searchMode == "" {
		searchMode = "quick"
	}

	var traceID string
	if s.obs != nil {
		ctx = s.obs.WithTraceRoot(ctx, observability.TraceRootAttrs{
			UserID:     userID,
			SessionID:  sessionID,
			MessageID:  userMsgID,
			RequestID:  requestIDFromCtx(ctx),
			SearchMode: searchMode,
			ModelID:    req.ModelID,
		})
		traceID = observability.TraceIDFromContext(ctx)
	}

	// 如果启用了可观测性 DB：先创建一个 agent_tasks 行，
	//   task_id = trace_id，trace_id/session_id/user_id/search_mode/model_id 全初始化
	//   这样即使中间任何环节崩了，前端仍能在详情页看到 task 基本信息 + 已写入的 agent_task_steps
	if s.obsRepo != nil && traceID != "" {
		_ = s.obsRepo.CreateAgentTask(ctx, &entity.AgentTask{
			ID:         traceID,
			TraceID:    traceID,
			SessionID:  sessionID,
			UserID:     userID,
			ModelID:    req.ModelID,
			SearchMode: searchMode,
			StartedAt:  time.Now(),
			Status:     "running",
		})
	}

	eventCh := make(chan dto.StreamEvent, 100)
	go func() {
		defer close(eventCh)
		status := "ok"
		abortReason := ""
		errorSummary := ""
		func() {
			defer func() {
				if r := recover(); r != nil {
					logger.Errorf("SendMessage goroutine panic 已恢复: sessionID=%s, err=%v", sessionID, r)
					status = "error"
					errorSummary = fmt.Sprintf("panic: %v", r)
					abortReason = "runtime_panic"
				}
			}()

			// 澄清追问恢复：检查 session 是否有待处理的澄清
			// 未超时 → 清掉 pending，历史自然串成 [user→assistant追问→user回答]，正常跑流程
			// 超时 → 清掉 pending，正常跑（用户已遗忘之前的追问，新消息按新问题处理）
			session, findErr := s.sessionRepo.FindByID(ctx, sessionID)
			if findErr == nil && session != nil && session.HasPendingClarify() {
				pc, _ := session.GetPendingClarify()
				if pc != nil {
					if pc.IsExpired(entity.ClarifyDefaultTimeout) {
						logger.Warnf("澄清追问已超时(>%v)，忽略 pending 状态: sessionID=%s", entity.ClarifyDefaultTimeout, sessionID)
					} else {
						logger.Warnf("用户回复澄清追问，恢复正常流程: sessionID=%s, question=%q", sessionID, pc.Question)
					}
					if cErr := s.sessionRepo.ClearPendingClarify(ctx, sessionID); cErr != nil {
						logger.Warnf("清除澄清状态失败: %v", cErr)
					}
				}
			}

			getModeHandler(searchMode).Handle(ctx, s, userID, sessionID, userMsgID, req, eventCh)
			if s.obs != nil {
				s.obs.FlushTrace(ctx, userID, sessionID, userMsgID)
			}
		}()
		// 结束 agent_tasks 行（无论成功/失败）
		if s.obsRepo != nil && traceID != "" {
			_ = s.obsRepo.MarkEnded(context.Background(), traceID, status, abortReason, errorSummary, 0, 0, 0.0, nil)
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

// CreateSession 创建新的聊天会话
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

// GetSession 根据会话 ID 获取会话详情
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

// ListSessions 列出指定用户的全部会话
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

// DeleteSession 删除会话及其消息
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

// GetMessages 获取指定会话的消息列表
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
//
// toolsTokensReserve 仅用于深度模式/带工具调用场景，调用方可先预计算工具 JSON Schema 占用的真 token，
// 这里会先从总窗口里扣掉再分各块预算，避免 tools 定义直接把上下文撑爆（P0-④）。
func (s *chatService) initContext(ctx context.Context, userID, sessionID, modelID, modelType, currentQuery string, toolsTokensReserve ...int) (*llm.OpenAIClient, *EnhancedContext, error) {
	t0 := time.Now()

	// 1. 解析模型客户端
	t1 := time.Now()
	client, err := s.resolveClient(ctx, userID, modelID, modelType)
	logger.Infof("[Timing] resolveClient: modelID=%s, cost=%dms", modelID, time.Since(t1).Milliseconds())
	if err != nil {
		logger.Errorf("模型解析失败, modelID=%s, modelType=%s: %v", modelID, modelType, err)
		return nil, nil, fmt.Errorf("模型配置无效或无权访问")
	}
	modelName := client.ModelName()

	// 2. 根据模型上下文窗口计算各组件预算
	// 优先使用运行时有效值（若曾触发上下文长度错误会被降低）
	maxCtx := llm.GetEffectiveMaxContextLength(modelID, client.MaxContextLength())
	toolsTokens := 0
	if len(toolsTokensReserve) > 0 {
		toolsTokens = toolsTokensReserve[0]
		if toolsTokens < 0 {
			toolsTokens = 0
		}
	}
	historyBudget, retrievalBudget, memoryBudget := calculateContextBudgets(maxCtx, toolsTokens)

	// 3. 并行加载：BuildContext / 用户信息 / 用户偏好 —— 三者无依赖，全部 goroutine
	type userLoadResult struct {
		entity *entity.User
		err    error
	}
	type prefLoadResult struct {
		pref *entity.UserPreference
		err  error
	}
	type ctxLoadResult struct {
		ctx *EnhancedContext
		err error
	}

	userCh := make(chan userLoadResult, 1)
	prefCh := make(chan prefLoadResult, 1)
	ctxCh := make(chan ctxLoadResult, 1)

	// goroutine A: 用户信息（只查一次，UserCtx 和 Profile 共用结果）
	go func() {
		var r userLoadResult
		if s.userRepo != nil && userID != "" {
			u, err := s.userRepo.FindByID(userID)
			r = userLoadResult{entity: u, err: err}
		}
		userCh <- r
	}()

	// goroutine B: 用户偏好
	go func() {
		var r prefLoadResult
		if s.prefSvc != nil && userID != "" {
			p, err := s.prefSvc.GetByUserID(ctx, userID)
			r = prefLoadResult{pref: p, err: err}
		}
		prefCh <- r
	}()

	// goroutine C: BuildContext（内部自己已经是 4 路并行：summary/memories/recent/relevant）
	go func() {
		var r ctxLoadResult
		if s.contextSvc != nil {
			enhancedCtx, bErr := s.contextSvc.BuildContext(ctx, userID, sessionID, currentQuery, BuildContextConfig{
				MaxTokens:         historyBudget,
				MaxMemories:       10,
				MaxRecentMessages: 20,
				RetrievalBudget:   retrievalBudget,
				MemoryBudget:      memoryBudget,
				ModelName:         modelName,
				ToolsTokens:       toolsTokens,
			}, client.ChatModel())
			if bErr != nil {
				logger.Warnf("构建增强上下文失败，降级为传统方式: %v", bErr)
			}
			r = ctxLoadResult{ctx: enhancedCtx, err: bErr}
		}
		ctxCh <- r
	}()

	userRes := <-userCh
	prefRes := <-prefCh
	ctxRes := <-ctxCh

	// 填 enhancedCtx
	var enhancedCtx *EnhancedContext
	if ctxRes.err == nil && ctxRes.ctx != nil {
		enhancedCtx = ctxRes.ctx
	}
	if enhancedCtx == nil {
		// 兜底：传统截断
		msg, _ := s.messageRepo.FindRecentForContext(ctx, sessionID, 20)
		enhancedCtx = &EnhancedContext{
			History:         truncateHistoryByTokens(msg, historyBudget, modelName),
			HistoryBudget:   historyBudget,
			RetrievalBudget: retrievalBudget,
		}
	}

	// 用 goroutine A 的结果同时填 UserCtx 和 Profile（之前 loadUserContext + 后面 FindByID 查了两次，现在一次搞定）
	if userRes.err != nil {
		logger.Warnf("加载用户信息失败, userID=%s: %v", userID, userRes.err)
	}
	if userRes.entity != nil {
		enhancedCtx.UserCtx = NewUserContext(*userRes.entity)
		enhancedCtx.Profile = userRes.entity
	} else {
		enhancedCtx.UserCtx = NewUserContext(entity.User{})
	}

	if prefRes.err != nil {
		logger.Warnf("加载用户偏好失败, userID=%s: %v", userID, prefRes.err)
	}
	if prefRes.pref != nil {
		enhancedCtx.Preference = prefRes.pref
	}

	logger.Infof("增强上下文: 历史 %d 条(预算 %d), 记忆 %d 条(预算 %d), 检索预算 %d, 工具预留 %d, 摘要存在=%v, 模型窗口=%d, 用户=%s, 偏好=%v",
		len(enhancedCtx.History), enhancedCtx.HistoryBudget,
		len(enhancedCtx.Memories), memoryBudget,
		enhancedCtx.RetrievalBudget, toolsTokens, enhancedCtx.Summary != nil, maxCtx, enhancedCtx.UserCtx.Username,
		enhancedCtx.Preference != nil)

	// P1-⑨：分块 token 指标（Prometheus /metrics 直接聚合可看"到底是哪一块把窗口撑爆了"）
	if s.obs != nil {
		obs := s.obs
		labels := map[string]string{
			"model_id":   modelID,
			"model_name": modelName,
		}
		// System prompt 骨架 + 摘要 + 记忆 + 用户上下文：快速/深度两模式都走 PromptBuilder.BuildSystem()
		systemTokens := tokenutil.CountTokens(
			NewPromptBuilder(PromptModeQuick, quickModeAgentSystemPrompt, enhancedCtx.Summary, enhancedCtx.Memories, enhancedCtx.UserCtx).
				WithProfile(enhancedCtx.Profile).WithPreference(enhancedCtx.Preference).
				BuildSystem(),
			modelName,
		)
		obs.Observe(ctx, "ctx_prompt_tokens_by_block", mergeStrMap(labels, map[string]string{"block": "system"}), float64(systemTokens))

		// 摘要块单独打一个，方便看"摘要越长，爆窗口风险越高"趋势
		summaryTokens := 0
		if enhancedCtx.Summary != nil {
			summaryTokens = tokenutil.CountTokens(enhancedCtx.Summary.Summary, modelName)
		}
		obs.Observe(ctx, "ctx_prompt_tokens_by_block", mergeStrMap(labels, map[string]string{"block": "summary"}), float64(summaryTokens))

		// 记忆块
		memoryText := strings.Builder{}
		for _, m := range enhancedCtx.Memories {
			memoryText.WriteString(m.Content)
			memoryText.WriteByte('\n')
		}
		obs.Observe(ctx, "ctx_prompt_tokens_by_block", mergeStrMap(labels, map[string]string{"block": "memory"}), float64(tokenutil.CountTokens(memoryText.String(), modelName)))

		// 用户画像+偏好（已经在 system 里，但单独打一个方便看 profile 模板是否膨胀）
		profileText := strings.Builder{}
		if enhancedCtx.Profile != nil {
			profileText.WriteString(enhancedCtx.Profile.Department)
			profileText.WriteByte(' ')
			profileText.WriteString(enhancedCtx.Profile.Position)
			profileText.WriteByte(' ')
			profileText.WriteString(enhancedCtx.Profile.Expertise)
		}
		obs.Observe(ctx, "ctx_prompt_tokens_by_block", mergeStrMap(labels, map[string]string{"block": "profile"}), float64(tokenutil.CountTokens(profileText.String(), modelName)))

		// 历史块
		historyText := strings.Builder{}
		for _, m := range enhancedCtx.History {
			historyText.WriteString(m.Content)
			historyText.WriteByte('\n')
		}
		obs.Observe(ctx, "ctx_prompt_tokens_by_block", mergeStrMap(labels, map[string]string{"block": "history"}), float64(tokenutil.CountTokens(historyText.String(), modelName)))

		// 检索块预算（真实占用要等 buildDocsContextBlock 后才知道，这里先记录"给了多少预算"）
		obs.Observe(ctx, "ctx_prompt_tokens_by_block", mergeStrMap(labels, map[string]string{"block": "retrieval_budget"}), float64(enhancedCtx.RetrievalBudget))

		// 工具定义块（深度模式才会有值）
		obs.Observe(ctx, "ctx_prompt_tokens_by_block", mergeStrMap(labels, map[string]string{"block": "tools"}), float64(toolsTokens))
	}

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
	if err := s.messageRepo.Create(ctx, &assistantMsg); err != nil {
		return err
	}
	s.bgComputeEmbedding(ctx, assistantMsg.ID, assistantMsg.Content)
	return nil
}

// bgComputeEmbedding 后台为消息计算向量表示，失败不阻塞主流程。
func (s *chatService) bgComputeEmbedding(ctx context.Context, messageID, content string) {
	if s.embedClient == nil || strings.TrimSpace(content) == "" {
		return
	}
	go func() {
		embedCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		vec, err := s.embedClient.Embed(embedCtx, content)
		if err != nil {
			logger.Warnf("消息向量计算失败: messageID=%s, err=%v", messageID, err)
			return
		}
		f32 := make(entity.FloatVector, len(vec))
		for i, v := range vec {
			f32[i] = float32(v)
		}
		if err := s.messageRepo.UpdateEmbedding(embedCtx, messageID, f32); err != nil {
			logger.Warnf("消息向量更新失败: messageID=%s, err=%v", messageID, err)
		}
	}()
}

// truncateHistoryByTokens 按真 BPE token 预算从尾部向前保留"完整轮对"。
//
// 关键修复（P0-②）：旧代码"从尾部逐个 append 头插"实际是按时间正序塞，但因为
// applySummary 之前插了一条「assistant 摘要消息」在最前面，预算紧张时只塞到"摘要 + 最老几条"，
// 最新的用户问题反而被截断。现在按轮对（user + assistant 配对）从尾部保留，
// 并且保证最后一条必须是本轮 user（最后一条如果是 assistant 会在调用方补 user query，
// 所以我们只确保"若最后一条恰好是 user 就不能截掉"）。
