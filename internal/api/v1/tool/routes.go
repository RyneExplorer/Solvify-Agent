package tool

import "github.com/gin-gonic/gin"

// RegisterRoutes 注册工具管理路由
func (c *Controller) RegisterRoutes(router *gin.RouterGroup) {
	// 管理员：工具类型管理
	adminTypeGroup := router.Group("/admin/tool-types")
	adminTypeGroup.GET("", c.ListToolTypes)
	adminTypeGroup.POST("", c.CreateToolType)
	adminTypeGroup.PUT("/:id", c.UpdateToolType)
	adminTypeGroup.DELETE("/:id", c.DeleteToolType)

	// 管理员：查看可用的 provider_key
	router.GET("/admin/provider-keys", c.ListProviderKeys)

	// 管理员：工具供应商管理
	adminProviderGroup := router.Group("/admin/tool-types/:id/providers")
	adminProviderGroup.GET("", c.ListToolProviders)
	adminProviderGroup.POST("", c.CreateToolProvider)
	adminProviderGroup.PUT("/:providerId", c.UpdateToolProvider)
	adminProviderGroup.DELETE("/:providerId", c.DeleteToolProvider)

	// 用户：工具模板浏览
	toolGroup := router.Group("/user/tools")
	toolGroup.GET("/templates", c.ListToolTemplates)

	// 用户：工具配置管理
	configGroup := router.Group("/user/tool-configs")
	configGroup.GET("", c.ListUserToolConfigs)
	configGroup.POST("", c.CreateUserToolConfig)
	configGroup.PUT("/:id", c.UpdateUserToolConfig)
	configGroup.DELETE("/:id", c.DeleteUserToolConfig)
}
