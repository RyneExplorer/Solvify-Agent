package api

import (
	"github.com/gin-gonic/gin"

	"solvify-agent/internal/api/v1/model"
	"solvify-agent/internal/middleware"
	"solvify-agent/internal/service"
	"solvify-agent/pkg/response"
)

// Router 聚合 API 模块路由
type Router struct {
	modelCtrl     *model.Controller
	userModelCtrl *model.UserModelController
}

// NewRouter 创建 API 路由聚合器
func NewRouter(
	modelService service.ModelServiceInterface,
	userModelConfigService service.UserModelConfigServiceInterface,
) *Router {
	return &Router{
		modelCtrl:     model.NewController(modelService),
		userModelCtrl: model.NewUserModelController(userModelConfigService),
	}
}

// Setup 注册项目 HTTP 路由
func (r *Router) Setup(engine *gin.Engine) {
	engine.GET("/health", r.health)

	v1 := engine.Group("/api/v1")

	// 所有业务路由需要用户身份（开发阶段使用假用户中间件）
	user := v1.Group("")
	user.Use(middleware.FakeUser())

	// 模型管理
	r.modelCtrl.RegisterRoutes(user)
	r.userModelCtrl.RegisterRoutes(user)

}

// health 返回服务健康状态
func (r *Router) health(ctx *gin.Context) {
	response.Success(ctx, gin.H{
		"status":  "ok",
		"service": "solvify-agent",
	})
}
