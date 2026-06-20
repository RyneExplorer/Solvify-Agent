package model

import (
	"github.com/gin-gonic/gin"

	requestdto "solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/service"
	"solvify-agent/pkg/response"
)

// Controller 处理系统模型管理请求
type Controller struct {
	modelService service.ModelServiceInterface
}

// NewController 创建系统模型控制器
func NewController(modelService service.ModelServiceInterface) *Controller {
	return &Controller{modelService: modelService}
}

// List 获取系统模型列表
func (ctrl *Controller) List(ctx *gin.Context) {
	output, err := ctrl.modelService.List(ctx.Request.Context())
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, output)
}

// Get 获取单个系统模型
func (ctrl *Controller) Get(ctx *gin.Context) {
	id := ctx.Param("id")
	if id == "" {
		response.BadRequest(ctx, "缺少模型 ID")
		return
	}

	output, err := ctrl.modelService.GetByID(ctx.Request.Context(), id)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, output)
}

// Create 创建系统模型
func (ctrl *Controller) Create(ctx *gin.Context) {
	var req requestdto.CreateModelRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.BadRequest(ctx, "请求参数错误")
		return
	}

	info, err := ctrl.modelService.Create(ctx.Request.Context(), req)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, info)
}

// Update 更新系统模型
func (ctrl *Controller) Update(ctx *gin.Context) {
	id := ctx.Param("id")
	if id == "" {
		response.BadRequest(ctx, "缺少模型 ID")
		return
	}

	var req requestdto.UpdateModelRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.BadRequest(ctx, "请求参数错误")
		return
	}

	if err := ctrl.modelService.Update(ctx.Request.Context(), id, req); err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, nil)
}

// Delete 删除系统模型
func (ctrl *Controller) Delete(ctx *gin.Context) {
	id := ctx.Param("id")
	if id == "" {
		response.BadRequest(ctx, "缺少模型 ID")
		return
	}

	if err := ctrl.modelService.Delete(ctx.Request.Context(), id); err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, nil)
}
