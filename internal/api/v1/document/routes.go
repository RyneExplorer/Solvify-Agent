package document

import "github.com/gin-gonic/gin"

// RegisterKnowledgeBaseRoutes 注册知识库下文档路由
func (ctrl *Controller) RegisterKnowledgeBaseRoutes(router *gin.RouterGroup) {
	group := router.Group("/knowledge-bases/:kb_id/documents")
	group.POST("", ctrl.Upload)
	group.GET("", ctrl.List)
}

// RegisterDocumentRoutes 注册文档独立路由
func (ctrl *Controller) RegisterDocumentRoutes(router *gin.RouterGroup) {
	group := router.Group("/documents")
	group.GET("/:id", ctrl.Detail)
	group.DELETE("/:id", ctrl.Delete)
}
