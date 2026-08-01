package user_model_config

import "github.com/gin-gonic/gin"

// RegisterRoutes 注册用户模型配置路由
func (ctrl *Controller) RegisterRoutes(router *gin.RouterGroup) {
	configGroup := router.Group("/user/model-configs")
	configGroup.GET("", ctrl.List)
	configGroup.POST("", ctrl.Create)
	configGroup.GET("/:id", ctrl.Get)
	configGroup.PUT("/:id", ctrl.Update)
	configGroup.DELETE("/:id", ctrl.Delete)
	configGroup.POST("/test", ctrl.Test)
}
