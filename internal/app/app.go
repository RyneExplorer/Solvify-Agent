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

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"solvify-agent/internal/agent"
	"solvify-agent/internal/api"
	"solvify-agent/internal/llm"
	"solvify-agent/internal/middleware"
	"solvify-agent/internal/rag"
	"solvify-agent/internal/repository"
	"solvify-agent/internal/service"
	"solvify-agent/internal/tool"
	"solvify-agent/pkg/cache"
	"solvify-agent/pkg/config"
	"solvify-agent/pkg/database"
	"solvify-agent/pkg/logger"
)

// App 是全局应用结构体，集中持有配置、基础设施和路由实例
type App struct {
	cfg          *config.Config
	postgresqlDB *gorm.DB
	redis        *redis.Client
	router       *api.Router
	server       *http.Server
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

// initDatabase 初始化 PostgreSQL 和 Redis 连接
func (a *App) initDatabase() error {
	// postgresql
	postgresqlDB, err := database.OpenPostgreSQL(&a.cfg.Database.Postgres)
	if err != nil {
		return fmt.Errorf("初始化 PostgreSQL 失败: %w", err)
	}
	a.postgresqlDB = postgresqlDB

	// 自动迁移数据库表结构
	//if err := postgresqlDB.AutoMigrate(
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
	//); err != nil {
	//	return fmt.Errorf("数据库自动迁移失败: %w", err)
	//}

	// redis
	redisClient, err := database.OpenRedis(&a.cfg.Database.Redis)
	if err != nil {
		_ = database.ClosePostgreSQL(a.postgresqlDB)
		return fmt.Errorf("初始化 Redis 失败: %w", err)
	}
	a.redis = redisClient
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
	Retriever    rag.Retriever
	ToolRegistry *tool.Registry
	AgentEngine  *agent.Engine
}

// initAgentComponents 初始化 Agent 相关组件（Embedding、RAG、工具、Agent 引擎）
func (a *App) initAgentComponents() *AgentComponents {
	embeddingFunc := a.initEmbedding()
	vectorRetriever := a.initRetriever(embeddingFunc)

	// 初始化 Tool Registry（注册所有内置工具）
	toolRegistry := tool.NewRegistry()
	toolRegistry.Register(tool.NewFinalAnswerTool())
	toolRegistry.Register(tool.NewWebSearchTool(a.cfg.Tools.WebSearch.APIKey, a.cfg.Tools.WebSearch.BaseURL))
	logger.Info("工具注册完成", zap.Int("count", len(toolRegistry.List())))

	// knowledge_search 工厂：每次 Agent 请求创建带用户上下文的工具实例
	ksFactory := agent.KnowledgeSearchFactory(func(userID string, kbIDs []string) *tool.KnowledgeSearchTool {
		return tool.NewKnowledgeSearchTool(vectorRetriever).WithContext(userID, kbIDs)
	})

	// 初始化 Agent Engine
	agentEngine := agent.NewEngine(
		toolRegistry,
		ksFactory,
		a.cfg.Agent,
	)

	return &AgentComponents{
		Retriever:    vectorRetriever,
		ToolRegistry: toolRegistry,
		AgentEngine:  agentEngine,
	}
}

// initDependencies 初始化业务依赖并创建路由
func (a *App) initDependencies() {
	// 初始化 Repository
	knowledgeBaseRepo := repository.NewKnowledgeBaseRepository(a.postgresqlDB)
	documentRepo := repository.NewDocumentRepository(a.postgresqlDB)
	documentVersionRepo := repository.NewDocumentVersionRepository(a.postgresqlDB)
	documentJobRepo := repository.NewDocumentProcessingJobRepository(a.postgresqlDB)
	storageQuotaRepo := repository.NewStorageQuotaRepository(a.postgresqlDB)
	userRepo := repository.NewUserRepository(a.postgresqlDB)

	// 模型配置缓存（10 分钟 TTL）
	modelCache := cache.New(a.redis, "model:", 10*time.Minute)
	modelRepo := repository.NewCachedModelRepository(repository.NewModelRepository(a.postgresqlDB), modelCache)
	userModelConfigRepo := repository.NewCachedUserModelConfigRepository(repository.NewUserModelConfigRepository(a.postgresqlDB), modelCache)
	chatSessionRepo := repository.NewChatSessionRepository(a.postgresqlDB)
	chatMessageRepo := repository.NewChatMessageRepository(a.postgresqlDB)

	// 预热所有已启用系统模型的 LLM 客户端（消除首次请求冷启动）
	a.prewarmModelClients(modelRepo)

	// 初始化 Agent 组件
	ai := a.initAgentComponents()

	// 初始化 Service
	userSvc := service.NewUserService(userRepo)
	authSvc := service.NewAuthService(userRepo, userSvc, a.redis)
	modelService := service.NewModelService(modelRepo)
	userModelConfigService := service.NewUserModelConfigService(userModelConfigRepo)
	knowledgeBaseSvc := service.NewKnowledgeBaseService(knowledgeBaseRepo)
	embeddingSvc := service.NewEmbeddingService(a.cfg.Embedding)
	documentChunkSvc := service.NewDocumentChunkService(embeddingSvc)
	documentSvc := service.NewDocumentServiceWithChunkService(knowledgeBaseRepo, documentRepo, documentVersionRepo, documentJobRepo, storageQuotaRepo, documentChunkSvc, "data/uploads")
	storageSvc := service.NewStorageService(storageQuotaRepo)
	chatSvc := service.NewChatService(chatSessionRepo, chatMessageRepo, ai.Retriever, modelRepo, userModelConfigRepo, ai.AgentEngine)

	// 路由
	a.router = api.NewRouter(
		userSvc,
		authSvc,
		modelService,
		userModelConfigService,
		knowledgeBaseSvc,
		documentSvc,
		storageSvc,
		chatSvc)
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
