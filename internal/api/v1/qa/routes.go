package qa

import "github.com/gin-gonic/gin"

// RegisterRoutes 注册问答模块路由
func (ctrl *Controller) RegisterRoutes(router *gin.RouterGroup) {
	qaGroup := router.Group("/qa")
	qaGroup.POST("/ask", ctrl.Ask)
}
