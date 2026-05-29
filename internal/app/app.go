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
	"solvify-agent/internal/rag"
	"solvify-agent/internal/service"
	"solvify-agent/internal/tool"
	"solvify-agent/pkg/config"
	"solvify-agent/pkg/logger"
)

// App 是全局应用结构体，集中持有配置、依赖、路由和服务实例
type App struct {
	cfg         *config.Config
	log         *zap.Logger
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
func (a *App) Run() error {
	errCh := make(chan error, 1)
	go func() {
		a.log.Info("HTTP 服务已启动", zap.String("addr", a.server.Addr), zap.String("mode", a.cfg.App.Mode))
		if err := a.server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	return a.gracefulShutdown(errCh)
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

// initLogger 初始化 zap 日志系统
func (a *App) initLogger() error {
	if err := logger.Init(&a.cfg.Log); err != nil {
		return fmt.Errorf("初始化日志失败: %w", err)
	}

	a.log = logger.GetLogger()
	a.log.Info("配置加载成功",
		zap.String("app", a.cfg.App.Name),
		zap.String("version", a.cfg.App.Version),
		zap.String("env", a.cfg.App.Env),
		zap.String("mode", a.cfg.App.Mode),
	)
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
		Logger:    a.log,
		Model:     a.cfg.LLM.Model,
	})
	a.chatService = service.NewChatService(knowledgeAgent)
}

// initRouter 初始化 Gin 模式和项目路由
func (a *App) initRouter() {
	gin.SetMode(a.cfg.App.Mode)
	a.router = api.NewRouter(a.chatService, a.log)
}

// initServer 初始化 HTTP Server
func (a *App) initServer() {
	a.server = &http.Server{
		Addr:              a.cfg.Server.Addr(),
		Handler:           a.router.Engine(),
		ReadHeaderTimeout: 5 * time.Second,
	}
}

// gracefulShutdown 监听退出信号并优雅关闭服务
func (a *App) gracefulShutdown(errCh <-chan error) error {
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(quit)

	select {
	case err := <-errCh:
		_ = logger.Sync()
		return err
	case <-quit:
		a.log.Info("正在关闭 HTTP 服务")
		timeout := time.Duration(a.cfg.Server.ShutdownTimeoutSeconds) * time.Second
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		if err := a.server.Shutdown(ctx); err != nil {
			a.log.Error("HTTP 服务关闭失败", zap.Error(err))
			_ = logger.Sync()
			return err
		}

		a.log.Info("HTTP 服务已停止")
		return logger.Sync()
	}
}
