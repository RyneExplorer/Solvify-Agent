package mcp

import (
	"github.com/gin-gonic/gin"
	"net/http"
	"solvify-agent/internal/middleware"
	"solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/service"
	"solvify-agent/pkg/response"
)

type TemplateController struct{ service service.MCPTemplateService }

func NewTemplateController(s service.MCPTemplateService) *TemplateController {
	return &TemplateController{s}
}
func (c *TemplateController) RegisterRoutes(router *gin.RouterGroup) {
	g := router.Group("/admin/mcp-templates")
	g.Use(middleware.RequireAdmin(), func(ctx *gin.Context) {
		ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, 256<<10)
		ctx.Next()
	})
	g.GET("", c.List)
	g.POST("", c.Create)
	g.POST("/probe", c.Probe)
	g.GET("/:id", c.Get)
	g.PUT("/:id", c.Update)
	g.DELETE("/:id", c.Delete)
}
func (c *TemplateController) List(ctx *gin.Context) {
	v, e := c.service.List(ctx.Request.Context())
	if e != nil {
		response.BizError(ctx, e)
		return
	}
	response.Success(ctx, v)
}
func (c *TemplateController) Get(ctx *gin.Context) {
	v, e := c.service.GetByID(ctx.Request.Context(), ctx.Param("id"))
	if e != nil {
		response.BizError(ctx, e)
		return
	}
	response.Success(ctx, v)
}
func (c *TemplateController) Create(ctx *gin.Context) {
	var req request.CreateMCPTemplateRequest
	if ctx.ShouldBindJSON(&req) != nil {
		response.BadRequest(ctx, "MCP预置参数格式错误")
		return
	}
	v, e := c.service.Create(ctx.Request.Context(), req)
	if e != nil {
		response.BizError(ctx, e)
		return
	}
	response.Success(ctx, v)
}
func (c *TemplateController) Update(ctx *gin.Context) {
	var req request.UpdateMCPTemplateRequest
	if ctx.ShouldBindJSON(&req) != nil {
		response.BadRequest(ctx, "MCP预置参数格式错误")
		return
	}
	v, e := c.service.Update(ctx.Request.Context(), ctx.Param("id"), req)
	if e != nil {
		response.BizError(ctx, e)
		return
	}
	response.Success(ctx, v)
}
func (c *TemplateController) Delete(ctx *gin.Context) {
	if e := c.service.Delete(ctx.Request.Context(), ctx.Param("id")); e != nil {
		response.BizError(ctx, e)
		return
	}
	response.Success(ctx, nil)
}
func (c *TemplateController) Probe(ctx *gin.Context) {
	var req request.ProbeMCPTemplateRequest
	if ctx.ShouldBindJSON(&req) != nil {
		response.BadRequest(ctx, "MCP预置参数格式错误")
		return
	}
	v, e := c.service.Probe(ctx.Request.Context(), req)
	if e != nil {
		response.BizError(ctx, e)
		return
	}
	response.Success(ctx, v)
}
