package api

import (
	"github.com/gin-gonic/gin"

	"solvify-agent/internal/api/v1/chat"
	"solvify-agent/internal/api/v1/auth"
	"solvify-agent/internal/api/v1/document"
	"solvify-agent/internal/api/v1/knowledgebase"
	"solvify-agent/internal/api/v1/model"
	"solvify-agent/internal/api/v1/storage"
	"solvify-agent/internal/api/v1/user"
	"solvify-agent/internal/middleware"
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
) *Router {
	return &Router{
		userCtrl:          user.NewController(userService),
		authCtrl:          auth.NewController(authService, userService),
		modelCtrl:         model.NewController(modelService),
		userModelCtrl:     model.NewUserModelController(userModelConfigService),
		knowledgeBaseCtrl: knowledgebase.NewController(knowledgeBaseSvc),
		documentCtrl:      document.NewController(documentSvc),
		storageCtrl:       storage.NewController(storageSvc),
		chatCtrl:          chat.NewController(chatSvc),
	}
}

// Setup 注册项目 HTTP 路由
func (r *Router) Setup(engine *gin.Engine) {
	// 全局 CORS 中间件
	engine.Use(middleware.CORS())

	engine.GET("/health", r.health)

	v1 := engine.Group("/api/v1")

	// 所有业务路由需要用户身份（开发阶段使用假用户中间件）
	user := v1.Group("")
	user.Use(middleware.FakeUser())

	// 模型管理
	r.modelCtrl.RegisterRoutes(user)
	r.userModelCtrl.RegisterRoutes(user)

	// 聊天
	r.chatCtrl.RegisterRoutes(user)

	r.knowledgeBaseCtrl.RegisterRoutes(v1)
	r.documentCtrl.RegisterKnowledgeBaseRoutes(v1)
	r.documentCtrl.RegisterDocumentRoutes(v1)
	r.documentCtrl.RegisterDocumentJobRoutes(v1)
	r.storageCtrl.RegisterRoutes(v1)
}

// health 返回服务健康状态
func (r *Router) health(c *gin.Context) {
	response.Success(c, gin.H{
		"status":  "ok",
		"service": "solvify-agent",
	})
}
