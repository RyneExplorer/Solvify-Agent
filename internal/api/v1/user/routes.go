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
		// 完整画像（基本信息 + 偏好）
		userGroup.GET("/profile", ctrl.GetProfile)
		// 更新用户画像字段（部门/职位/擅长/语言/时区）
		userGroup.PUT("/profile/detail", ctrl.UpdateUserProfile)
		// 更新基本信息（头像/邮箱）
		userGroup.PUT("/profile", ctrl.UpdateBasicInfo)
		// 头像上传
		userGroup.POST("/avatar", ctrl.UploadAvatar)
		// 修改密码
		userGroup.POST("/password", ctrl.ChangePassword)
		// 偏好设置：查询 + 更新
		userGroup.GET("/preference", ctrl.GetPreference)
		userGroup.PUT("/preference", ctrl.UpdatePreference)
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
