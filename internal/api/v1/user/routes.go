package user

import (
	"solvify-agent/internal/middleware"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes 注册用户模块路由
func (ctrl *Controller) RegisterRoutes(r *gin.RouterGroup) {
	group := r.Group("/user")
	group.Use(middleware.Auth())
	{
		// 获取个人资料
		group.GET("/profile", ctrl.GetProfile)
		// 更新个人资料
		group.PUT("/profile", ctrl.UpdateProfile)
		// 上传头像
		group.POST("/avatar", ctrl.UploadAvatar)
		// 修改密码
		group.POST("/password", ctrl.ChangePassword)
	}
}
