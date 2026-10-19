package mcp

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

func (c *Controller) RegisterRoutes(router *gin.RouterGroup) {
	group := router.Group("/user/mcp-connections")
	group.Use(func(ctx *gin.Context) {
		ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, 256<<10)
		ctx.Next()
	})
	group.GET("/templates", c.Templates)
	group.POST("/probe", c.Probe)
	group.GET("", c.List)
	group.POST("", c.Create)
	group.GET("/:id", c.Get)
	group.GET("/:id/tools", c.Tools)
	group.PUT("/:id/tools", c.UpdateTool)
	group.PUT("/:id", c.Update)
	group.DELETE("/:id", c.Delete)
}
