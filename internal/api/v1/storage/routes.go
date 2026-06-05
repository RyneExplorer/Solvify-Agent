package storage

import "github.com/gin-gonic/gin"

// RegisterRoutes 注册存储配额模块路由
func (ctrl *Controller) RegisterRoutes(router *gin.RouterGroup) {
	group := router.Group("/storage")
	group.GET("/quota", ctrl.Quota)
}
