package service

import (
	"context"
	"encoding/json"
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
			if searchMode == "smart-reasoning" {
				s.processDeepMode(ctx, userID, sessionID, userMsgID, req, eventCh)
			} else {
				s.processMessage(ctx, userID, sessionID, userMsgID, req, eventCh)
			}
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
			ModelName:         modelName,
			ToolsTokens:       toolsTokens,
		}, client.ChatModel())
		if err != nil {
			logger.Warnf("构建增强上下文失败，降级为传统方式: %v", err)
		}
	}

	// 兜底：传统截断
	if enhancedCtx == nil {
		msg, _ := s.messageRepo.FindRecentForContext(ctx, sessionID, 20)
		enhancedCtx = &EnhancedContext{
			History:         truncateHistoryByTokens(msg, historyBudget, modelName),
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

	logger.Infof("增强上下文: 历史 %d 条(预算 %d), 记忆 %d 条(预算 %d), 检索预算 %d, 工具预留 %d, 摘要存在=%v, 模型窗口=%d, 用户=%s, 偏好=%v",
		len(enhancedCtx.History), enhancedCtx.HistoryBudget,
		len(enhancedCtx.Memories), memoryBudget,
		enhancedCtx.RetrievalBudget, toolsTokens, enhancedCtx.Summary != nil, maxCtx, userCtx.Username,
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

// mergeStrMap 返回 base+extra 合并后的 map（不修改原始 map，避免 label 串场）
func mergeStrMap(a, b map[string]string) map[string]string {
	out := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
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

// calculateContextBudgets 根据模型最大上下文窗口 + 工具定义占用，分配历史、检索、记忆的 token 预算。
//
// P0-④ 关键修复：toolsTokens (深度模式/多工具场景的工具 JSON Schema 真 token 数) 必须先从总窗口
// 扣除，再分配回复预留和固定预留，否则多工具时直接把历史 + 检索预算挤成负数或零。
// 同时所有预算最终都按 0.95*maxCtx 的安全顶封顶，给偶发的角色名/special token 留余量。
func calculateContextBudgets(maxContextLength int, toolsTokens ...int) (historyBudget, retrievalBudget, memoryBudget int) {
	toolReserve := 0
	if len(toolsTokens) > 0 && toolsTokens[0] > 0 {
		toolReserve = toolsTokens[0]
	}
	if maxContextLength <= 0 {
		maxContextLength = 8192
	}
	// 0.95 的安全顶：角色标记、特殊 token、工具结果 JSON 序列化扩展，都容易让"算刚好"爆。
	safeCap := int(float64(maxContextLength) * 0.95)
	if toolReserve >= safeCap {
		// 工具定义已经吃掉整个窗口（极端异常配置）：给历史留 200 保底，其他归零
		return 200, 0, 0
	}
	remaining := safeCap - toolReserve

	// 1. 回复预留：不超过 4096 或 safeCap 的 1/4
	completionReserved := 4096
	if remaining/4 < completionReserved {
		completionReserved = remaining / 4
	}
	if completionReserved < 200 {
		completionReserved = 200
	}
	remaining -= completionReserved

	// 2. 固定预留：System Prompt 基础骨架 + 当前 user question 包装 + 安全边距
	fixedReserved := 1500
	if remaining-fixedReserved < 500 {
		fixedReserved = remaining / 4
		if fixedReserved < 300 {
			fixedReserved = 300
		}
	}
	remaining -= fixedReserved

	// 3. 检索上下文块（RAG context）优先保证至少 500，最多取 min(3000, remaining/3)
	retrievalBudget = 3000
	if remaining-retrievalBudget < 1000 {
		retrievalBudget = remaining / 3
	}
	if retrievalBudget < 500 {
		retrievalBudget = 500
	}
	if retrievalBudget > remaining {
		retrievalBudget = max(remaining, 0)
	}
	remaining -= retrievalBudget

	// 4. 记忆预算：与模型窗口成正比，但封顶。8k 及以下不给记忆，省出空间给历史。
	memoryBudget = 800
	switch {
	case maxContextLength >= 32000:
		memoryBudget = 1200
	case maxContextLength >= 16000:
		memoryBudget = 1000
	case maxContextLength <= 8192:
		memoryBudget = 400
	}
	if memoryBudget > remaining {
		memoryBudget = max(remaining/2, 0)
	}
	remaining -= memoryBudget

	// 5. 历史消息预算：剩下的全给历史，保底 500，封顶 6000（防止过大的上下文拖慢模型推理）
	historyBudget = remaining
	historyBudget = max(historyBudget, 500)
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
func truncateHistoryByTokens(messages []entity.ChatMessage, maxTokens int, modelName string) []entity.ChatMessage {
	if maxTokens <= 0 {
		return nil
	}
	n := len(messages)
	if n == 0 {
		return nil
	}

	// 1. 对齐轮对边界：若最后一条是 user，尾部刚好半个轮对 → 预占它的 token，
	//    保证无论如何都能保留。
	tailIdx := n - 1
	tailReserved := 0
	tailCutMsg := (*entity.ChatMessage)(nil)
	if messages[tailIdx].Role == "user" {
		t := tokenutil.CountTokens(messages[tailIdx].Content, modelName)
		if t > maxTokens {
			cut, actual := tokenutil.TruncateByTokens(messages[tailIdx].Content, modelName, max(maxTokens-50, 50))
			if actual > 0 {
				m := messages[tailIdx]
				m.Content = cut
				tailCutMsg = &m
				tailReserved = actual
			}
		} else {
			tailReserved = t
		}
	}

	// 2. 轮对从尾部向前保留，遇到 user 开始收集一对；到 budget 不够就丢弃该整轮
	//    （不保留半截 assistant，否则会出现"assistant 的问题没人问"）
	pairs := make([][]entity.ChatMessage, 0, 4)
	total := tailReserved
	i := n - 1
	if messages[tailIdx].Role == "user" {
		i--
	}
	for i >= 0 {
		// 找一对 user i0..i：先从 i 往前走到最近的 user，再回退一条 assistant
		if messages[i].Role != "assistant" {
			// 异常孤立消息（中间多了一条 user），直接跳过这一条保持轮对完整
			i--
			continue
		}
		a := i
		u := -1
		for j := i - 1; j >= 0; j-- {
			if messages[j].Role == "user" {
				u = j
				break
			}
		}
		if u < 0 {
			break
		}
		pairTokens := 0
		for k := u; k <= a; k++ {
			pairTokens += tokenutil.CountTokens(messages[k].Content, modelName)
		}
		if total+pairTokens > maxTokens {
			// 还有至少 120 token 空间 → 截断 user 头部保留主题，丢 assistant
			remain := maxTokens - total
			if remain >= 120 {
				m := messages[u]
				cut, actual := truncateContentHeadByTokens(m.Content, modelName, remain)
				if actual > 0 {
					m.Content = cut + "\n\n（内容过长，已截断）"
					pairs = append(pairs, []entity.ChatMessage{m})
				}
			}
			break
		}
		total += pairTokens
		pair := append([]entity.ChatMessage(nil), messages[u:a+1]...)
		pairs = append(pairs, pair)
		i = u - 1
	}

	// 3. 组装：轮对顺序是"先收集的靠后"，所以要 reverse
	for l, r := 0, len(pairs)-1; l < r; l, r = l+1, r-1 {
		pairs[l], pairs[r] = pairs[r], pairs[l]
	}
	out := make([]entity.ChatMessage, 0, 2*len(pairs)+1)
	for _, p := range pairs {
		out = append(out, p...)
	}
	if tailCutMsg != nil {
		out = append(out, *tailCutMsg)
	} else if messages[tailIdx].Role == "user" && tailReserved > 0 {
		out = append(out, messages[tailIdx])
	}
	return out
}

// truncateContentHeadByTokens 从"头"按真 BPE 截断到至多 maxTokens。
// 与 tokenutil.TruncateByTokens 的区别：后者默认从左往右，这里再包一层
// 统一返回（截断后文本，实际 token）。
func truncateContentHeadByTokens(content, modelName string, maxTokens int) (string, int) {
	return tokenutil.TruncateByTokens(content, modelName, maxTokens)
}

// truncateContentByTokens 保留旧签名给现有调用方，内部转成新接口。
// 新代码优先用 tokenutil.TruncateByTokens，可拿到实际用了多少 token。
func truncateContentByTokens(content string, maxTokens int) string {
	out, _ := tokenutil.TruncateByTokens(content, "", maxTokens)
	return out
}

// ─── 可观测：反馈 / Trace / Metrics 查询接口 ───────────────────────────────────

// SubmitFeedback 提交消息反馈
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

// ListFeedbacks 分页查询用户反馈列表
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

// buildTraceResponse 构建追踪详情响应
func (s *chatService) buildTraceResponse(t *entity.ChatTrace, includeAgentDetail bool) TraceResponse {
	if t == nil {
		return TraceResponse{}
	}
	resp := TraceResponse{
		ID:         t.ID,
		RequestID:  t.RequestID,
		UserID:     t.UserID,
		SessionID:  t.SessionID,
		SearchMode: extractSearchMode(t.Attrs),
		SampleRate: t.SampleRate,
		Sampled:    t.Sampled,
		DurationMs: t.DurationMs,
		Status:     t.Status,
		Error:      t.Error,
		Attrs:      t.Attrs,
		SpanTree:   t.SpanTree,
		CreatedAt:  t.CreatedAt.Format("2006-01-02 15:04:05"),
	}
	if !includeAgentDetail || s.obsRepo == nil {
		return resp
	}
	task, steps, _ := s.obsRepo.FindByTraceID(context.Background(), t.ID)
	resp.AgentTask = chatAgentTaskEntityToResponse(task)
	resp.AgentSteps = chatAgentStepEntityToResponse(steps)
	// 有 AgentStep 信息时，把 TotalSteps / ToolCalls 反填回 AgentTask（如果之前 MarkEnded 没填充好）
	if resp.AgentTask != nil && len(resp.AgentSteps) > 0 {
		toolCalls := 0
		for _, st := range resp.AgentSteps {
			if st.ToolName != "" && st.ToolName != "llm.reasoning" {
				toolCalls++
			}
		}
		if resp.AgentTask.TotalSteps <= 0 {
			resp.AgentTask.TotalSteps = len(resp.AgentSteps)
		}
		if resp.AgentTask.ToolCalls <= 0 {
			resp.AgentTask.ToolCalls = toolCalls
		}
	}
	return resp
}

// GetTrace 根据追踪 ID 查询追踪详情
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
	resp := s.buildTraceResponse(t, true)
	return &resp, nil
}

// ListSessionTraces 分页查询会话维度的追踪列表
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
	for i := range list {
		items = append(items, s.buildTraceResponse(&list[i], false))
	}
	return TraceListResponse{Total: total, Traces: items}, nil
}

// AdminListTraces 管理员分页查询全量追踪列表
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
	for i := range list {
		items = append(items, s.buildTraceResponse(&list[i], false))
	}
	return TraceListResponse{Total: total, Traces: items}, nil
}

// GetMetricsSnapshot 获取可观测性指标快照
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
						// JS Number.MAX_SAFE_INTEGER = 9007199254740991，+Inf 语义替换为该值，保证前端 TS number 类型一致
						if s, _ := le.(string); s == "+Inf" {
							le = float64(9007199254740991)
						} else if le == "+inf" || le == "Inf" {
							le = float64(9007199254740991)
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

// extractSearchMode 从 Attrs JSON / SpanTree JSON 里取 search_mode 作为 TraceResponse 顶层字段
// 兼容 datatypes.JSON（GORM）、map[string]any（内存对象）、[]byte 三种来源；
// 找不到时再回退硬解析 ChatTrace.SpanTree root 的 attrs.search_mode，避免新老数据过渡时为空
func extractSearchMode(attrs any, spanTreeHint ...datatypes.JSON) string {
	if s := extractSearchModeFromAny(attrs); s != "" {
		return s
	}
	for _, st := range spanTreeHint {
		if len(st) == 0 {
			continue
		}
		var root struct {
			Attrs map[string]any `json:"attrs"`
		}
		if err := json.Unmarshal(st, &root); err == nil {
			if s, ok := root.Attrs["search_mode"].(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

func extractSearchModeFromAny(attrs any) string {
	if attrs == nil {
		return ""
	}
	switch v := attrs.(type) {
	case map[string]any:
		if s, ok := v["search_mode"].(string); ok {
			return s
		}
	case datatypes.JSON:
		if len(v) == 0 {
			return ""
		}
		var m map[string]any
		if err := json.Unmarshal(v, &m); err == nil {
			if s, ok := m["search_mode"].(string); ok {
				return s
			}
		}
	case []byte:
		if len(v) == 0 {
			return ""
		}
		var m map[string]any
		if err := json.Unmarshal(v, &m); err == nil {
			if s, ok := m["search_mode"].(string); ok {
				return s
			}
		}
	}
	return ""
}
