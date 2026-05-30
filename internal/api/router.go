package api

import (
	"github.com/gin-gonic/gin"

	"solvify-agent/internal/api/v1/qa"
	"solvify-agent/internal/service"
	"solvify-agent/pkg/response"
)

// Router 聚合 API 模块路由
type Router struct {
	qaCtrl *qa.Controller
}

// NewRouter 创建 API 路由聚合器
func NewRouter(chatService *service.ChatService) *Router {
	return &Router{
		qaCtrl: qa.NewController(chatService),
	}
}

// Setup 注册项目 HTTP 路由
func (r *Router) Setup(engine *gin.Engine) {
	engine.GET("/health", r.health)

	v1 := engine.Group("/api/v1")
	r.qaCtrl.RegisterRoutes(v1)
}

// health 返回服务健康状态
func (r *Router) health(ctx *gin.Context) {
	response.Success(ctx, gin.H{
		"status":  "ok",
		"service": "solvify-agent",
	})
}
