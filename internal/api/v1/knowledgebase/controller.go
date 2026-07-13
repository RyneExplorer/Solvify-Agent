package knowledgebase

import (
	"github.com/gin-gonic/gin"

	"solvify-agent/internal/middleware"
	requestdto "solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/service"
	"solvify-agent/pkg/response"
)

// Controller 处理知识库模块请求
type Controller struct {
	knowledgeBaseSvc service.KnowledgeBaseServiceInterface
}

// NewController 创建知识库控制器
func NewController(knowledgeBaseSvc service.KnowledgeBaseServiceInterface) *Controller {
	return &Controller{knowledgeBaseSvc: knowledgeBaseSvc}
}

// Create 创建本地知识库
func (ctrl *Controller) Create(c *gin.Context) {
	userID, ok := middleware.CurrentUserID(c)
	if !ok {
		return
	}

	var input requestdto.CreateKnowledgeBaseRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "请求体格式错误")
		return
	}

	output, err := ctrl.knowledgeBaseSvc.Create(c.Request.Context(), userID, input)
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, output)
}

// List 查询当前用户知识库列表
func (ctrl *Controller) List(c *gin.Context) {
	userID, ok := middleware.CurrentUserID(c)
	if !ok {
		return
	}

	output, err := ctrl.knowledgeBaseSvc.List(c.Request.Context(), userID)
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, output)
}

// Detail 查询知识库详情
func (ctrl *Controller) Detail(c *gin.Context) {
	userID, kbID, ok := ctrl.userAndKnowledgeBaseID(c)
	if !ok {
		return
	}

	output, err := ctrl.knowledgeBaseSvc.Detail(c.Request.Context(), userID, kbID)
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, output)
}

// Update 更新知识库基础信息
func (ctrl *Controller) Update(c *gin.Context) {
	userID, kbID, ok := ctrl.userAndKnowledgeBaseID(c)
	if !ok {
		return
	}

	var input requestdto.UpdateKnowledgeBaseRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "请求体格式错误")
		return
	}

	output, err := ctrl.knowledgeBaseSvc.Update(c.Request.Context(), userID, kbID, input)
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, output)
}

// Delete 软删除知识库
func (ctrl *Controller) Delete(c *gin.Context) {
	userID, kbID, ok := ctrl.userAndKnowledgeBaseID(c)
	if !ok {
		return
	}

	if err := ctrl.knowledgeBaseSvc.Delete(c.Request.Context(), userID, kbID); err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

// Stats 查询知识库统计
func (ctrl *Controller) Stats(c *gin.Context) {
	userID, kbID, ok := ctrl.userAndKnowledgeBaseID(c)
	if !ok {
		return
	}

	output, err := ctrl.knowledgeBaseSvc.Stats(c.Request.Context(), userID, kbID)
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, output)
}

// userAndKnowledgeBaseID 读取当前用户和知识库 ID
func (ctrl *Controller) userAndKnowledgeBaseID(c *gin.Context) (string, string, bool) {
	userID, ok := middleware.CurrentUserID(c)
	if !ok {
		return "", "", false
	}

	kbID := c.Param("id")
	if !middleware.IsUUID(kbID) {
		response.BadRequest(c, "知识库 ID 格式错误")
		return "", "", false
	}
	return userID, kbID, true
}
