package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
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
)

// App 是全局应用结构体，集中持有配置、基础设施和路由实例
type App struct {
	cfg          *config.Config
	postgresqlDB *gorm.DB
	redis        *redis.Client
	obsRecorder  observability.Recorder
	router       *api.Router
	server       *http.Server

	// 阶段 1.4：OTel / Prometheus 资源，由 App 负责生命周期管理
	tracerShutdown func(context.Context) error
	promRegistry   *prometheus.Registry
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
	a.initDependencies()
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
	//自动迁移数据库表结构
	//if err := postgresqlDB.AutoMigrate(
	//	&entity.User{},
	//	&entity.Model{},
	//	&entity.UserModelConfig{},
	//	&entity.KnowledgeBase{},
	//	&entity.StorageQuota{},
	//	&entity.Document{},
	//	&entity.DocumentProcessingJob{},
	//	&entity.DocumentVersion{},
	//	&entity.DocumentChunk{},
	//	&entity.ChatSession{},
	//	&entity.ChatMessage{},
	//	&entity.ToolType{},
	//	&entity.ToolProvider{},
	//	&entity.UserToolConfig{},
	//); err != nil {
	//	return fmt.Errorf("数据库自动迁移失败: %w", err)
	//}

	// Redis 缓存连接
	redisClient, err := database.OpenRedis(&a.cfg.Database.Redis)
	if err != nil {
		_ = database.ClosePostgreSQL(a.postgresqlDB)
		return fmt.Errorf("初始化 Redis 失败: %w", err)
	}
	a.redis = redisClient
	return nil
}

// ensureStorageQuotaUniqueIndex 确保存储配额用户唯一索引存在
func (a *App) ensureStorageQuotaUniqueIndex(db *gorm.DB) error {
	if err := db.Exec("CREATE UNIQUE INDEX IF NOT EXISTS storage_quotas_user_unique ON storage_quotas(user_id)").Error; err != nil {
		return fmt.Errorf("创建存储配额用户唯一索引失败: %w", err)
	}
	return nil
}

// initEmbedding 初始化 Embedding 客户端，返回带缓存的向量化函数
func (a *App) initEmbedding() rag.EmbeddingFunc {

	embeddingClient, err := llm.NewEmbeddingClientFromConfig(context.Background(), &a.cfg.Embedding)
	if err != nil {
		logger.Fatal("初始化 Embedding 客户端失败", zap.Error(err))
	}

	// Embedding 缓存（相同文本 → 相同向量，缓存 24 小时）
	embeddingCache := cache.New(a.redis, "emb:", 24*time.Hour)

	return func(ctx context.Context, text string) ([]float64, error) {
		cacheKey := fmt.Sprintf("%x", sha256.Sum256([]byte(text)))
		var vec []float64
		if found, _ := embeddingCache.Get(ctx, cacheKey, &vec); found {
			logger.Infof("[Embedding] 缓存命中: key=%s text=%q dim=%d", cacheKey[:8], text, len(vec))
			return vec, nil
		}
		logger.Infof("[Embedding] 缓存未命中: key=%s text=%q, 调用 API...", cacheKey[:8], text)
		vec, err := embeddingClient.Embed(ctx, text)
		if err != nil {
			logger.Errorf("[Embedding] API 调用失败: %v", err)
			return nil, err
		}
		logger.Infof("[Embedding] API 返回: dim=%d", len(vec))
		if err := embeddingCache.Set(ctx, cacheKey, vec, 0); err != nil {
			logger.Warnf("[Embedding] 缓存写入失败: %v", err)
		} else {
			logger.Infof("[Embedding] 已写入缓存: key=%s", cacheKey[:8])
		}
		return vec, nil
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
	if a.obsRecorder != nil {
		agentEngine.WithObservability(a.obsRecorder)
	}

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

	return &AgentComponents{
		Retriever:   vectorRetriever,
		AgentEngine: agentEngine,
	}
}

// initDependencies 初始化业务依赖并创建路由
func (a *App) initDependencies() {
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
	obsRepo := repository.NewObservabilityRepository(a.postgresqlDB)
	agentCheckpointRepo := repository.NewAgentCheckpointRepository(a.postgresqlDB)

	// 阶段 1.4：可观测性初始化（OTel Tracer + Prometheus Registry + Recorder）
	//
	// 顺序很重要：必须先 InitTracerProvider / InitPrometheusRegistry，再 NewRecorder，
	// 否则 NewRecorder 拿到的 GlobalMetrics() 是兜底空 metrics，/metrics 路由不会暴露真实指标。
	obsCfg := a.cfg.Observability
	ctx := context.Background()
	tp, tracerShutdown, err := observability.InitTracerProvider(ctx, obsCfg)
	if err != nil {
		logger.Warnf("OTel TracerProvider 初始化失败，回退 noop: %v", err)
	} else {
		a.tracerShutdown = tracerShutdown
		if tp != nil {
			logger.Infof("OTel TracerProvider 已初始化: exporter=%s sample_rate=%.2f", obsCfg.OTelExporter, obsCfg.OTelSamplingRate)
		}
	}
	promReg := observability.InitPrometheusRegistry(obsCfg)
	a.promRegistry = promReg
	logger.Infof("Prometheus Registry 已初始化")

	// 阶段三：初始化可观测性 Recorder（DB Sink + 批量日志 Sink + 采样器 + PII）
	// NewRecorder 内部调 GlobalMetrics() / GlobalTracer()，已经在上一步被赋值
	if !obsCfg.Enabled {
		a.obsRecorder = observability.NewRecorder(obsCfg)
	} else {
		a.obsRecorder = observability.NewRecorderWithDBSink(obsCfg, obsRepo)
	}
	logger.Infof("可观测性模块初始化: enabled=%v sample_rate=%.2f db_sink=%v otel_exporter=%s", obsCfg.Enabled, obsCfg.SamplingRate, obsCfg.TraceTableEnabled, obsCfg.OTelExporter)
	// 注册 eino 全局 callback：所有走 eino 标准接口的组件（ChatModel / Retriever / Tool / Embedding / Agent / Graph）
	// 会自动打 span 和通用指标，不用再在业务代码里手动成对 StartSpan/EndSpan。
	observability.RegisterGlobalEinoCallback(a.obsRecorder)

	// 模型配置缓存（10 分钟 TTL）
	modelCache := cache.New(a.redis, "model:", 10*time.Minute)
	modelRepo := repository.NewCachedModelRepository(repository.NewModelRepository(a.postgresqlDB), modelCache)
	userModelConfigRepo := repository.NewCachedUserModelConfigRepository(repository.NewUserModelConfigRepository(a.postgresqlDB), modelCache)
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
	// ToolFactory——Agent 引擎从 DB/Redis 加载用户配置的工具
	toolFactory := tool.NewFactory(toolRegistry, cachedUserToolConfigRepo, cachedToolTypeRepo)

	// Chunk Repository（文档分块查询）
	chunkRepo := repository.NewDocumentChunkRepository(a.postgresqlDB)

	// 初始化 Agent 组件（传入 ToolFactory + DocumentRepo + ChunkRepo + KnowledgeBaseRepo）
	ai := a.initAgentComponents(toolFactory, documentRepo, chunkRepo, knowledgeBaseRepo)

	// 注入 DB 版 CheckPointStore 所需的 AgentCheckpointRepo
	ai.AgentEngine.WithCheckpointRepo(agentCheckpointRepo)

	// 初始化 Service
	prefSvc := service.NewUserPreferenceService(userPreferenceRepo)
	userSvc := service.NewUserService(userRepo, prefSvc, userModelCache)
	adminUserSvc := service.NewAdminUserService(userRepo)
	adminSessionSvc := service.NewAdminSessionService(chatSessionRepo, chatMessageRepo)
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
	documentSvc := service.NewDocumentServiceWithChunkService(knowledgeBaseRepo, documentRepo, documentVersionRepo, documentJobRepo, storageQuotaRepo, documentChunkSvc, textExtractor, "data/uploads")
	dingtalkClient := dingtalk.NewClient(a.cfg.DingTalk)
	dingtalkStateCache := cache.New(a.redis, "dingtalk:oauth:state:", 10*time.Minute)
	dingtalkSvc := service.NewDingTalkService(a.cfg.DingTalk, dingtalkBindingRepo, dingtalkStateCache, dingtalkClient)
	syncSvc := service.NewSyncService(knowledgeBaseRepo, syncSourceRepo, syncJobRepo, syncItemRepo, syncedDocumentRepo, dingtalkBindingRepo, documentChunkSvc, textExtractor, dingtalkClient, "data/uploads")
	storageSvc := service.NewStorageService(storageQuotaRepo)
	contextSvc := service.NewContextService(chatMessageRepo, memoryRepo, summaryRepo, a.obsRecorder)
	chatSvc := service.NewChatService(chatSessionRepo, chatMessageRepo, ai.Retriever, modelRepo, userModelConfigRepo, userRepo, userModelCache, ai.AgentEngine, contextSvc, prefSvc, a.obsRecorder, obsRepo)
	toolTypeService := service.NewToolTypeService(cachedToolTypeRepo)
	toolProviderService := service.NewToolProviderService(toolProviderRepo, cachedToolTypeRepo, toolRegistry)
	userToolConfigService := service.NewUserToolConfigService(cachedUserToolConfigRepo, cachedToolTypeRepo, toolProviderRepo, toolRegistry)
	searchSvc := service.NewSearchService(chatMessageRepo, chunkRepo)

	// 路由
	// 阶段 1.4：把 Prometheus Registry 注入 Router，/metrics 路由直接挂 promhttp.HandlerFor(promReg)
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
		chunkRepo,
		toolTypeService,
		toolProviderService,
		userToolConfigService,
		prefSvc,
		a.promRegistry, // 阶段 1.4：替换原 obsRecorder，/metrics 走 promhttp.Handler
	)
}

// prewarmModelClients 启动时预创建所有已启用系统模型的 LLM 客户端
func (a *App) prewarmModelClients(modelRepo repository.ModelRepo) {
	models, err := modelRepo.List(context.Background())
	if err != nil {
		logger.Warnf("预热模型客户端: 查询系统模型列表失败: %v", err)
		return
	}

	infos := make([]llm.SystemModelInfo, 0, len(models))
	for _, m := range models {
		infos = append(infos, llm.SystemModelInfo{
			ModelID: m.ModelID,
			BaseURL: m.BaseURL,
			APIKey:  m.APIKey,
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
	if a.obsRecorder != nil {
		// 阶段三：可观测性链路中间件（生成 request_id、记录 HTTP 指标、panic 恢复）
		engine.Use(observability.NewTraceMiddleware(a.obsRecorder).Handler())
	}
	a.router.Setup(engine)

	a.server = &http.Server{
		Addr:              a.cfg.Server.Addr(),
		Handler:           engine,
		ReadHeaderTimeout: 5 * time.Second,
	}
}

// gracefulShutdown 监听退出信号并优雅关闭服务
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

	// 阶段三：优雅关闭可观测性 recorder（刷新批量 Sink）
	if a.obsRecorder != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := a.obsRecorder.Shutdown(shutdownCtx); err != nil {
			logger.Errorf("可观测性 recorder 关闭失败: %v", err)
		}
	}
	// 阶段 1.4：优雅关闭 OTel TracerProvider（flush 还在 batcher 里的 span 到 exporter）
	if a.tracerShutdown != nil {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()
		if err := a.tracerShutdown(shutdownCtx); err != nil {
			logger.Errorf("OTel TracerProvider 关闭失败: %v", err)
		}
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

	logger.Info("HTTP 服务已停止")
	logger.Info("=========================================")
	_ = logger.Sync()
}
