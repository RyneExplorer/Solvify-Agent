package sync

import (
	"github.com/gin-gonic/gin"

	apiv1 "solvify-agent/internal/api/v1"
	requestdto "solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/service"
	"solvify-agent/pkg/response"
)

// Controller 处理同步模块请求
type Controller struct {
	syncSvc service.SyncServiceInterface
}

// NewController 创建同步控制器
func NewController(syncSvc service.SyncServiceInterface) *Controller {
	return &Controller{syncSvc: syncSvc}
}

// CreateSource 创建同步源
func (ctrl *Controller) CreateSource(c *gin.Context) {
	userID, ok := apiv1.CurrentUserID(c)
	if !ok {
		return
	}
	var input requestdto.CreateSyncSourceRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "请求体格式错误")
		return
	}
	output, err := ctrl.syncSvc.CreateSource(c.Request.Context(), userID, input)
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, output)
}

// ListSources 查询同步源列表
func (ctrl *Controller) ListSources(c *gin.Context) {
	userID, ok := apiv1.CurrentUserID(c)
	if !ok {
		return
	}
	output, err := ctrl.syncSvc.ListSources(c.Request.Context(), userID)
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, output)
}

// SourceDetail 查询同步源详情
func (ctrl *Controller) SourceDetail(c *gin.Context) {
	userID, sourceID, ok := ctrl.userAndSourceID(c)
	if !ok {
		return
	}
	output, err := ctrl.syncSvc.SourceDetail(c.Request.Context(), userID, sourceID)
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, output)
}

// UpdateSource 更新同步源
func (ctrl *Controller) UpdateSource(c *gin.Context) {
	userID, sourceID, ok := ctrl.userAndSourceID(c)
	if !ok {
		return
	}
	var input requestdto.UpdateSyncSourceRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "请求体格式错误")
		return
	}
	output, err := ctrl.syncSvc.UpdateSource(c.Request.Context(), userID, sourceID, input)
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, output)
}

// DeleteSource 软删除同步源
func (ctrl *Controller) DeleteSource(c *gin.Context) {
	userID, sourceID, ok := ctrl.userAndSourceID(c)
	if !ok {
		return
	}
	if err := ctrl.syncSvc.DeleteSource(c.Request.Context(), userID, sourceID); err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

// CreateJob 创建同步任务
func (ctrl *Controller) CreateJob(c *gin.Context) {
	userID, sourceID, ok := ctrl.userAndSourceID(c)
	if !ok {
		return
	}
	output, err := ctrl.syncSvc.CreateJob(c.Request.Context(), userID, sourceID)
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, output)
}

// ListJobs 查询同步源任务列表
func (ctrl *Controller) ListJobs(c *gin.Context) {
	userID, sourceID, ok := ctrl.userAndSourceID(c)
	if !ok {
		return
	}
	output, err := ctrl.syncSvc.ListJobs(c.Request.Context(), userID, sourceID)
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, output)
}

// JobDetail 查询同步任务详情
func (ctrl *Controller) JobDetail(c *gin.Context) {
	userID, ok := apiv1.CurrentUserID(c)
	if !ok {
		return
	}
	jobID := c.Param("id")
	if !apiv1.IsUUID(jobID) {
		response.BadRequest(c, "同步任务 ID 格式错误")
		return
	}
	output, err := ctrl.syncSvc.JobDetail(c.Request.Context(), userID, jobID)
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, output)
}

// userAndSourceID 读取当前用户和同步源 ID
func (ctrl *Controller) userAndSourceID(c *gin.Context) (string, string, bool) {
	userID, ok := apiv1.CurrentUserID(c)
	if !ok {
		return "", "", false
	}
	sourceID := c.Param("id")
	if !apiv1.IsUUID(sourceID) {
		response.BadRequest(c, "同步源 ID 格式错误")
		return "", "", false
	}
	return userID, sourceID, true
}
