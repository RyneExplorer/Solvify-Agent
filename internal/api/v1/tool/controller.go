package tool

import (
	"github.com/gin-gonic/gin"

	"solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/service"
	"solvify-agent/pkg/response"
)

// Controller 工具配置控制器
type Controller struct {
	typeService     service.ToolTypeService
	providerService service.ToolProviderService
}

// NewController 创建工具配置控制器实例
func NewController(
	typeService service.ToolTypeService,
	providerService service.ToolProviderService,
) *Controller {
	return &Controller{
		typeService:     typeService,
		providerService: providerService,
	}
}

// ========== 管理员接口：工具类型 ==========

// ListToolTypes 获取所有工具类型
func (c *Controller) ListToolTypes(ctx *gin.Context) {
	result, err := c.typeService.List(ctx.Request.Context())
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, result)
}

// CreateToolType 创建工具类型
func (c *Controller) CreateToolType(ctx *gin.Context) {
	var req request.CreateToolTypeRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.BadRequest(ctx, "请求参数错误")
		return
	}

	result, err := c.typeService.Create(ctx.Request.Context(), req)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, result)
}

// UpdateToolType 更新工具类型
func (c *Controller) UpdateToolType(ctx *gin.Context) {
	id := ctx.Param("id")
	var req request.UpdateToolTypeRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.BadRequest(ctx, "请求参数错误")
		return
	}

	result, err := c.typeService.Update(ctx.Request.Context(), id, req)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, result)
}

// DeleteToolType 删除工具类型
func (c *Controller) DeleteToolType(ctx *gin.Context) {
	id := ctx.Param("id")
	if err := c.typeService.Delete(ctx.Request.Context(), id); err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, nil)
}

// CleanupEmptyToolTypes 清理所有无供应商的非容器工具类型（含其用户配置），返回删除数量
func (c *Controller) CleanupEmptyToolTypes(ctx *gin.Context) {
	deleted, err := c.providerService.CleanupEmptyToolTypes(ctx.Request.Context())
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, gin.H{"deleted": deleted})
}

// ========== 管理员接口：工具供应商 ==========

// ListProviderTypes 返回所有已注册的供应商类型
func (c *Controller) ListProviderTypes(ctx *gin.Context) {
	types := c.providerService.ListProviderTypes()
	response.Success(ctx, types)
}

// ListToolProviders 获取工具供应商列表
func (c *Controller) ListToolProviders(ctx *gin.Context) {
	toolTypeID := ctx.Param("id")
	result, err := c.providerService.ListByToolTypeID(ctx.Request.Context(), toolTypeID)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, result)
}

// CreateToolProvider 创建工具供应商
func (c *Controller) CreateToolProvider(ctx *gin.Context) {
	var req request.CreateToolProviderRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.BadRequest(ctx, "请求参数错误")
		return
	}

	result, err := c.providerService.Create(ctx.Request.Context(), req)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, result)
}

// UpdateToolProvider 更新工具供应商
func (c *Controller) UpdateToolProvider(ctx *gin.Context) {
	id := ctx.Param("providerId")
	var req request.UpdateToolProviderRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.BadRequest(ctx, "请求参数错误")
		return
	}

	result, err := c.providerService.Update(ctx.Request.Context(), id, req)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, result)
}

// DeleteToolProvider 删除工具供应商
func (c *Controller) DeleteToolProvider(ctx *gin.Context) {
	id := ctx.Param("providerId")
	if err := c.providerService.Delete(ctx.Request.Context(), id); err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, nil)
}

// ========== 管理员接口：MCP 服务器管理 ==========

// ListMCPServers 列出全部 MCP 供应商（含所属工具类型信息）
func (c *Controller) ListMCPServers(ctx *gin.Context) {
	result, err := c.providerService.ListMCPServers(ctx.Request.Context())
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, result)
}

// DeleteMCPServer 删除 MCP 供应商；所属工具类型若随之变空且非 mcp 容器类型，级联删除空壳类型
func (c *Controller) DeleteMCPServer(ctx *gin.Context) {
	id := ctx.Param("providerId")
	if err := c.providerService.DeleteMCPServerWithCleanup(ctx.Request.Context(), id); err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, nil)
}

// TestToolProvider 测试工具供应商连接
func (c *Controller) TestToolProvider(ctx *gin.Context) {
	var req request.TestToolRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.BadRequest(ctx, "请求参数错误")
		return
	}

	result, err := c.providerService.Test(ctx.Request.Context(), req)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, result)
}

// ProbeMCPTools 探测 MCP 供应商的工具清单（不执行工具调用）；传入 provider_id 时持久化
func (c *Controller) ProbeMCPTools(ctx *gin.Context) {
	var req request.TestToolRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.BadRequest(ctx, "请求参数错误")
		return
	}

	result, err := c.providerService.ProbeMCPTools(ctx.Request.Context(), req)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, result)
}
