package mcp

import (
	"github.com/gin-gonic/gin"

	"solvify-agent/internal/middleware"
	"solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/service"
	"solvify-agent/pkg/response"
)

type Controller struct{ service service.MCPService }

func NewController(s service.MCPService) *Controller { return &Controller{service: s} }
func (c *Controller) Templates(ctx *gin.Context) {
	v, e := c.service.Templates(ctx.Request.Context())
	if e != nil {
		response.BizError(ctx, e)
		return
	}
	response.Success(ctx, v)
}
func (c *Controller) List(ctx *gin.Context) {
	uid, ok := middleware.CurrentUserID(ctx)
	if !ok {
		return
	}
	v, e := c.service.List(ctx.Request.Context(), uid)
	if e != nil {
		response.BizError(ctx, e)
		return
	}
	response.Success(ctx, v)
}
func (c *Controller) Get(ctx *gin.Context) {
	uid, ok := middleware.CurrentUserID(ctx)
	if !ok {
		return
	}
	v, e := c.service.Get(ctx.Request.Context(), uid, ctx.Param("id"))
	if e != nil {
		response.BizError(ctx, e)
		return
	}
	response.Success(ctx, v)
}
func (c *Controller) Create(ctx *gin.Context) {
	uid, ok := middleware.CurrentUserID(ctx)
	if !ok {
		return
	}
	var req request.CreateMCPConnectionRequest
	if ctx.ShouldBindJSON(&req) != nil {
		response.BadRequest(ctx, "MCP参数格式错误")
		return
	}
	v, e := c.service.Create(ctx.Request.Context(), uid, req)
	if e != nil {
		response.BizError(ctx, e)
		return
	}
	response.Success(ctx, v)
}
func (c *Controller) Update(ctx *gin.Context) {
	uid, ok := middleware.CurrentUserID(ctx)
	if !ok {
		return
	}
	var req request.UpdateMCPConnectionRequest
	if ctx.ShouldBindJSON(&req) != nil {
		response.BadRequest(ctx, "MCP参数格式错误")
		return
	}
	v, e := c.service.Update(ctx.Request.Context(), uid, ctx.Param("id"), req)
	if e != nil {
		response.BizError(ctx, e)
		return
	}
	response.Success(ctx, v)
}
func (c *Controller) Delete(ctx *gin.Context) {
	uid, ok := middleware.CurrentUserID(ctx)
	if !ok {
		return
	}
	if e := c.service.Delete(ctx.Request.Context(), uid, ctx.Param("id")); e != nil {
		response.BizError(ctx, e)
		return
	}
	response.Success(ctx, nil)
}
func (c *Controller) Probe(ctx *gin.Context) {
	uid, ok := middleware.CurrentUserID(ctx)
	if !ok {
		return
	}
	var req request.ProbeMCPConnectionRequest
	if ctx.ShouldBindJSON(&req) != nil {
		response.BadRequest(ctx, "MCP参数格式错误")
		return
	}
	v, e := c.service.Probe(ctx.Request.Context(), uid, req)
	if e != nil {
		response.BizError(ctx, e)
		return
	}
	response.Success(ctx, v)
}

func (c *Controller) Tools(ctx *gin.Context) {
	uid, ok := middleware.CurrentUserID(ctx)
	if !ok {
		return
	}
	v, err := c.service.Tools(ctx.Request.Context(), uid, ctx.Param("id"))
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, v)
}

func (c *Controller) UpdateTool(ctx *gin.Context) {
	uid, ok := middleware.CurrentUserID(ctx)
	if !ok {
		return
	}
	var req request.UpdateMCPToolRequest
	if ctx.ShouldBindJSON(&req) != nil {
		response.BadRequest(ctx, "MCP工具开关参数错误")
		return
	}
	v, err := c.service.UpdateTool(ctx.Request.Context(), uid, ctx.Param("id"), req)
	if err != nil {
		response.BizError(ctx, err)
		return
	}
	response.Success(ctx, v)
}
