package sync

import "github.com/gin-gonic/gin"

// RegisterRoutes 注册同步模块路由
func (ctrl *Controller) RegisterRoutes(router *gin.RouterGroup) {
	sources := router.Group("/sync-sources")
	sources.POST("", ctrl.CreateSource)
	sources.GET("", ctrl.ListSources)
	sources.GET("/:id", ctrl.SourceDetail)
	sources.PUT("/:id", ctrl.UpdateSource)
	sources.DELETE("/:id", ctrl.DeleteSource)
	sources.POST("/:id/jobs", ctrl.CreateJob)
	sources.GET("/:id/jobs", ctrl.ListJobs)

	jobs := router.Group("/sync-jobs")
	jobs.GET("/:id", ctrl.JobDetail)
}
