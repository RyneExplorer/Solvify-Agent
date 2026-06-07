package knowledgebase

import "github.com/gin-gonic/gin"

// RegisterRoutes 注册知识库模块路由
func (ctrl *Controller) RegisterRoutes(router *gin.RouterGroup) {
	group := router.Group("/knowledge-bases")
	group.POST("", ctrl.Create)
	group.GET("", ctrl.List)
	group.GET("/:id", ctrl.Detail)
	group.PUT("/:id", ctrl.Update)
	group.DELETE("/:id", ctrl.Delete)
	group.GET("/:id/stats", ctrl.Stats)
}
