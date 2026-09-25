package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
	"gorm.io/gorm"

	"solvify-agent/internal/agent"
	"solvify-agent/internal/api"
	"solvify-agent/internal/integration/dingtalk"
	"solvify-agent/internal/llm"
	"solvify-agent/internal/middleware"
	"solvify-agent/internal/observability"
	"solvify-agent/internal/rag"
	"solvify-agent/internal/repository"
	"solvify-agent/internal/service"
	"solvify-agent/internal/tool"
	"solvify-agent/internal/tool/providers"
	"solvify-agent/pkg/cache"
	"solvify-agent/pkg/config"
	"solvify-agent/pkg/database"
	"solvify-agent/pkg/documentparser"
	"solvify-agent/pkg/logger"
	"solvify-agent/pkg/strutil"
)

// App 是全局应用结构体，集中持有配置、基础设施和路由实例
type App struct {
	cfg          *config.Config
	postgresqlDB *gorm.DB
	redis        *redis.Client
	// tracer 是「eino → Langfuse」这条三方链路的唯一入口，由 App 负责生命周期管理。
	// 未配凭据时是 nil，调用点（chatService）靠 nil 接收者安全空转。
	tracer       *observability.Tracer
	router       *api.Router
	server       *http.Server

	// checkpoint 过期清理后台任务的取消函数，由 App 负责生命周期管理
	checkpointCleanupCancel context.CancelFunc

	// MCP 客户端连接池，由 App 负责生命周期管理
	mcpClientPool *providers.MCPClientPool
}

// NewApp 创建应用实例
func NewApp() *App {
	return &App{}
}

// Initialize 初始化配置、日志、依赖、路由和 HTTP Server
func (a *App) Initialize() error {
	if err := a.initConfig(); err != nil {
		return err
	}
	if err := a.initLogger(); err != nil {
		return err
	}
	if err := a.initDatabase(); err != nil {
		return err
	}
	if err := a.initDependencies(); err != nil {
		return err
	}
	a.initRouter()
	a.initServer()
	return nil
}

// Run 启动 HTTP 服务并等待优雅关闭信号
func (a *App) Run() {
	go func() {
		logger.Info("HTTP 服务已启动",
			zap.String("addr", a.server.Addr),
			zap.String("mode", a.cfg.App.Mode),
		)
		if err := a.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal("HTTP 服务启动失败", zap.Error(err))
		}
	}()

	// 优雅关闭
	a.gracefulShutdown()
}

// Config 返回应用全局配置
func (a *App) Config() *config.Config {
	return a.cfg
}

// initConfig 加载项目全局配置
func (a *App) initConfig() error {
	cfg, err := config.Load("")
	if err != nil {
		return fmt.Errorf("加载配置失败: %w", err)
	}
	a.cfg = cfg
	return nil
}

// initLogger 初始化日志
func (a *App) initLogger() error {
	if err := logger.Init(&a.cfg.Log); err != nil {
		return fmt.Errorf("初始化日志失败: %w", err)
	}

	logger.Info("=========================================")
	logger.Info(fmt.Sprintf("欢迎使用 %s", a.cfg.App.Name))
	logger.Info(fmt.Sprintf("版本: %s", a.cfg.App.Version))
	logger.Info(fmt.Sprintf("环境: %s", a.cfg.App.Env))
	logger.Info(fmt.Sprintf("模式: %s", a.cfg.App.Mode))
	logger.Info("配置加载成功")
	logger.Info("=========================================")
	return nil
}

// initDatabase 初始化 PostgresSQL 和 Redis 连接
func (a *App) initDatabase() error {
	// PostgreSQL 数据库连接
	postgresqlDB, err := database.OpenPostgreSQL(&a.cfg.Database.Postgres)
	if err != nil {
		return fmt.Errorf("初始化 PostgreSQL 失败: %w", err)
	}
	a.postgresqlDB = postgresqlDB

	// pgvector 索引健康检查（仅在 enable_pgvector 时执行）
	if a.cfg.Database.Postgres.EnablePGVector {
		if err := database.EnsurePGVectorIndex(postgresqlDB); err != nil {
			logger.Warnf("pgvector 索引检查异常（不阻塞启动）: %v", err)
		}
	}

	// keywords GIN 索引健康检查（关键词检索核心加速，不依赖 pgvector 开关）
	if err := database.EnsureKeywordsGINIndex(postgresqlDB); err != nil {
		logger.Warnf("keywords GIN 索引检查异常（不阻塞启动）: %v", err)
	}

	// 上下文加载链路高频索引（chat_messages / chat_sessions / user_memories）
	if err := database.EnsureContextIndexes(postgresqlDB); err != nil {
		logger.Warnf("上下文索引检查异常（不阻塞启动）: %v", err)
	}

	// message_feedback 表 schema 补齐（早期 AutoMigrate 建表后 entity 新增列，AutoMigrate 不会 ADD COLUMN）
	if err := database.EnsureMessageFeedbackSchema(postgresqlDB); err != nil {
		logger.Warnf("message_feedback schema 补齐异常（不阻塞启动）: %v", err)
	}

	// tool_providers 表 schema 补齐（新增 is_system 列，区分系统预置与管理员自定义 MCP 供应商）
	if err := database.EnsureToolProviderSchema(postgresqlDB); err != nil {
		logger.Warnf("tool_providers schema 补齐异常（不阻塞启动）: %v", err)
	}

	// Redis 缓存连接
	redisClient, err := database.OpenRedis(&a.cfg.Database.Redis)
	if err != nil {
		_ = database.ClosePostgreSQL(a.postgresqlDB)
		return fmt.Errorf("初始化 Redis 失败: %w", err)
	}
	a.redis = redisClient
	return nil
}

const (
	embeddingInMemCacheSize = 2048
	embeddingRedisTTL       = 24 * time.Hour
)

// initEmbedding 初始化 Embedding 客户端，返回带两级缓存 + singleflight 去重的向量化函数
//
// 缓存层级：
//  1. 进程内 sync.Map fast-path（零网络 IO，容量 embeddingInMemCacheSize，LRU 清理）
//  2. Redis 共享缓存（跨进程，24h TTL）
//  3. Embedding API 调用
//
// singleflight 防止并发击穿：同一 key 的并发请求只打一次 API。
func (a *App) initEmbedding() rag.EmbeddingFunc {

	embeddingClient, err := llm.NewEmbeddingClientFromConfig(context.Background(), &a.cfg.Embedding)
	if err != nil {
		logger.Fatal("初始化 Embedding 客户端失败", zap.Error(err))
	}
	redisCache := cache.New(a.redis, "emb:", embeddingRedisTTL)
	var inMem sync.Map // map[string][]float64
	var sf singleflight.Group
	var inMemMu sync.Mutex
	inMemCount := 0

	return func(ctx context.Context, text string) ([]float64, error) {
		cacheKey := fmt.Sprintf("%x", sha256.Sum256([]byte(text)))

		// 层级 1：进程内 fast-path
		if v, ok := inMem.Load(cacheKey); ok {
			logger.Debugf("[Embedding] 内存缓存命中: key=%s", cacheKey[:8])
			return v.([]float64), nil
		}

		// singleflight 包裹：并发只调一次
		ch := sf.DoChan(cacheKey, func() (interface{}, error) {
			// 层级 2：Redis
			var vec []float64
			if found, _ := redisCache.Get(ctx, cacheKey, &vec); found {
				logger.Infof("[Embedding] Redis 缓存命中: key=%s dim=%d", cacheKey[:8], len(vec))
				inMem.Store(cacheKey, vec)
				return vec, nil
			}

			// 层级 3：调 API
			logger.Infof("[Embedding] 全缓存未命中: key=%s text=%q, 调用 API...", cacheKey[:8], strutil.Truncate(text, 60))
			vec, err := embeddingClient.Embed(ctx, text)
			if err != nil {
				logger.Errorf("[Embedding] API 调用失败: %v", err)
				return nil, err
			}

			if err := redisCache.Set(ctx, cacheKey, vec, 0); err != nil {
				logger.Warnf("[Embedding] Redis 缓存写入失败: %v", err)
			}
			inMem.Store(cacheKey, vec)

			// 简单容量管控：超阈值时删除约 20% 条目
			inMemMu.Lock()
			inMemCount++
			if inMemCount > embeddingInMemCacheSize {
				cleaned := 0
				inMem.Range(func(key, _ any) bool {
					if cleaned >= embeddingInMemCacheSize/5 {
						return false
					}
					inMem.Delete(key)
					cleaned++
					return true
				})
				inMemCount -= cleaned
				logger.Infof("[Embedding] 内存缓存清理: 删除 %d 条, 剩余约 %d", cleaned, inMemCount)
			}
			inMemMu.Unlock()

			logger.Infof("[Embedding] API 返回: dim=%d, 已写两级缓存", len(vec))
			return vec, nil
		})

		select {
		case res := <-ch:
			if res.Err != nil {
				return nil, res.Err
			}
			return res.Val.([]float64), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// initRetriever 初始化 RAG 检索器（混合检索 + 可选装饰器链）
func (a *App) initRetriever(embeddingFunc rag.EmbeddingFunc) rag.Retriever {
	// 使用混合检索器（向量 + 关键词 + RRF 融合）
	var retriever rag.Retriever = rag.NewHybridRetriever(rag.HybridRetrieverConfig{
		DB:             a.postgresqlDB,
		EmbeddingFunc:  embeddingFunc,
		ScoreThreshold: a.cfg.RAG.ScoreThreshold,
		VectorWeight:   a.cfg.RAG.VectorWeight,
		KeywordWeight:  a.cfg.RAG.KeywordWeight,
		RRFK:           a.cfg.RAG.RRFK,
	})

	// 可选：Rerank 重排序装饰器
	if a.cfg.RAG.Reranker.Enabled {
		retriever = rag.NewRerankRetrieverFromConfig(retriever)
		logger.Info("Rerank 重排序已启用")
	}

	// 可选：相邻分块扩展装饰器
	if a.cfg.RAG.Expander.Enabled {
		retriever = rag.NewExpandRetrieverFromConfig(retriever, a.postgresqlDB)
		logger.Info("相邻分块扩展已启用")
	}

	logger.Info("RAG 检索器初始化完成")
	return retriever
}

// AgentComponents 持有 Agent 相关组件
type AgentComponents struct {
	Retriever   rag.Retriever
	AgentEngine *agent.Engine
}

// initAgentComponents 初始化 Agent 相关组件（Embedding、RAG、工具注册、Agent 引擎）
// 内置工具全部通过 RegisterInternal 注册，Engine 不感知具体工具类型，新增内置工具只需要在这里多调一行
func (a *App) initAgentComponents(toolFactory tool.ToolFactory, documentRepo repository.DocumentRepository, chunkRepo repository.DocumentChunkRepository, kbRepo repository.KnowledgeBaseRepository) *AgentComponents {
	embeddingFunc := a.initEmbedding()
	vectorRetriever := a.initRetriever(embeddingFunc)

	// ── 初始化 Agent Engine ──
	agentEngine := agent.NewEngine(toolFactory, a.cfg.Agent)

	// ── 注册内置工具（按 Order 升序出现在 prompt "可用工具" 段） ──
	agentEngine.RegisterInternal("knowledge_search", 1, false,
		func(ctx context.Context, userID string, kbIDs []string) einoTool.BaseTool {
			return tool.NewKnowledgeSearchTool(vectorRetriever).WithContext(userID, kbIDs)
		})
	agentEngine.RegisterInternal("grep_chunks", 2, false,
		func(ctx context.Context, userID string, kbIDs []string) einoTool.BaseTool {
			return tool.NewGrepChunksTool(chunkRepo)(userID, kbIDs)
		})
	agentEngine.RegisterInternal("get_document_info", 3, false,
		func(ctx context.Context, userID string, kbIDs []string) einoTool.BaseTool {
			return tool.NewGetDocumentInfoTool(documentRepo)(userID, kbIDs)
		})
	agentEngine.RegisterInternal("list_knowledge_chunks", 4, false,
		func(ctx context.Context, userID string, kbIDs []string) einoTool.BaseTool {
			return tool.NewListKnowledgeChunksTool(documentRepo)(userID, kbIDs)
		})
	agentEngine.RegisterInternal("list_knowledge_bases", 5, false,
		func(ctx context.Context, userID string, kbIDs []string) einoTool.BaseTool {
			return tool.NewListKnowledgeBasesTool(kbRepo)(userID, kbIDs)
		})
	agentEngine.RegisterInternal("delete_document", 10, true,
		func(ctx context.Context, userID string, kbIDs []string) einoTool.BaseTool {
			return tool.NewDeleteDocumentTool(documentRepo)(userID, kbIDs)
		})
	agentEngine.RegisterInternal("ask_clarify", 20, false,
		func(ctx context.Context, userID string, kbIDs []string) einoTool.BaseTool {
			return tool.NewAskClarifyTool()(userID, kbIDs)
		})

	return &AgentComponents{
		Retriever:   vectorRetriever,
		AgentEngine: agentEngine,
	}
}

// initDependencies 初始化业务依赖并创建路由
func (a *App) initDependencies() error {
	// 初始化 Repository
	knowledgeBaseRepo := repository.NewKnowledgeBaseRepository(a.postgresqlDB)
	documentRepo := repository.NewDocumentRepository(a.postgresqlDB)
	documentVersionRepo := repository.NewDocumentVersionRepository(a.postgresqlDB)
	documentJobRepo := repository.NewDocumentProcessingJobRepository(a.postgresqlDB)
	syncSourceRepo := repository.NewSyncSourceRepository(a.postgresqlDB)
	syncJobRepo := repository.NewSyncJobRepository(a.postgresqlDB)
	syncItemRepo := repository.NewSyncItemRepository(a.postgresqlDB)
	syncedDocumentRepo := repository.NewSyncedDocumentRepository(a.postgresqlDB)
	dingtalkBindingRepo := repository.NewDingTalkBindingRepository(a.postgresqlDB)
	storageQuotaRepo := repository.NewStorageQuotaRepository(a.postgresqlDB)
	userRepo := repository.NewUserRepository(a.postgresqlDB)
	userPreferenceRepo := repository.NewUserPreferenceRepository(a.postgresqlDB)
	feedbackRepo := repository.NewFeedbackRepository(a.postgresqlDB)
	agentCheckpointRepo := repository.NewAgentCheckpointRepository(a.postgresqlDB)

	// 模型配置缓存（10 分钟 TTL）
	modelCache := cache.New(a.redis, "model:", 10*time.Minute)
	// 用户模型配置是另一份数据，单独命名空间，避免与系统模型共用实例导致 key 归属混乱
	userModelConfigCache := cache.New(a.redis, "user:model:config:", 10*time.Minute)
	modelRepo := repository.NewCachedModelRepository(repository.NewModelRepository(a.postgresqlDB), modelCache)
	userModelConfigRepo := repository.NewCachedUserModelConfigRepository(repository.NewUserModelConfigRepository(a.postgresqlDB), userModelConfigCache)
	txMgr := repository.NewTxManager(a.postgresqlDB)
	chatSessionRepo := repository.NewChatSessionRepository(a.postgresqlDB)
	chatMessageRepo := repository.NewChatMessageRepository(a.postgresqlDB)
	memoryRepo := repository.NewUserMemoryRepository(a.postgresqlDB)
	summaryRepo := repository.NewSummaryRepository(a.postgresqlDB)
	// 工具配置——原始仓库
	toolTypeRepo := repository.NewToolTypeRepository(a.postgresqlDB)
	toolProviderRepo := repository.NewToolProviderRepository(a.postgresqlDB)
	rawUserToolConfigRepo := repository.NewUserToolConfigRepository(a.postgresqlDB)

	// Redis 缓存（写时失效，10 分钟 TTL 兜底）
	toolTypeCache := cache.New(a.redis, "tool:type:", 10*time.Minute)
	toolConfigCache := cache.New(a.redis, "tool:config:", 10*time.Minute)
	userModelCache := cache.New(a.redis, "user:model:", 24*time.Hour)

	// 缓存装饰器
	cachedToolTypeRepo := repository.NewCachedToolTypeRepository(toolTypeRepo, toolTypeCache)
	cachedUserToolConfigRepo := repository.NewCachedUserToolConfigRepository(rawUserToolConfigRepo, toolConfigCache)

	// 预热所有已启用系统模型的 LLM 客户端（消除首次请求冷启动）
	a.prewarmModelClients(modelRepo)
	// 初始化工具 Provider 注册表——注册通用 Provider 类型
	toolRegistry := tool.NewProviderRegistry()
	toolRegistry.Register("http", providers.NewHTTPProvider()) // 通用 HTTP Provider

	// MCP Provider：一个 MCP Server 可提供多个工具（一对多映射）
	a.mcpClientPool = providers.NewMCPClientPool()
	toolRegistry.Register("mcp", providers.NewMCPProvider(a.mcpClientPool))

	// ToolFactory——Agent 引擎从 DB/Redis 加载用户配置的工具
	toolFactory := tool.NewFactory(toolRegistry, cachedUserToolConfigRepo, cachedToolTypeRepo)

	// 加载系统预置 MCP 服务器（从 config.yaml 同步到数据库）
	a.loadSystemMCPServersWithTimeout()

	// Chunk Repository（文档分块查询）
	chunkRepo := repository.NewDocumentChunkRepository(a.postgresqlDB)

	// 初始化 Agent 组件（传入 ToolFactory + DocumentRepo + ChunkRepo + KnowledgeBaseRepo）
	ai := a.initAgentComponents(toolFactory, documentRepo, chunkRepo, knowledgeBaseRepo)

	// 注入 DB 版 CheckPointStore 所需的 AgentCheckpointRepo
	ai.AgentEngine.WithCheckpointRepo(agentCheckpointRepo)

	// 启动 checkpoint 过期清理后台任务：定期删除 agent_checkpoints 中超过 TTL 的行。
	// Eino 框架不自动调用 CheckPointStore.Delete（Delete 是可选接口），原 DeleteExpired 是死代码（无调用方）。
	// 这里起 Ticker 周期性清理，与 runWithRunner 恢复成功后即时删除互补，彻底堵住 checkpoint 字节泄漏。
	{
		cleanupCtx, cleanupCancel := context.WithCancel(context.Background())
		a.checkpointCleanupCancel = cleanupCancel
		const cleanupInterval = time.Hour
		go func() {
			ticker := time.NewTicker(cleanupInterval)
			defer ticker.Stop()
			for {
				select {
				case <-cleanupCtx.Done():
					logger.Info("checkpoint 过期清理任务已停止")
					return
				case <-ticker.C:
					n, dErr := agentCheckpointRepo.DeleteExpired(cleanupCtx, time.Now())
					if dErr != nil {
						logger.Warnf("[CheckpointCleanup] 删除过期 checkpoint 失败: %v", dErr)
					} else if n > 0 {
						logger.Infof("[CheckpointCleanup] 已清理 %d 条过期 checkpoint", n)
					}
				}
			}
		}()
		logger.Infof("checkpoint 过期清理任务已启动: interval=%v", cleanupInterval)
	}

	// 初始化 Service
	prefSvc := service.NewUserPreferenceService(userPreferenceRepo)
	userSvc := service.NewUserService(userRepo, prefSvc, userModelCache)
	adminUserSvc := service.NewAdminUserService(userRepo)
	adminSessionSvc := service.NewAdminSessionService(chatSessionRepo, chatMessageRepo, txMgr)
	authSvc := service.NewAuthService(userRepo, userSvc, a.redis)
	modelService := service.NewModelService(modelRepo)
	userModelConfigService := service.NewUserModelConfigService(userModelConfigRepo)
	knowledgeBaseSvc := service.NewKnowledgeBaseService(knowledgeBaseRepo)
	embeddingSvc := service.NewEmbeddingService(a.cfg.Embedding)
	documentChunkSvc := service.NewDocumentChunkService(embeddingSvc)
	textExtractor := documentparser.New(documentparser.Config{
		PythonPath:     a.cfg.DocumentParser.PythonPath,
		ScriptPath:     a.cfg.DocumentParser.ScriptPath,
		TimeoutSeconds: a.cfg.DocumentParser.TimeoutSeconds,
	})
	documentSvc := service.NewDocumentServiceWithChunkService(knowledgeBaseRepo, documentRepo, documentVersionRepo, documentJobRepo, storageQuotaRepo, txMgr, chunkRepo, documentChunkSvc, textExtractor, "data/uploads")
	dingtalkClient := dingtalk.NewClient(a.cfg.DingTalk)
	dingtalkStateCache := cache.New(a.redis, "dingtalk:oauth:state:", 10*time.Minute)
	dingtalkSvc := service.NewDingTalkService(a.cfg.DingTalk, dingtalkBindingRepo, dingtalkStateCache, dingtalkClient)
	syncSvc := service.NewSyncService(knowledgeBaseRepo, syncSourceRepo, syncJobRepo, syncItemRepo, syncedDocumentRepo, dingtalkBindingRepo, documentChunkSvc, textExtractor, dingtalkClient, "data/uploads")
	storageSvc := service.NewStorageService(storageQuotaRepo)
	// 三方链路：只有「eino → Langfuse」这一条，没有自研 span 树 / 落库 / 指标出口。
	// NewTracer 在凭据不全时返回 (nil, nil)，调用点靠 nil 接收者空转，不必各自判分支。
	obsCfg := a.cfg.Observability
	tracer, tErr := observability.NewTracer(context.Background(), obsCfg)
	if tErr != nil {
		logger.Errorf("Langfuse 初始化失败，三方链路本次不生效: %v", tErr)
	} else {
		a.tracer = tracer
	}

	contextSvc := service.NewContextService(chatMessageRepo, memoryRepo, summaryRepo)
	chatSvc, err := service.NewChatService(chatSessionRepo, chatMessageRepo, txMgr, ai.Retriever, modelRepo, userModelConfigRepo, userRepo, userModelCache, ai.AgentEngine, contextSvc, prefSvc, a.tracer, feedbackRepo)
	if err != nil {
		return fmt.Errorf("初始化聊天服务失败: %w", err)
	}
	toolTypeService := service.NewToolTypeService(cachedToolTypeRepo, toolProviderRepo, cachedUserToolConfigRepo, txMgr)
	toolProviderService := service.NewToolProviderService(toolProviderRepo, cachedToolTypeRepo, toolRegistry, cachedUserToolConfigRepo, txMgr)
	userToolConfigService := service.NewUserToolConfigService(cachedUserToolConfigRepo, cachedToolTypeRepo, toolProviderRepo, toolRegistry)
	searchSvc := service.NewSearchService(chatMessageRepo, chunkRepo)

	// 路由
	a.router = api.NewRouter(
		userSvc,
		adminUserSvc,
		adminSessionSvc,
		searchSvc,
		authSvc,
		modelService,
		userModelConfigService,
		knowledgeBaseSvc,
		documentSvc,
		storageSvc,
		chatSvc,
		syncSvc,
		dingtalkSvc,
		toolTypeService,
		toolProviderService,
		userToolConfigService,
		prefSvc,
	)

	return nil
}

// prewarmModelClients 启动时预创建所有已启用系统模型的 LLM 客户端
func (a *App) prewarmModelClients(modelRepo repository.ModelRepo) {
	models, err := modelRepo.List(context.Background())
	if err != nil {
		logger.Warnf("预热模型客户端: 查询系统模型列表失败: %v", err)
		return
	}

	// 预热入参用与请求路径同一个结构（llm.ModelConfig），
	// 而不是它的子集 —— 子集漏字段正是预热失效的原因（见 llm.PrewarmClients 注释）。
	infos := make([]llm.ModelConfig, 0, len(models))
	for _, m := range models {
		infos = append(infos, llm.ModelConfig{
			Provider:         m.Provider,
			ModelID:          m.ModelID,
			BaseURL:          m.BaseURL,
			APIKey:           m.APIKey,
			Config:           m.Config,
			MaxContextLength: m.MaxContextLength,
		})
	}
	logger.Infof("预热模型客户端: 从数据库加载到 %d 个已启用系统模型", len(infos))
	llm.PrewarmClients(context.Background(), infos)
}

// initRouter 初始化路由
func (a *App) initRouter() {
	// 设置 Gin 模式
	gin.SetMode(a.cfg.App.Mode)
}

// initServer 初始化 HTTP Server
func (a *App) initServer() {
	engine := gin.New()
	engine.Use(middleware.Recovery())
	engine.Use(middleware.CORS())
	engine.Use(middleware.Logger())
	a.router.Setup(engine)

	a.server = &http.Server{
		Addr:              a.cfg.Server.Addr(),
		Handler:           engine,
		ReadHeaderTimeout: 5 * time.Second,
	}
}

func (a *App) gracefulShutdown() {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(quit)

	<-quit
	logger.Info("正在关闭 HTTP 服务")
	timeout := time.Duration(a.cfg.Server.ShutdownTimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if err := a.server.Shutdown(ctx); err != nil {
		logger.Fatal("HTTP 服务关闭失败", zap.Error(err))
	}

	// 三方链路关闭：官方 v2 handler 自己持有 OTLP 批处理器与 TracerProvider，
	// 必须 Shutdown 才会把队列里剩余的 span 导出并释放 provider。
	// 只 Flush 不 Shutdown 会在退出前丢掉最后一批（最多一个 BatchTimeout 窗口的 span）。
	if a.tracer != nil {
		lfCtx, lfCancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := a.tracer.Close(lfCtx); err != nil {
			logger.Errorf("Langfuse 关闭失败: %v", err)
		}
		lfCancel()
	}

	if a.postgresqlDB != nil {
		if err := database.ClosePostgreSQL(a.postgresqlDB); err != nil {
			logger.Error("PostgresSQL 连接关闭失败", zap.Error(err))
		}
	}
	if a.redis != nil {
		if err := database.CloseRedis(a.redis); err != nil {
			logger.Error("Redis 连接关闭失败", zap.Error(err))
		}
	}

	// 关闭 MCP 客户端连接池（清理 stdio 子进程等资源）
	if a.mcpClientPool != nil {
		if err := a.mcpClientPool.Close(); err != nil {
			logger.Errorf("MCP 客户端连接池关闭失败: %v", err)
		}
	}

	// 停止 checkpoint 过期清理后台任务
	if a.checkpointCleanupCancel != nil {
		a.checkpointCleanupCancel()
	}

	logger.Info("HTTP 服务已停止")
	logger.Info("=========================================")
	_ = logger.Sync()
}
