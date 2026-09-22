package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"solvify-agent/internal/api/v1/auth"
	"solvify-agent/internal/api/v1/chat"
	dingtalkapi "solvify-agent/internal/api/v1/dingtalk"
	"solvify-agent/internal/api/v1/document"
	"solvify-agent/internal/api/v1/knowledgebase"
	"solvify-agent/internal/api/v1/model"
	"solvify-agent/internal/api/v1/search"
	"solvify-agent/internal/api/v1/storage"
	syncapi "solvify-agent/internal/api/v1/sync"
	"solvify-agent/internal/api/v1/tool"
	"solvify-agent/internal/api/v1/user"
	usermodelconfigapi "solvify-agent/internal/api/v1/user_model_config"
	"solvify-agent/internal/middleware"
	"solvify-agent/internal/service"
	"solvify-agent/pkg/logger"
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
	promRegistry      *prometheus.Registry
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
	toolTypeService service.ToolTypeService,
	toolProviderService service.ToolProviderService,
	userToolConfigService service.UserToolConfigService,
	prefService service.UserPreferenceService,
	promRegistry *prometheus.Registry,
) *Router {
	r := &Router{
		userCtrl:          user.NewController(userService, adminUserService, prefService),
		authCtrl:          auth.NewController(authService, userService),
		searchCtrl:        search.NewController(searchService),
		modelCtrl:         model.NewController(modelService),
		userModelCtrl:     usermodelconfigapi.NewController(userModelConfigService),
		knowledgeBaseCtrl: knowledgebase.NewController(knowledgeBaseSvc),
		documentCtrl:      document.NewController(documentSvc),
		storageCtrl:       storage.NewController(storageSvc),
		syncCtrl:          syncapi.NewController(syncSvc),
		dingtalkCtrl:      dingtalkapi.NewController(dingtalkSvc),
		chatCtrl:          chat.NewController(chatSvc, adminSessionService),
		toolCtrl:          tool.NewController(toolTypeService, toolProviderService, userToolConfigService),
		authService:       authService,
	}
	// /metrics 路由直接挂 promhttp.HandlerFor(promRegistry) 输出标准 Prometheus 文本格式。
	//
	// 这里刻意用有类型的形参，而不是 `extra ...interface{}` + 运行时类型断言：
	// 断言不中只会静默跳过，于是「Registry 没接上」要等到有人发现 /metrics 一直是零指标
	// 才可能被察觉（且 503/404 都没有）。显式形参把这类错接线直接变成编译错误。
	r.promRegistry = promRegistry
	return r
}

// Setup 注册项目 HTTP 路由
func (r *Router) Setup(engine *gin.Engine) {
	// 全局 CORS 中间件
	engine.Use(middleware.CORS())

	engine.GET("/health", r.health)

	// GET /metrics：Prometheus 标准抓取端点（公开不登录即可访问，方便 Prometheus server 抓取）。
	// 内容里不含任何敏感字段，只是指标名/计数/分桶，所有标签值在写入前已通过 PII sanitizer 清理。
	if r.promRegistry != nil {
		engine.GET("/metrics", gin.WrapH(promhttp.HandlerFor(r.promRegistry, promhttp.HandlerOpts{})))
	} else {
		// Registry 未注入 = 装配漏了。InitPrometheusRegistry 用 sync.Once 且从不返回 nil，
		// 所以这里不可能是「用户关掉了可观测性」这种合法场景，只能是接线错了。
		//
		// 刻意不再返回 200 占位文本：Prometheus 只认 HTTP 状态码，200 + 零指标会被读成
		// 「这个服务没有指标」而不是「指标没接上」—— 沉默的失败等于没有失败信号。
		// 503 会让抓取方记 up=0，能被告警规则抓到；启动期再补一条日志，本地也看得见。
		logger.Errorf("Prometheus Registry 未注入，/metrics 将返回 503：请检查 app 装配是否漏传 registry")
		engine.GET("/metrics", func(c *gin.Context) {
			c.String(http.StatusServiceUnavailable, "prometheus registry not initialized: /metrics 未接线\n")
		})
	}

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
