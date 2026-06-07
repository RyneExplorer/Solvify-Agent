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

	"solvify-agent/internal/api"
	"solvify-agent/internal/middleware"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
	"solvify-agent/internal/service"
	"solvify-agent/pkg/config"
	"solvify-agent/pkg/database"
	"solvify-agent/pkg/logger"
)

// App 是全局应用结构体，集中持有配置、基础设施和路由实例
type App struct {
	cfg                    *config.Config
	postgresqlDB           *gorm.DB
	redis                  *redis.Client
	router                 *api.Router
	server                 *http.Server
	modelService           service.ModelServiceInterface
	userModelConfigService service.UserModelConfigServiceInterface
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
	if err := postgresqlDB.AutoMigrate(
		&entity.Model{},
		&entity.UserModelConfig{},
	); err != nil {
		return fmt.Errorf("数据库自动迁移失败: %w", err)
	}

	// redis
	redisClient, err := database.OpenRedis(&a.cfg.Database.Redis)
	if err != nil {
		_ = database.ClosePostgreSQL(a.postgresqlDB)
		return fmt.Errorf("初始化 Redis 失败: %w", err)
	}
	a.redis = redisClient
	return nil
}

// initDependencies 初始化业务依赖并创建路由
func (a *App) initDependencies() {
	// 初始化 Repository
	knowledgeBaseRepo := repository.NewKnowledgeBaseRepository(a.postgresqlDB)
	documentRepo := repository.NewDocumentRepository(a.postgresqlDB)
	documentJobRepo := repository.NewDocumentProcessingJobRepository(a.postgresqlDB)
	storageQuotaRepo := repository.NewStorageQuotaRepository(a.postgresqlDB)
	modelRepo := repository.NewModelRepository(a.postgresqlDB)
	userModelConfigRepo := repository.NewUserModelConfigRepository(a.postgresqlDB)

	// 初始化 Service
	modelService := service.NewModelService(modelRepo)
	userModelConfigService := service.NewUserModelConfigService(userModelConfigRepo)
	knowledgeBaseSvc := service.NewKnowledgeBaseService(knowledgeBaseRepo)
	documentSvc := service.NewDocumentService(knowledgeBaseRepo, documentRepo, documentJobRepo, storageQuotaRepo)
	storageSvc := service.NewStorageService(storageQuotaRepo)

	// 路由
	a.router = api.NewRouter(
		modelService,
		userModelConfigService,
		knowledgeBaseSvc,
		documentSvc,
		storageSvc)
}

// initRouter 初始化路由
func (a *App) initRouter() {
	// 设置 Gin 模式
	gin.SetMode(a.cfg.App.Mode)
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

	if a.postgresqlDB != nil {
		if err := database.ClosePostgreSQL(a.postgresqlDB); err != nil {
			logger.Error("PostgreSQL 连接关闭失败", zap.Error(err))
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
