package api

import (
	"github.com/gin-gonic/gin"

	"solvify-agent/internal/api/v1/knowledgebase"
	"solvify-agent/internal/api/v1/storage"
	"solvify-agent/internal/service"
	"solvify-agent/pkg/response"
)

// Router 聚合 API 模块路由
type Router struct {
	knowledgeBaseCtrl *knowledgebase.Controller
	storageCtrl       *storage.Controller
}

// NewRouter 创建 API 路由聚合器
func NewRouter(knowledgeBaseService *service.KnowledgeBaseService, storageService *service.StorageService) *Router {
	return &Router{
		knowledgeBaseCtrl: knowledgebase.NewController(knowledgeBaseService),
		storageCtrl:       storage.NewController(storageService),
	}
}

// Setup 注册项目 HTTP 路由
func (r *Router) Setup(engine *gin.Engine) {
	engine.GET("/health", r.health)

	v1 := engine.Group("/api/v1")
	r.knowledgeBaseCtrl.RegisterRoutes(v1)
	r.storageCtrl.RegisterRoutes(v1)
}

// health 返回服务健康状态
func (r *Router) health(c *gin.Context) {
	response.Success(c, gin.H{
		"status":  "ok",
		"service": "solvify-agent",
	})
}
