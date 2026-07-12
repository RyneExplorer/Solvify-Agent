package model

import "github.com/gin-gonic/gin"

// RegisterRoutes 注册模型管理路由
func (ctrl *Controller) RegisterRoutes(router *gin.RouterGroup) {
	modelGroup := router.Group("/models")
	modelGroup.GET("", ctrl.List)
	modelGroup.GET("/:id", ctrl.Get)
	modelGroup.POST("", ctrl.Create)
	modelGroup.PUT("/:id", ctrl.Update)
	modelGroup.DELETE("/:id", ctrl.Delete)
	modelGroup.POST("/test", ctrl.Test)
}
