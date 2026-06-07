package document

import (
	"github.com/gin-gonic/gin"

	apiv1 "solvify-agent/internal/api/v1"
	"solvify-agent/internal/service"
	"solvify-agent/pkg/response"
)

// Controller 处理文档模块请求
type Controller struct {
	documentService *service.DocumentService
}

// NewController 创建文档控制器
func NewController(documentService *service.DocumentService) *Controller {
	return &Controller{documentService: documentService}
}

// Upload 上传文档到指定知识库
func (ctrl *Controller) Upload(c *gin.Context) {
	userID, kbID, ok := ctrl.userAndKnowledgeBaseID(c)
	if !ok {
		return
	}

	fileHeader, err := c.FormFile("file")
	if err != nil {
		response.BadRequest(c, "上传文件不能为空")
		return
	}

	output, err := ctrl.documentService.Upload(c.Request.Context(), userID, kbID, fileHeader)
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, output)
}

// List 查询知识库下文档列表
func (ctrl *Controller) List(c *gin.Context) {
	userID, kbID, ok := ctrl.userAndKnowledgeBaseID(c)
	if !ok {
		return
	}

	output, err := ctrl.documentService.List(c.Request.Context(), userID, kbID)
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, output)
}

// Detail 查询文档详情
func (ctrl *Controller) Detail(c *gin.Context) {
	userID, documentID, ok := ctrl.userAndDocumentID(c)
	if !ok {
		return
	}

	output, err := ctrl.documentService.Detail(c.Request.Context(), userID, documentID)
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, output)
}

// Delete 软删除文档
func (ctrl *Controller) Delete(c *gin.Context) {
	userID, documentID, ok := ctrl.userAndDocumentID(c)
	if !ok {
		return
	}

	if err := ctrl.documentService.Delete(c.Request.Context(), userID, documentID); err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

// Process 手动触发文档处理
func (ctrl *Controller) Process(c *gin.Context) {
	userID, documentID, ok := ctrl.userAndDocumentID(c)
	if !ok {
		return
	}

	output, err := ctrl.documentService.Process(c.Request.Context(), userID, documentID)
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, output)
}

// Jobs 查询文档处理任务列表
func (ctrl *Controller) Jobs(c *gin.Context) {
	userID, documentID, ok := ctrl.userAndDocumentID(c)
	if !ok {
		return
	}

	output, err := ctrl.documentService.ListJobs(c.Request.Context(), userID, documentID)
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, output)
}

// JobDetail 查询文档处理任务详情
func (ctrl *Controller) JobDetail(c *gin.Context) {
	userID, jobID, ok := ctrl.userAndDocumentJobID(c)
	if !ok {
		return
	}

	output, err := ctrl.documentService.JobDetail(c.Request.Context(), userID, jobID)
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, output)
}

// userAndKnowledgeBaseID 读取当前用户和知识库 ID
func (ctrl *Controller) userAndKnowledgeBaseID(c *gin.Context) (string, string, bool) {
	userID, ok := apiv1.CurrentUserID(c)
	if !ok {
		return "", "", false
	}
	kbID := c.Param("id")
	if !apiv1.IsUUID(kbID) {
		response.BadRequest(c, "知识库 ID 格式错误")
		return "", "", false
	}
	return userID, kbID, true
}

// userAndDocumentID 读取当前用户和文档 ID
func (ctrl *Controller) userAndDocumentID(c *gin.Context) (string, string, bool) {
	userID, ok := apiv1.CurrentUserID(c)
	if !ok {
		return "", "", false
	}
	documentID := c.Param("id")
	if !apiv1.IsUUID(documentID) {
		response.BadRequest(c, "文档 ID 格式错误")
		return "", "", false
	}
	return userID, documentID, true
}

// userAndDocumentJobID 读取当前用户和文档处理任务 ID
func (ctrl *Controller) userAndDocumentJobID(c *gin.Context) (string, string, bool) {
	userID, ok := apiv1.CurrentUserID(c)
	if !ok {
		return "", "", false
	}
	jobID := c.Param("id")
	if !apiv1.IsUUID(jobID) {
		response.BadRequest(c, "文档处理任务 ID 格式错误")
		return "", "", false
	}
	return userID, jobID, true
}
