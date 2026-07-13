package user

import (
	"solvify-agent/internal/middleware"
	"solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/service"
	"solvify-agent/pkg/response"
	"solvify-agent/pkg/upload"

	"github.com/gin-gonic/gin"
)

// Controller 用户控制器
type Controller struct {
	userService      service.UserServiceInterface
	adminUserService service.AdminUserServiceInterface
}

// NewController 创建用户控制器
func NewController(userService service.UserServiceInterface, adminUserService service.AdminUserServiceInterface) *Controller {
	return &Controller{
		userService:      userService,
		adminUserService: adminUserService,
	}
}

// GetProfile 获取当前用户信息
func (ctrl *Controller) GetProfile(c *gin.Context) {
	userID, ok := middleware.CurrentUserID(c)
	if !ok {
		return
	}

	user, err := ctrl.userService.GetUserByID(userID)
	if err != nil {
		response.BizError(c, err)
		return
	}

	response.Success(c, ctrl.userService.GetUserResponse(user))
}

// UpdateProfile 更新当前用户信息
func (ctrl *Controller) UpdateProfile(c *gin.Context) {
	userID, ok := middleware.CurrentUserID(c)
	if !ok {
		return
	}

	var req request.UpdateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	if err := ctrl.userService.UpdateUser(userID, &req); err != nil {
		response.BizError(c, err)
		return
	}

	response.Success(c, nil)
}

// UploadAvatar 上传并更新当前用户头像
func (ctrl *Controller) UploadAvatar(c *gin.Context) {
	userID, ok := middleware.CurrentUserID(c)
	if !ok {
		return
	}

	result, err := upload.SaveImage(c, "file", "user")
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if err := ctrl.userService.UpdateUser(userID, &request.UpdateUserRequest{
		Avatar: result.URL,
	}); err != nil {
		response.BizError(c, err)
		return
	}

	response.Success(c, gin.H{
		"avatar": result.URL,
		"url":    result.URL,
	})
}

// ChangePassword 修改密码
func (ctrl *Controller) ChangePassword(c *gin.Context) {
	userID, ok := middleware.CurrentUserID(c)
	if !ok {
		return
	}

	var req request.ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	if err := ctrl.userService.ChangePassword(userID, &req); err != nil {
		response.BizError(c, err)
		return
	}

	response.Success(c, nil)
}

// AdminListUsers 管理员查询用户列表
func (ctrl *Controller) AdminListUsers(c *gin.Context) {
	adminID := middleware.GetUserID(c)

	var req request.AdminUserListRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	result, err := ctrl.adminUserService.List(adminID, &req)
	if err != nil {
		response.BizError(c, err)
		return
	}

	response.Success(c, result)
}

// AdminCreateUser 管理员创建用户
func (ctrl *Controller) AdminCreateUser(c *gin.Context) {
	adminID := middleware.GetUserID(c)

	var req request.AdminCreateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	user, err := ctrl.adminUserService.Create(adminID, &req)
	if err != nil {
		response.BizError(c, err)
		return
	}

	response.Success(c, user)
}

// AdminUpdateUser 管理员更新用户
func (ctrl *Controller) AdminUpdateUser(c *gin.Context) {
	adminID := middleware.GetUserID(c)
	userID := c.Param("id")

	var req request.AdminUpdateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	if err := ctrl.adminUserService.Update(adminID, userID, &req); err != nil {
		response.BizError(c, err)
		return
	}

	response.Success(c, nil)
}

// AdminDeleteUser 管理员删除用户
func (ctrl *Controller) AdminDeleteUser(c *gin.Context) {
	adminID := middleware.GetUserID(c)
	userID := c.Param("id")

	if err := ctrl.adminUserService.Delete(adminID, userID); err != nil {
		response.BizError(c, err)
		return
	}

	response.Success(c, nil)
}

// AdminResetPassword 管理员重置用户密码
func (ctrl *Controller) AdminResetPassword(c *gin.Context) {
	adminID := middleware.GetUserID(c)
	userID := c.Param("id")

	var req request.AdminResetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	if err := ctrl.adminUserService.ResetPassword(adminID, userID, &req); err != nil {
		response.BizError(c, err)
		return
	}

	response.Success(c, nil)
}
