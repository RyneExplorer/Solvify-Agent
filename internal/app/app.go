package app

import (
	"context"
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
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/rag"
	"solvify-agent/internal/repository"
	"solvify-agent/internal/service"
	"solvify-agent/internal/tool"
	"solvify-agent/pkg/config"
	"solvify-agent/pkg/database"
	"solvify-agent/pkg/logger"
)

// App 是全局应用结构体，集中持有配置、依赖、路由和服务实例
type App struct {
	cfg                    *config.Config
	log                    *zap.Logger
	db                     *gorm.DB
	redisClient            *redis.Client
	router                 *api.Router
	chatService            service.ChatServiceInterface
	modelService           service.ModelServiceInterface
	knowledgeService       service.KnowledgeServiceInterface
	userModelConfigService service.UserModelConfigServiceInterface
	server                 *http.Server
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
	db, err := database.OpenPostgreSQL(&a.cfg.Database.Postgres)
	if err != nil {
		return fmt.Errorf("初始化 PostgreSQL 失败: %w", err)
	}
	a.db = db

	// 自动迁移数据库表结构
	if err := db.AutoMigrate(
		&entity.Model{},
		&entity.UserModelConfig{},
		&entity.ChatSession{},
		&entity.ChatMessage{},
	); err != nil {
		return fmt.Errorf("数据库自动迁移失败: %w", err)
	}

	client, err := database.OpenRedis(&a.cfg.Database.Redis)
	if err != nil {
		_ = database.ClosePostgreSQL(a.db)
		return fmt.Errorf("初始化 Redis 失败: %w", err)
	}
	a.redisClient = client
	return nil
}

// initDependencies 初始化 Agent、Tool、RAG、LLM 和业务服务
func (a *App) initDependencies() {
	var retriever rag.Retriever
	if a.cfg.RAG.Enabled {
		// 创建 Embedding 客户端
		embeddingClient := llm.NewOpenAIEmbeddingClient(a.cfg.Embedding)

		// 尝试启用 pgvector 扩展
		if err := rag.EnablePgVector(a.db); err != nil {
			logger.Warn("启用 pgvector 扩展失败", zap.Error(err))
		}

		// 创建向量检索器
		vectorRetriever := rag.NewVectorRetriever(a.db, embeddingClient, a.cfg.RAG, logger.GetLogger())

		// 设置 ReRanker（使用 LLM 做重排序）
		llmClient := llm.NewClient(a.cfg.LLM)
		reranker := rag.NewReRanker(llmClient, a.cfg.LLM.Model, a.cfg.RAG.ScoreThreshold, logger.GetLogger())
		vectorRetriever.SetReRanker(reranker)

		retriever = vectorRetriever
		logger.Info("使用向量检索器（含 ReRank）")
	}

	var tools []tool.Tool
	if a.cfg.Tools.Enabled {
		tools = []tool.Tool{
			tool.NewCalculator(),
			tool.NewGrepChunks(a.db),
			tool.NewWebSearch(),
		}
		if retriever != nil {
			tools = append(tools, tool.NewKnowledgeSearch(retriever))
		}
	}

	llmClient := llm.NewClient(a.cfg.LLM)

	knowledgeAgent := agent.NewKnowledgeAgent(agent.Options{
		LLM:       llmClient,
		Retriever: retriever,
		Tools:     tools,
		Logger:    logger.GetLogger(),
		Model:     a.cfg.LLM.Model,
	})

	// 初始化 Repository
	modelRepo := repository.NewModelRepository(a.db)
	sessionRepo := repository.NewSessionRepository(a.db)
	messageRepo := repository.NewMessageRepository(a.db)
	userModelConfigRepo := repository.NewUserModelConfigRepository(a.db)

	// 初始化 Service
	a.modelService = service.NewModelService(modelRepo)
	a.knowledgeService = service.NewKnowledgeService()
	a.userModelConfigService = service.NewUserModelConfigService(userModelConfigRepo)
	a.chatService = service.NewChatService(knowledgeAgent, sessionRepo, messageRepo, modelRepo, a.modelService, a.userModelConfigService)

}

// seedDefaultModel 首次启动时从配置文件创建默认系统模型
func (a *App) seedDefaultModel(repo repository.ModelRepo) {
	ctx := context.Background()

	// 检查是否已有模型
	models, err := repo.List(ctx)
	if err == nil && len(models) > 0 {
		return // 已有模型，跳过
	}

	model := &entity.Model{
		Name:      a.cfg.LLM.Model,
		Provider:  a.cfg.LLM.Provider,
		ModelID:   a.cfg.LLM.Model,
		BaseURL:   a.cfg.LLM.BaseURL,
		APIKey:    a.cfg.LLM.APIKey,
		IsEnabled: true,
	}
	if err := repo.Create(ctx, model); err != nil {
		logger.Warn("创建默认系统模型失败", zap.Error(err))
		return
	}
	logger.Info("已创建默认系统模型", zap.String("model_id", model.ModelID))
}

// initRouter 初始化 Gin 模式和项目路由
func (a *App) initRouter() {
	gin.SetMode(a.cfg.App.Mode)
	a.router = api.NewRouter(a.chatService, a.modelService, a.knowledgeService, a.userModelConfigService)
}

// initServer 初始化 HTTP Server
func (a *App) initServer() {
	engine := gin.New()
	engine.Use(middleware.Recovery(logger.GetLogger()))
	engine.Use(middleware.Logger(logger.GetLogger()))
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

	if a.db != nil {
		if err := database.ClosePostgreSQL(a.db); err != nil {
			logger.Error("PostgreSQL 连接关闭失败", zap.Error(err))
		}
	}
	if a.redisClient != nil {
		if err := database.CloseRedis(a.redisClient); err != nil {
			logger.Error("Redis 连接关闭失败", zap.Error(err))
		}
	}

	logger.Info("HTTP 服务已停止")
	logger.Info("=========================================")
	_ = logger.Sync()
}
