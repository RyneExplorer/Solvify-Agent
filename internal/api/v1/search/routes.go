package search

import (
	"solvify-agent/internal/middleware"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes 注册搜索路由
func (ctrl *Controller) RegisterRoutes(router *gin.RouterGroup) {
	searchGroup := router.Group("/search")
	searchGroup.Use(middleware.Auth())
	{
		searchGroup.GET("", ctrl.Search)
	}
}
