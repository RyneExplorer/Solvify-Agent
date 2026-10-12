package model

import (
	"solvify-agent/internal/middleware"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes 注册模型管理路由。
//
// 读写分权的原因：
//   - GET /models、GET /models/:id 必须对「已登录用户」开放。问答页与设置页都要拉
//     系统模型列表供用户选模型（design/vue 的 QAPage、useModelConfig），收进 admin 组
//     会直接把普通用户的模型选择打断。既然响应里的 APIKey 已在 service 层脱敏
//     （service.maskAPIKey），列表对外就是安全的。
//   - POST/PUT/DELETE/POST /test 是平台级配置的写操作，任何普通登录用户都不该触碰。
//     此前这些路由只挂了登录鉴权，导致任意用户可新增/改写/删除系统模型；尤其
//     /models/test 会拿服务器身份向请求里指定的任意 base_url 发出站请求，等于半个
//     SSRF 入口。故统一收进 RequireAdmin。
//
// 路径刻意保持 /models 不变（未按 admin 前缀挪到 /admin/models），是为了不动已发布的
// 管理台接口（design/vue/src/api/admin.ts 全部指向 /models）。同名路径分属两个 group
// 不冲突：gin 只在「方法 + 路径」完全相同时才 panic。
func (ctrl *Controller) RegisterRoutes(router *gin.RouterGroup) {
	modelGroup := router.Group("/models")
	modelGroup.GET("", ctrl.List)
	modelGroup.GET("/:id", ctrl.Get)

	modelAdminGroup := router.Group("/models")
	modelAdminGroup.Use(middleware.RequireAdmin())
	modelAdminGroup.POST("", ctrl.Create)
	modelAdminGroup.PUT("/:id", ctrl.Update)
	modelAdminGroup.DELETE("/:id", ctrl.Delete)
	modelAdminGroup.POST("/test", ctrl.Test)
}
