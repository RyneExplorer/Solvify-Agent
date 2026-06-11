package auth

import (
	"github.com/gin-gonic/gin"
)

// RegisterRoutes 注册认证路由
func (ctrl *Controller) RegisterRoutes(r *gin.RouterGroup) {
	group := r.Group("/auth")
	// 注册
	group.POST("/register", ctrl.Register)
	// 登录
	group.POST("/login", ctrl.Login)
	// 刷新接口
	group.POST("/refresh", ctrl.RefreshToken)
	// 登出
	group.POST("/logout", ctrl.Logout)
	// 发送邮箱验证码
	group.POST("/email/code", ctrl.SendEmailCode)
	// 忘记密码
	group.POST("/password/reset", ctrl.ResetPassword)
	// 获取图形验证码
	group.GET("/captcha", ctrl.GetCaptcha)
}
