package tool

import (
	"encoding/json"

	"github.com/gin-gonic/gin"

	"solvify-agent/internal/middleware"
	"solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/service"
	"solvify-agent/pkg/response"
)

// Controller 工具配置控制器
type Controller struct {
	typeService     service.ToolTypeService
	providerService service.ToolProviderService
	configService   service.UserToolConfigService
}

// NewController 创建工具配置控制器实例
func NewController(
	typeService service.ToolTypeService,
	providerService service.ToolProviderService,
	configService service.UserToolConfigService,
) *Controller {
	return &Controller{
		typeService:     typeService,
		providerService: providerService,
		configService:   configService,
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

// ========== 用户接口：工具配置 ==========

// ListToolTemplates 获取可用工具模板（用户视角）
func (c *Controller) ListToolTemplates(ctx *gin.Context) {
	toolTypes, err := c.typeService.ListEnabled(ctx.Request.Context())
	if err != nil {
		response.BizError(ctx, err)
		return
	}

	type providerBrief struct {
		ID           string          `json:"id"`
		ProviderKey  string          `json:"provider_key"`
		Name         string          `json:"name"`
		Description  string          `json:"description"`
		ProviderType string          `json:"provider_type"`
		ConfigSchema json.RawMessage `json:"config_schema"`
		InputSchema  json.RawMessage `json:"input_schema"`
		IsSystem     bool            `json:"is_system"`
		// MCP 专用：该服务器探测到的工具清单（[ {name, description} ]，可能为 null）
		MCPToolManifest json.RawMessage `json:"mcp_tool_manifest"`
	}
	type templateInfo struct {
		ID            string          `json:"id"`
		Name          string          `json:"name"`
		ToolKey       string          `json:"tool_key"`
		Description   string          `json:"description"`
		ExecutionMode string          `json:"execution_mode"`
		ProviderCount int             `json:"provider_count"`
		Providers     []providerBrief `json:"providers"`
	}

	templates := make([]templateInfo, 0, len(toolTypes.ToolTypes))
	for _, tt := range toolTypes.ToolTypes {
		providers, _ := c.providerService.ListEnabledByToolTypeID(ctx.Request.Context(), tt.ID)
		t := templateInfo{
			ID:            tt.ID,
			Name:          tt.Name,
			ToolKey:       tt.ToolKey,
			Description:   tt.Description,
			ExecutionMode: tt.ExecutionMode,
			ProviderCount: tt.ProviderCount,
			Providers:     []providerBrief{},
		}
		if providers != nil {
			for _, p := range providers.Providers {
				t.Providers = append(t.Providers, providerBrief{
					ID:              p.ID,
					ProviderKey:     p.ProviderKey,
					Name:            p.Name,
					Description:     p.Description,
					ProviderType:    p.ProviderType,
					ConfigSchema:    p.ConfigSchema,
					InputSchema:     p.InputSchema,
					IsSystem:        p.IsSystem,
					MCPToolManifest: p.MCPToolManifest,
				})
			}
		}
		templates = append(templates, t)
	}

	response.Success(ctx, templates)
}

// ListUserToolConfigs 获取用户工具配置列表
func (c *Controller) ListUserToolConfigs(ctx *gin.Context) {
	userID, ok := middleware.CurrentUserID(ctx)
	if !ok {
		return
	}
	result, err := c.configService.List(ctx.Request.Context(), userID)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, result)
}

// CreateUserToolConfig 创建用户工具配置
func (c *Controller) CreateUserToolConfig(ctx *gin.Context) {
	userID, ok := middleware.CurrentUserID(ctx)
	if !ok {
		return
	}
	var req request.CreateUserToolConfigRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.BadRequest(ctx, "请求参数错误")
		return
	}

	result, err := c.configService.Create(ctx.Request.Context(), userID, req)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, result)
}

// UpdateUserToolConfig 更新用户工具配置
func (c *Controller) UpdateUserToolConfig(ctx *gin.Context) {
	userID, ok := middleware.CurrentUserID(ctx)
	if !ok {
		return
	}
	id := ctx.Param("id")
	var req request.UpdateUserToolConfigRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.BadRequest(ctx, "请求参数错误")
		return
	}

	result, err := c.configService.Update(ctx.Request.Context(), userID, id, req)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, result)
}

// DeleteUserToolConfig 删除用户工具配置
func (c *Controller) DeleteUserToolConfig(ctx *gin.Context) {
	userID, ok := middleware.CurrentUserID(ctx)
	if !ok {
		return
	}
	id := ctx.Param("id")
	if err := c.configService.Delete(ctx.Request.Context(), userID, id); err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, nil)
}

// TestUserToolConfig 测试用户工具配置连接
func (c *Controller) TestUserToolConfig(ctx *gin.Context) {
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
