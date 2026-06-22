package storage

import (
	"github.com/gin-gonic/gin"

	"solvify-agent/internal/middleware"
	"solvify-agent/internal/service"
	"solvify-agent/pkg/response"
)

// Controller 处理存储配额模块请求
type Controller struct {
	storageSvc service.StorageServiceInterface
}

// NewController 创建存储配额控制器
func NewController(storageSvc service.StorageServiceInterface) *Controller {
	return &Controller{storageSvc: storageSvc}
}

// Quota 查询当前用户存储配额
func (ctrl *Controller) Quota(c *gin.Context) {
	userID, ok := middleware.CurrentUserID(c)
	if !ok {
		return
	}

	output, err := ctrl.storageSvc.Quota(c.Request.Context(), userID)
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, output)
}
