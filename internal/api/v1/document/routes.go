package document

import "github.com/gin-gonic/gin"

// RegisterKnowledgeBaseRoutes 注册知识库下文档路由
func (ctrl *Controller) RegisterKnowledgeBaseRoutes(router *gin.RouterGroup) {
	group := router.Group("/knowledge-bases/:id/documents")
	group.POST("", ctrl.Upload)
	group.POST("/notes", ctrl.CreateNote)
	group.GET("", ctrl.List)
}

// RegisterDocumentRoutes 注册文档独立路由
func (ctrl *Controller) RegisterDocumentRoutes(router *gin.RouterGroup) {
	group := router.Group("/documents")
	group.GET("/:id", ctrl.Detail)
	group.GET("/:id/preview", ctrl.Preview)
	group.DELETE("/:id", ctrl.Delete)
	group.POST("/:id/process", ctrl.Process)
	group.GET("/:id/versions", ctrl.Versions)
	group.GET("/:id/versions/:version_id", ctrl.VersionDetail)
	group.POST("/:id/versions", ctrl.CreateVersion)
	group.POST("/:id/reindex", ctrl.Reindex)
	group.GET("/:id/jobs", ctrl.Jobs)
}

// RegisterDocumentJobRoutes 注册文档处理任务独立路由
func (ctrl *Controller) RegisterDocumentJobRoutes(router *gin.RouterGroup) {
	group := router.Group("/document-jobs")
	group.GET("/:id", ctrl.JobDetail)
}

// RegisterChunkRoutes 注册 chunk 独立路由
func (ctrl *Controller) RegisterChunkRoutes(router *gin.RouterGroup) {
	group := router.Group("/chunks")
	group.GET("/:id", ctrl.ChunkDetail)
}
