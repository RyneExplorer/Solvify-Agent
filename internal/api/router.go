package api

import (
	"github.com/gin-gonic/gin"

	"solvify-agent/internal/api/v1/auth"
	"solvify-agent/internal/api/v1/chat"
	dingtalkapi "solvify-agent/internal/api/v1/dingtalk"
	"solvify-agent/internal/api/v1/document"
	"solvify-agent/internal/api/v1/knowledgebase"
	"solvify-agent/internal/api/v1/model"
	"solvify-agent/internal/api/v1/storage"
	syncapi "solvify-agent/internal/api/v1/sync"
	"solvify-agent/internal/api/v1/tool"
	"solvify-agent/internal/api/v1/user"
	"solvify-agent/internal/middleware"
	"solvify-agent/internal/repository"
	"solvify-agent/internal/service"
	"solvify-agent/pkg/response"
)

// Router 聚合 API 模块路由
type Router struct {
	userCtrl          *user.Controller
	authCtrl          *auth.Controller
	knowledgeBaseCtrl *knowledgebase.Controller
	documentCtrl      *document.Controller
	storageCtrl       *storage.Controller
	modelCtrl         *model.Controller
	userModelCtrl     *model.UserModelController
	chatCtrl          *chat.Controller
	syncCtrl          *syncapi.Controller
	dingtalkCtrl      *dingtalkapi.Controller
	toolCtrl          *tool.Controller
}

// NewRouter 创建 API 路由聚合器
func NewRouter(
	userService service.UserServiceInterface,
	authService service.AuthServiceInterface,
	modelService service.ModelServiceInterface,
	userModelConfigService service.UserModelConfigServiceInterface,
	knowledgeBaseSvc service.KnowledgeBaseServiceInterface,
	documentSvc service.DocumentServiceInterface,
	storageSvc service.StorageServiceInterface,
	chatSvc service.ChatServiceInterface,
	syncSvc service.SyncServiceInterface,
	dingtalkSvc service.DingTalkServiceInterface,
	chunkRepo repository.ChunkRepository,
	toolTypeService service.ToolTypeService,
	toolProviderService service.ToolProviderService,
	userToolConfigService service.UserToolConfigService,
) *Router {
	return &Router{
		userCtrl:          user.NewController(userService),
		authCtrl:          auth.NewController(authService, userService),
		modelCtrl:         model.NewController(modelService),
		userModelCtrl:     model.NewUserModelController(userModelConfigService),
		knowledgeBaseCtrl: knowledgebase.NewController(knowledgeBaseSvc),
		documentCtrl:      document.NewController(documentSvc, chunkRepo),
		storageCtrl:       storage.NewController(storageSvc),
		chatCtrl:          chat.NewController(chatSvc),
		syncCtrl:          syncapi.NewController(syncSvc),
		dingtalkCtrl:      dingtalkapi.NewController(dingtalkSvc),
		toolCtrl:          tool.NewController(toolTypeService, toolProviderService, userToolConfigService),
	}
}

// Setup 注册项目 HTTP 路由
func (r *Router) Setup(engine *gin.Engine) {
	// 全局 CORS 中间件
	engine.Use(middleware.CORS())

	engine.GET("/health", r.health)

	v1 := engine.Group("/api/v1")

	// 认证路由 — 公开，无需登录
	r.authCtrl.RegisterRoutes(v1)

	// 业务路由 — 需要 JWT 认证
	protected := v1.Group("")
	protected.Use(middleware.Auth())

	// 用户
	r.userCtrl.RegisterRoutes(protected)

	// 模型管理
	r.modelCtrl.RegisterRoutes(protected)
	r.userModelCtrl.RegisterRoutes(protected)

	// 聊天
	r.chatCtrl.RegisterRoutes(protected)

	// 知识库 & 文档
	r.knowledgeBaseCtrl.RegisterRoutes(protected)
	r.documentCtrl.RegisterKnowledgeBaseRoutes(protected)
	r.documentCtrl.RegisterDocumentRoutes(protected)
	r.documentCtrl.RegisterDocumentJobRoutes(protected)
	r.documentCtrl.RegisterChunkRoutes(protected)

	r.knowledgeBaseCtrl.RegisterRoutes(v1)
	r.documentCtrl.RegisterKnowledgeBaseRoutes(v1)
	r.documentCtrl.RegisterDocumentRoutes(v1)
	r.documentCtrl.RegisterDocumentJobRoutes(v1)
	r.documentCtrl.RegisterChunkRoutes(v1)
	r.syncCtrl.RegisterRoutes(v1)
	r.dingtalkCtrl.RegisterRoutes(v1)
	r.storageCtrl.RegisterRoutes(v1)
	r.toolCtrl.RegisterRoutes(user)
	// 存储
	r.storageCtrl.RegisterRoutes(protected)

	// 工具
	r.toolCtrl.RegisterRoutes(protected)
}

// health 返回服务健康状态
func (r *Router) health(c *gin.Context) {
	response.Success(c, gin.H{
		"status":  "ok",
		"service": "solvify-agent",
	})
}
