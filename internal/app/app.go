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
	"go.uber.org/zap"

	"solvify-agent/internal/agent"
	"solvify-agent/internal/api"
	"solvify-agent/internal/llm"
	"solvify-agent/internal/middleware"
	"solvify-agent/internal/rag"
	"solvify-agent/internal/service"
	"solvify-agent/internal/tool"
	"solvify-agent/pkg/config"
	"solvify-agent/pkg/logger"
)

// App 是全局应用结构体，集中持有配置、依赖、路由和服务实例
type App struct {
	cfg         *config.Config
	router      *api.Router
	chatService *service.ChatService
	server      *http.Server
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

// initDependencies 初始化 Agent、Tool、RAG、LLM 和业务服务
func (a *App) initDependencies() {
	var retriever rag.Retriever
	if a.cfg.RAG.Enabled {
		retriever = rag.NewMemoryRetriever(rag.SeedDocuments())
	}

	var tools []tool.Tool
	if a.cfg.Tools.Enabled {
		tools = []tool.Tool{tool.NewCalculator()}
	}

	knowledgeAgent := agent.NewKnowledgeAgent(agent.Options{
		LLM:       llm.NewMockClient(a.cfg.LLM.Model),
		Retriever: retriever,
		Tools:     tools,
		Logger:    logger.GetLogger(),
		Model:     a.cfg.LLM.Model,
	})
	a.chatService = service.NewChatService(knowledgeAgent)
}

// initRouter 初始化 Gin 模式和项目路由
func (a *App) initRouter() {
	gin.SetMode(a.cfg.App.Mode)
	a.router = api.NewRouter(a.chatService)
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

	logger.Info("HTTP 服务已停止")
	logger.Info("=========================================")
	_ = logger.Sync()
}
