package api

import (
	"github.com/gin-gonic/gin"

	"solvify-agent/internal/api/v1/auth"
	"solvify-agent/internal/api/v1/chat"
	dingtalkapi "solvify-agent/internal/api/v1/dingtalk"
	"solvify-agent/internal/api/v1/document"
	"solvify-agent/internal/api/v1/knowledgebase"
	"solvify-agent/internal/api/v1/model"
	usermodelconfigapi "solvify-agent/internal/api/v1/user_model_config"
	"solvify-agent/internal/api/v1/search"
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
	searchCtrl        *search.Controller
	knowledgeBaseCtrl *knowledgebase.Controller
	documentCtrl      *document.Controller
	storageCtrl       *storage.Controller
	modelCtrl         *model.Controller
	userModelCtrl     *usermodelconfigapi.Controller
	chatCtrl          *chat.Controller
	syncCtrl          *syncapi.Controller
	dingtalkCtrl      *dingtalkapi.Controller
	toolCtrl          *tool.Controller
	authService       service.AuthServiceInterface
}

// NewRouter 创建 API 路由聚合器
func NewRouter(
	userService service.UserServiceInterface,
	adminUserService service.AdminUserServiceInterface,
	adminSessionService service.AdminSessionServiceInterface,
	searchService service.SearchServiceInterface,
	authService service.AuthServiceInterface,
	modelService service.ModelServiceInterface,
	userModelConfigService service.UserModelConfigServiceInterface,
	knowledgeBaseSvc service.KnowledgeBaseServiceInterface,
	documentSvc service.DocumentServiceInterface,
	storageSvc service.StorageServiceInterface,
	chatSvc service.ChatServiceInterface,
	syncSvc service.SyncServiceInterface,
	dingtalkSvc service.DingTalkServiceInterface,
	chunkRepo repository.DocumentChunkRepository,
	toolTypeService service.ToolTypeService,
	toolProviderService service.ToolProviderService,
	userToolConfigService service.UserToolConfigService,
	prefService service.UserPreferenceService,
) *Router {
	return &Router{
		userCtrl:          user.NewController(userService, adminUserService, prefService),
		authCtrl:          auth.NewController(authService, userService),
		searchCtrl:        search.NewController(searchService),
		modelCtrl:         model.NewController(modelService),
		userModelCtrl:     usermodelconfigapi.NewController(userModelConfigService),
		knowledgeBaseCtrl: knowledgebase.NewController(knowledgeBaseSvc),
		documentCtrl:      document.NewController(documentSvc, chunkRepo),
		storageCtrl:       storage.NewController(storageSvc),
		syncCtrl:          syncapi.NewController(syncSvc),
		dingtalkCtrl:      dingtalkapi.NewController(dingtalkSvc),
		chatCtrl:          chat.NewController(chatSvc, adminSessionService),
		toolCtrl:          tool.NewController(toolTypeService, toolProviderService, userToolConfigService),
		authService:       authService,
	}
}

// Setup 注册项目 HTTP 路由
func (r *Router) Setup(engine *gin.Engine) {
	// 全局 CORS 中间件
	engine.Use(middleware.CORS())

	engine.GET("/health", r.health)

	v1 := engine.Group("/api/v1")

	// 公开认证路由
	r.authCtrl.RegisterPublicRoutes(v1)

	// 中间件认证
	v1.Use(middleware.Auth(r.authService))

	// 需要登录的认证路由
	r.authCtrl.RegisterPrivateRoutes(v1)

	// 用户管理
	r.userCtrl.RegisterRoutes(v1)

	// 搜索
	r.searchCtrl.RegisterRoutes(v1)

	// 模型管理
	r.modelCtrl.RegisterRoutes(v1)
	r.userModelCtrl.RegisterRoutes(v1)

	// 聊天管理
	r.chatCtrl.RegisterRoutes(v1)

	// 知识库 & 文档
	r.knowledgeBaseCtrl.RegisterRoutes(v1)
	r.documentCtrl.RegisterKnowledgeBaseRoutes(v1)
	r.documentCtrl.RegisterDocumentRoutes(v1)
	r.documentCtrl.RegisterDocumentJobRoutes(v1)
	r.documentCtrl.RegisterChunkRoutes(v1)
	r.syncCtrl.RegisterRoutes(v1)
	r.dingtalkCtrl.RegisterRoutes(v1)

	// 存储
	r.storageCtrl.RegisterRoutes(v1)

	// 工具
	r.toolCtrl.RegisterRoutes(v1)
}

// health 返回服务健康状态
func (r *Router) health(c *gin.Context) {
	response.Success(c, gin.H{
		"status":  "ok",
		"service": "solvify-agent",
	})
}
