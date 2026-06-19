package dingtalk

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	apiv1 "solvify-agent/internal/api/v1"
	requestdto "solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/service"
	"solvify-agent/pkg/response"
)

// Controller 处理钉钉绑定模块请求
type Controller struct {
	dingtalkSvc service.DingTalkServiceInterface
}

// NewController 创建钉钉绑定控制器
func NewController(dingtalkSvc service.DingTalkServiceInterface) *Controller {
	return &Controller{dingtalkSvc: dingtalkSvc}
}

// OAuthConfig 获取前端内嵌二维码登录参数
func (ctrl *Controller) OAuthConfig(c *gin.Context) {
	userID, ok := apiv1.CurrentUserID(c)
	if !ok {
		return
	}
	output, err := ctrl.dingtalkSvc.OAuthConfig(c.Request.Context(), userID)
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, output)
}

// ExchangeAuthCode 兑换钉钉扫码授权码并保存绑定
func (ctrl *Controller) ExchangeAuthCode(c *gin.Context) {
	userID, ok := apiv1.CurrentUserID(c)
	if !ok {
		return
	}
	var input requestdto.DingTalkAuthCodeExchangeRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "请求体格式错误")
		return
	}
	output, err := ctrl.dingtalkSvc.ExchangeAuthCode(c.Request.Context(), userID, input)
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, output)
}

// Binding 查询当前用户钉钉绑定状态
func (ctrl *Controller) Binding(c *gin.Context) {
	userID, ok := apiv1.CurrentUserID(c)
	if !ok {
		return
	}
	output, err := ctrl.dingtalkSvc.Binding(c.Request.Context(), userID)
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, output)
}

// DeleteBinding 删除当前用户钉钉绑定
func (ctrl *Controller) DeleteBinding(c *gin.Context) {
	userID, ok := apiv1.CurrentUserID(c)
	if !ok {
		return
	}
	if err := ctrl.dingtalkSvc.DeleteBinding(c.Request.Context(), userID); err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

// ListWorkspaces 查询钉钉知识库列表
func (ctrl *Controller) ListWorkspaces(c *gin.Context) {
	userID, ok := apiv1.CurrentUserID(c)
	if !ok {
		return
	}
	output, err := ctrl.dingtalkSvc.ListWorkspaces(c.Request.Context(), userID, strings.TrimSpace(c.Query("next_token")), queryInt(c, "max_results", 30))
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, output)
}

// ListNodes 查询钉钉知识库节点列表
func (ctrl *Controller) ListNodes(c *gin.Context) {
	userID, ok := apiv1.CurrentUserID(c)
	if !ok {
		return
	}
	workspaceID := strings.TrimSpace(c.Param("workspace_id"))
	if workspaceID == "" {
		response.BadRequest(c, "知识库 ID 不能为空")
		return
	}
	output, err := ctrl.dingtalkSvc.ListNodes(
		c.Request.Context(),
		userID,
		workspaceID,
		strings.TrimSpace(c.Query("parent_node_id")),
		strings.TrimSpace(c.Query("next_token")),
		queryInt(c, "max_results", 50),
	)
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, output)
}

// queryInt 读取正整数查询参数
func queryInt(c *gin.Context, key string, defaultValue int) int {
	value := strings.TrimSpace(c.Query(key))
	if value == "" {
		return defaultValue
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return defaultValue
	}
	return parsed
}
