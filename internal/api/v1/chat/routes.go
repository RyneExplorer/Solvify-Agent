package chat

import "github.com/gin-gonic/gin"

// RegisterRoutes 注册聊天模块路由
func (ctrl *Controller) RegisterRoutes(router *gin.RouterGroup) {
	group := router.Group("/chat")
	group.POST("/sessions", ctrl.CreateSession)
	group.GET("/sessions", ctrl.ListSessions)
	group.GET("/sessions/:id", ctrl.GetSession)
	group.PUT("/sessions/:id", ctrl.UpdateSession)
	group.DELETE("/sessions/:id", ctrl.DeleteSession)
	group.POST("/sessions/:id/messages", ctrl.SendMessage)
	group.GET("/sessions/:id/messages", ctrl.GetMessages)
}
