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
	userService service.UserServiceInterface
}

// NewController 创建用户控制器
func NewController(userService service.UserServiceInterface) *Controller {
	return &Controller{
		userService: userService,
	}
}

// GetProfile 获取当前用户信息
func (ctrl *Controller) GetProfile(c *gin.Context) {
	userID := middleware.GetUserID(c)
	if userID == "" {
		response.Unauthorized(c, "用户未登录")
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
	userID := middleware.GetUserID(c)
	if userID == "" {
		response.Unauthorized(c, "用户未登录")
		return
	}

	var req request.UpdateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
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
	userID := middleware.GetUserID(c)
	if userID == "" {
		response.Unauthorized(c, "用户未登录")
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
	userID := middleware.GetUserID(c)
	if userID == "" {
		response.Unauthorized(c, "用户未登录")
		return
	}

	var req request.ChangePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, err.Error())
		return
	}

	if err := ctrl.userService.ChangePassword(userID, &req); err != nil {
		response.BizError(c, err)
		return
	}

	response.Success(c, nil)
}
