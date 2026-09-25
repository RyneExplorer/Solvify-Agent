package chat

import (
	"solvify-agent/internal/middleware"

	"github.com/gin-gonic/gin"
)

func (ctrl *Controller) RegisterRoutes(router *gin.RouterGroup) {
	chatGroup := router.Group("/chat")
	{
		chatGroup.POST("/sessions", ctrl.CreateSession)
		chatGroup.GET("/sessions", ctrl.ListSessions)
		chatGroup.GET("/sessions/:id", ctrl.GetSession)
		chatGroup.PUT("/sessions/:id", ctrl.UpdateSession)
		chatGroup.DELETE("/sessions/:id", ctrl.DeleteSession)
		chatGroup.POST("/sessions/:id/messages", ctrl.SendMessage)
		chatGroup.GET("/sessions/:id/messages", ctrl.GetMessages)
		chatGroup.POST("/messages/:message_id/feedback", ctrl.SubmitFeedback)
		chatGroup.GET("/feedbacks", ctrl.ListFeedbacks)
	}

	adminGroup := router.Group("/admin/sessions")
	adminGroup.Use(middleware.RequireAdmin())
	{
		adminGroup.GET("", ctrl.AdminListSessions)
		adminGroup.DELETE("/:id", ctrl.AdminDeleteSession)
		adminGroup.POST("/cleanup", ctrl.AdminCleanupSessions)
	}

}
