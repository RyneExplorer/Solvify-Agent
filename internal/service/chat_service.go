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
	"solvify-agent/pkg/traceid"
)

const (
	// sessionStatusActive 表示会话处于活跃状态
	sessionStatusActive = "active"
)

// chatService 封装聊天业务用例实现
type chatService struct {
	sessionRepo         repository.ChatSessionRepo
	messageRepo         repository.ChatMessageRepo
	modelRepo           repository.ModelRepo
	userModelConfigRepo repository.UserModelConfigRepo
	userRepo            repository.UserRepository
	userCache           *cache.RedisCache
	agentEngine         agent.AgentInterface
	contextSvc          ContextServiceInterface
	prefSvc             UserPreferenceService
	tracer              *observability.Tracer
	feedbackRepo        repository.FeedbackRepo
	txMgr               repository.TxManager
	// embedFn 复用 app.initEmbedding 的向量化函数（两级缓存 + singleflight），
	// 不是裸客户端：同一段文本重复向量化会直接打 API。
	embedFn rag.EmbeddingFunc
}

// NewChatService 创建聊天业务服务。
func NewChatService(
	sessionRepo repository.ChatSessionRepo,
	messageRepo repository.ChatMessageRepo,
	txMgr repository.TxManager,
	modelRepo repository.ModelRepo,
	userModelConfigRepo repository.UserModelConfigRepo,
	userRepo repository.UserRepository,
	userCache *cache.RedisCache,
	agentEngine agent.AgentInterface,
	contextSvc ContextServiceInterface,
	prefSvc UserPreferenceService,
	tracer *observability.Tracer,
	feedbackRepo repository.FeedbackRepo,
	embedFn rag.EmbeddingFunc,
) ChatServiceInterface {
	return &chatService{
		sessionRepo:         sessionRepo,
		messageRepo:         messageRepo,
		txMgr:               txMgr,
		modelRepo:           modelRepo,
		userModelConfigRepo: userModelConfigRepo,
		userRepo:            userRepo,
		userCache:           userCache,
		agentEngine:         agentEngine,
		contextSvc:          contextSvc,
		prefSvc:             prefSvc,
		tracer:              tracer,
		feedbackRepo:        feedbackRepo,
		embedFn:             embedFn,
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

	if req.ModelID != "" {
		s.updateUserLastModel(ctx, userID, req.ModelID)
	}

	searchMode := req.SearchMode
	if searchMode == "" {
		searchMode = "quick"
	}

	// 本次请求的关联 id：先落进 ctx，Tracer.Start 会拿它当平台侧的 traceID，
	// 于是「SSE / 消息 metadata 里的 id」与「平台上的 traceID」一定是同一个值。
	//
	// ⚠️ 这一步与「有没有接三方」无关：它是我们自己的关联标识，前端要靠它把助手消息
	// 与平台上的链路对上，所以本地不配凭据时同样要生成（Tracer 为 nil 时 Start 原样返回 ctx）。
	ctx, _ = traceid.Ensure(ctx)
	ctx = s.tracer.Start(ctx, observability.TraceRootAttrs{
		UserID:     userID,
		SessionID:  sessionID,
		MessageID:  userMsgID,
		RequestID:  requestIDFromCtx(ctx),
		SearchMode: searchMode,
		ModelID:    req.ModelID,
		// 用户提问原文 → 三方平台 trace 级 input。脱敏由官方 MaskFunc 负责，
		// 这里不做本地截断（见 TraceRootAttrs.Input 注释）。
		Input: req.Content,
	})

	eventCh := make(chan dto.StreamEvent, 100)
	go func() {
		defer close(eventCh)
		func() {
			defer func() {
				if r := recover(); r != nil {
					logger.Errorf("SendMessage goroutine panic 已恢复: sessionID=%s, err=%v", sessionID, r)
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
		}()
	}()

	return eventCh, nil
}

// updateUserLastModel 更新用户上次使用的模型（缓存比对策略）
func (s *chatService) updateUserLastModel(ctx context.Context, userID, modelID string) {
	// key 只需用户维度：userCache 实例已带 "user:model:" 前缀，
	// 这里再拼一次会变成 user:model:user:model:<userID>
	cacheKey := userID

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
		return dto.SessionResponse{}, apperrors.NotFoundOrInternal(apperrors.CodeSessionNotFound, err)
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
	// 会话与其消息必须同生共死：两步分开写、中间失败就会留下「消息已删、会话还在」
	// 的空壳 —— 用户看得见会话，点进去却是空的。两个仓库各自持有连接池，
	// 只有 InTx 能让它们落进同一个事务。
	return s.txMgr.InTx(ctx, func(ctx context.Context) error {
		if err := s.messageRepo.DeleteBySessionID(ctx, sessionID); err != nil {
			return fmt.Errorf("删除会话消息失败: %w", err)
		}
		if err := s.sessionRepo.Delete(ctx, sessionID); err != nil {
			return fmt.Errorf("删除会话失败: %w", err)
		}
		return nil
	})
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
func (s *chatService) initContext(ctx context.Context, userID, sessionID, modelID, modelType, currentQuery, currentMessageID string, toolsTokensReserve ...int) (*llm.OpenAIClient, *EnhancedContext, error) {
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
		toolsTokens = max(toolsTokensReserve[0], 0)
	}
	if _, err := llm.ResolveContextBudget(modelID, modelName, maxCtx, client.OutputLimit(), false, config.ContextDefaults()); err != nil {
		return nil, nil, err
	}
	historyBudget, retrievalBudget, memoryBudget := calculateContextBudgetsWithOutput(maxCtx, client.OutputLimit(), toolsTokens)

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

	// goroutine D: 当前问题的向量（语义召回的查询侧）。
	// 单独一个 goroutine 是为了跟用户/偏好加载重叠 —— 这次调用是一次远程 API 往返，
	// 串在 BuildContext 前面就等于把首字延迟直接加上。
	queryEmbCh := make(chan []float64, 1)
	go func() {
		queryEmbCh <- s.queryEmbedding(ctx, currentQuery)
	}()

	// goroutine C: BuildContext（内部自己已经是 5 路并行：summary/memories/recent/关键词召回/语义召回）
	go func() {
		var r ctxLoadResult
		if s.contextSvc != nil {
			contextParams := buildContextParams(historyBudget, retrievalBudget, memoryBudget, modelName, toolsTokens, <-queryEmbCh)
			contextParams.CurrentMessageID = currentMessageID
			enhancedCtx, bErr := s.contextSvc.BuildContext(ctx, userID, sessionID, currentQuery,
				contextParams,
				client.ChatModel())
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
			History:         truncateHistoryByTokens(excludeByMessageID(msg, currentMessageID), historyBudget, modelName),
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

	logger.Infof("[Timing] initContext 总耗时: cost=%dms", time.Since(t0).Milliseconds())
	return client, enhancedCtx, nil
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
		return apperrors.NotFoundOrInternal(apperrors.CodeSessionNotFound, err)
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
	// 提问也要有向量：只有答案有向量时，多轮指代（「你刚说的那个再展开下」）在
	// 语义召回里永远命中不了 —— 召回的是答案，匹配的是新问题，两边对不上。
	s.bgComputeEmbedding(ctx, userMsg.ID, userMsg.Content)
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
//
// ⚠️ 写失败是 Error 而不是 Warn：向量写不进去 ⇒ 这条消息在语义召回里**永远查不到**，
// 而聊天本身照常工作，没有任何用户可见症状。dim 一并打出来，维度不匹配一眼可见
// （系统性的维度错配由启动期 EnsureEmbeddingColumnDimension 兜住）。
func (s *chatService) bgComputeEmbedding(ctx context.Context, messageID, content string) {
	if s.embedFn == nil || strings.TrimSpace(content) == "" {
		return
	}
	go func() {
		embedCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		vec, err := s.embedFn(embedCtx, content)
		if err != nil {
			logger.Warnf("消息向量计算失败: messageID=%s, err=%v", messageID, err)
			return
		}
		f32 := make(entity.FloatVector, len(vec))
		for i, v := range vec {
			f32[i] = float32(v)
		}
		if err := s.messageRepo.UpdateEmbedding(embedCtx, messageID, f32); err != nil {
			logger.Errorf("消息向量写入失败（该消息将无法被语义召回）: messageID=%s, dim=%d, err=%v",
				messageID, len(f32), err)
		}
	}()
}

// buildContextParams 是 initContext 传给 BuildContext 的那份配置的唯一组装点。
//
// 抽出来的唯一原因是可测：initContext 整体要真 llm 客户端 + 真模型配置，
// 而「查询向量有没有真的接进 BuildContextConfig」这一步是纯组装 ——
// 它一旦漏掉，语义召回整个退化成死通道，而单测全绿（BuildContext 的用例自己传
// QueryEmbedding，压根不经过这段接线）。
func buildContextParams(historyBudget, retrievalBudget, memoryBudget int, modelName string, toolsTokens int, queryEmbedding []float64) BuildContextConfig {
	return BuildContextConfig{
		BudgetsConfigured: true,
		MaxTokens:         historyBudget,
		MaxMemories:       10,
		MaxRecentMessages: 20,
		RetrievalBudget:   retrievalBudget,
		MemoryBudget:      memoryBudget,
		ModelName:         modelName,
		ToolsTokens:       toolsTokens,
		QueryEmbedding:    queryEmbedding,
	}
}

// queryEmbedding 同步算当前问题的向量，供语义召回当查询条件。
//
// 与 bgComputeEmbedding 分成两个函数而不是共用一个，是因为等待策略相反：
// 那份是写库，失败只影响「以后还能不能被召回」，所以丢后台；这份是本次请求的
// 召回条件，拿不到就只能靠关键词通道，这里必须等得起。失败返回 nil，
// BuildContext 会自动关闭语义通道 —— 召回是补充通道，不该拖垮整轮聊天。
func (s *chatService) queryEmbedding(ctx context.Context, query string) []float64 {
	if s.embedFn == nil || strings.TrimSpace(query) == "" {
		return nil
	}
	embedCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	vec, err := s.embedFn(embedCtx, query)
	if err != nil {
		logger.Warnf("当前问题向量化失败，本次仅走关键词召回: err=%v", err)
		return nil
	}
	return vec
}

// truncateHistoryByTokens 按真 BPE token 预算从尾部向前保留"完整轮对"。
//
// 关键修复（P0-②）：旧代码"从尾部逐个 append 头插"实际是按时间正序塞，但因为
// applySummary 之前插了一条「assistant 摘要消息」在最前面，预算紧张时只塞到"摘要 + 最老几条"，
// 最新的用户问题反而被截断。现在按轮对（user + assistant 配对）从尾部保留，
// 并且保证最后一条必须是本轮 user（最后一条如果是 assistant 会在调用方补 user query，
// 所以我们只确保"若最后一条恰好是 user 就不能截掉"）。
