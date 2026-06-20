package user

import (
	"solvify-agent/internal/middleware"

	"github.com/gin-gonic/gin"
)

// RegisterRoutes 注册用户模块路由
func (ctrl *Controller) RegisterRoutes(r *gin.RouterGroup) {
	// 普通用户：个人资料管理
	userGroup := r.Group("/user")
	{
		userGroup.GET("/profile", ctrl.GetProfile)
		userGroup.PUT("/profile", ctrl.UpdateProfile)
		userGroup.POST("/avatar", ctrl.UploadAvatar)
		userGroup.POST("/password", ctrl.ChangePassword)
	}

	// 管理员：用户管理
	adminGroup := r.Group("/admin/users")
	adminGroup.Use(middleware.RequireAdmin())
	{
		adminGroup.GET("", ctrl.AdminListUsers)
		adminGroup.POST("", ctrl.AdminCreateUser)
		adminGroup.PUT("/:id", ctrl.AdminUpdateUser)
		adminGroup.DELETE("/:id", ctrl.AdminDeleteUser)
		adminGroup.POST("/:id/reset-password", ctrl.AdminResetPassword)
	}
}
