package model

import (
	"github.com/gin-gonic/gin"

	"solvify-agent/internal/middleware"
	requestdto "solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/service"
	"solvify-agent/pkg/response"
)

// UserModelController 处理用户模型配置请求
type UserModelController struct {
	userModelConfigService service.UserModelConfigServiceInterface
}

// NewUserModelController 创建用户模型配置控制器
func NewUserModelController(svc service.UserModelConfigServiceInterface) *UserModelController {
	return &UserModelController{userModelConfigService: svc}
}

// List 获取用户模型配置列表
func (ctrl *UserModelController) List(ctx *gin.Context) {
	userID, ok := middleware.CurrentUserID(ctx)
	if !ok {
		return
	}

	output, err := ctrl.userModelConfigService.List(ctx.Request.Context(), userID)
	if err != nil {
		response.BizError(ctx, err)
		return
	}

	response.Success(ctx, output)
}

// Create 创建用户模型配置
func (ctrl *UserModelController) Create(ctx *gin.Context) {
	userID, ok := middleware.CurrentUserID(ctx)
	if !ok {
		return
	}

	var input requestdto.CreateUserModelConfigRequest
	if err := ctx.ShouldBindJSON(&input); err != nil {
		response.BadRequest(ctx, "请求格式错误")
		return
	}

	output, err := ctrl.userModelConfigService.Create(ctx.Request.Context(), userID, input)
	if err != nil {
		response.BizError(ctx, err)
		return
	}

	response.Success(ctx, output)
}

// Get 获取单个用户模型配置
func (ctrl *UserModelController) Get(ctx *gin.Context) {
	userID, ok := middleware.CurrentUserID(ctx)
	if !ok {
		return
	}
	configID := ctx.Param("id")

	output, err := ctrl.userModelConfigService.Get(ctx.Request.Context(), userID, configID)
	if err != nil {
		response.BizError(ctx, err)
		return
	}

	response.Success(ctx, output)
}

// Update 更新用户模型配置
func (ctrl *UserModelController) Update(ctx *gin.Context) {
	userID, ok := middleware.CurrentUserID(ctx)
	if !ok {
		return
	}
	configID := ctx.Param("id")

	var input requestdto.UpdateUserModelConfigRequest
	if err := ctx.ShouldBindJSON(&input); err != nil {
		response.BadRequest(ctx, "请求体格式错误")
		return
	}

	output, err := ctrl.userModelConfigService.Update(ctx.Request.Context(), userID, configID, input)
	if err != nil {
		response.BizError(ctx, err)
		return
	}

	response.Success(ctx, output)
}

// Delete 删除用户模型配置
func (ctrl *UserModelController) Delete(ctx *gin.Context) {
	userID, ok := middleware.CurrentUserID(ctx)
	if !ok {
		return
	}
	configID := ctx.Param("id")

	if err := ctrl.userModelConfigService.Delete(ctx.Request.Context(), userID, configID); err != nil {
		response.BizError(ctx, err)
		return
	}

	response.Success(ctx, nil)
}
