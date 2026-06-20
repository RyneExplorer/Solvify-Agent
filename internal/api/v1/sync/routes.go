package sync

import "github.com/gin-gonic/gin"

// RegisterRoutes 注册同步模块路由
func (ctrl *Controller) RegisterRoutes(router *gin.RouterGroup) {
	sources := router.Group("/sync-sources")
	// 创建同步源
	sources.POST("", ctrl.CreateSource)
	// 查询同步源列表
	sources.GET("", ctrl.ListSources)
	// 通过id查询同步源详情
	sources.GET("/:id", ctrl.SourceDetail)
	// 通过id更新同步源
	sources.PUT("/:id", ctrl.UpdateSource)
	// 通过id软删除同步源
	sources.DELETE("/:id", ctrl.DeleteSource)
	// 创建同步任务
	sources.POST("/:id/jobs", ctrl.CreateJob)
	// 查询同步源任务列表
	sources.GET("/:id/jobs", ctrl.ListJobs)

	jobs := router.Group("/sync-jobs")
	// 根据id查询同步任务详情
	jobs.GET("/:id", ctrl.JobDetail)
}
