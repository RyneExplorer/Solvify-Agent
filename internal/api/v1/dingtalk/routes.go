package dingtalk

import "github.com/gin-gonic/gin"

// RegisterRoutes 注册钉钉绑定模块路由
func (ctrl *Controller) RegisterRoutes(router *gin.RouterGroup) {
	group := router.Group("/dingtalk")
	group.GET("/oauth-config", ctrl.OAuthConfig)
	group.POST("/auth-code/exchange", ctrl.ExchangeAuthCode)
	group.GET("/binding", ctrl.Binding)
	group.DELETE("/binding", ctrl.DeleteBinding)
	group.GET("/workspaces", ctrl.ListWorkspaces)
	group.GET("/workspaces/:workspace_id/nodes", ctrl.ListNodes)
}
