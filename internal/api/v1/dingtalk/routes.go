package dingtalk

import "github.com/gin-gonic/gin"

// RegisterRoutes 注册钉钉绑定模块路由
func (ctrl *Controller) RegisterRoutes(router *gin.RouterGroup) {
	group := router.Group("/dingtalk")
	// 获取前端内嵌二维码登录参数
	group.GET("/oauth-config", ctrl.OAuthConfig)
	// 兑换钉钉扫码授权码并保存绑定
	group.POST("/auth-code/exchange", ctrl.ExchangeAuthCode)
	// 查询当前用户钉钉绑定状态
	group.GET("/binding", ctrl.Binding)
	// 删除当前用户钉钉绑定
	group.DELETE("/binding", ctrl.DeleteBinding)
	// 查询钉钉知识库列表
	group.GET("/workspaces", ctrl.ListWorkspaces)
	// 查询钉钉知识库节点列表
	group.GET("/workspaces/:workspace_id/nodes", ctrl.ListNodes)
}
