package chat

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"github.com/gin-gonic/gin"

	"solvify-agent/internal/middleware"
	requestdto "solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/service"
	"solvify-agent/pkg/logger"
	"solvify-agent/pkg/response"
)

type Controller struct {
	chatSvc             service.ChatServiceInterface
	adminSessionService service.AdminSessionServiceInterface
}

func NewController(chatSvc service.ChatServiceInterface, adminSessionService service.AdminSessionServiceInterface) *Controller {
	return &Controller{
		chatSvc:             chatSvc,
		adminSessionService: adminSessionService,
	}
}

func (ctrl *Controller) CreateSession(c *gin.Context) {
	userID, ok := middleware.CurrentUserID(c)
	if !ok {
		return
	}

	var input requestdto.CreateSessionRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "请求体格式错误")
		return
	}

	output, err := ctrl.chatSvc.CreateSession(c.Request.Context(), userID, input)
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, output)
}

func (ctrl *Controller) GetSession(c *gin.Context) {
	userID, sessionID, ok := ctrl.userAndSessionID(c)
	if !ok {
		return
	}
	output, err := ctrl.chatSvc.GetSession(c.Request.Context(), userID, sessionID)
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, output)
}

func (ctrl *Controller) ListSessions(c *gin.Context) {
	userID, ok := middleware.CurrentUserID(c)
	if !ok {
		return
	}

	output, err := ctrl.chatSvc.ListSessions(c.Request.Context(), userID)
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, gin.H{"sessions": output})
}

func (ctrl *Controller) UpdateSession(c *gin.Context) {
	userID, sessionID, ok := ctrl.userAndSessionID(c)
	if !ok {
		return
	}

	var input requestdto.UpdateSessionRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "请求体格式错误")
		return
	}

	if err := ctrl.chatSvc.UpdateSessionTitle(c.Request.Context(), userID, sessionID, input); err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, nil)
}

func (ctrl *Controller) DeleteSession(c *gin.Context) {
	userID, sessionID, ok := ctrl.userAndSessionID(c)
	if !ok {
		return
	}

	if err := ctrl.chatSvc.DeleteSession(c.Request.Context(), userID, sessionID); err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, nil)
}

func (ctrl *Controller) SendMessage(c *gin.Context) {
	userID, sessionID, ok := ctrl.userAndSessionID(c)
	if !ok {
		return
	}

	var input requestdto.SendMessageRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "请求体格式错误")
		return
	}

	eventCh, err := ctrl.chatSvc.SendMessage(c.Request.Context(), userID, sessionID, input)
	if err != nil {
		response.BizError(c, err)
		return
	}

	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	c.Stream(func(w io.Writer) bool {
		event, ok := <-eventCh
		if !ok {
			return false
		}
		data, err := json.Marshal(event)
		if err != nil {
			logger.Errorf("SSE 事件序列化失败: %v", err)
			if _, writeErr := fmt.Fprintf(w, "data: {\"type\":\"error\",\"error\":\"事件序列化失败\",\"done\":true}\n\n"); writeErr != nil {
				logger.Errorf("SSE 错误事件写入失败: %v", writeErr)
			}
			return false
		}
		if _, writeErr := fmt.Fprintf(w, "data: %s\n\n", data); writeErr != nil {
			logger.Errorf("SSE 事件写入失败: %v", writeErr)
			return false
		}
		return !event.Done
	})
}

func (ctrl *Controller) GetMessages(c *gin.Context) {
	userID, sessionID, ok := ctrl.userAndSessionID(c)
	if !ok {
		return
	}
	output, err := ctrl.chatSvc.GetMessages(c.Request.Context(), userID, sessionID)
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, gin.H{"messages": output})
}

func (ctrl *Controller) SubmitFeedback(c *gin.Context) {
	userID, ok := middleware.CurrentUserID(c)
	if !ok {
		return
	}
	messageID := c.Param("message_id")
	if messageID == "" {
		response.BadRequest(c, "message_id 不能为空")
		return
	}
	var req service.FeedbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求体格式错误")
		return
	}
	if err := ctrl.chatSvc.SubmitFeedback(c.Request.Context(), userID, messageID, req); err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, gin.H{"ok": true})
}

func (ctrl *Controller) ListFeedbacks(c *gin.Context) {
	userID, ok := middleware.CurrentUserID(c)
	if !ok {
		return
	}
	offset, limit := listParams(c, 0, 20)
	out, err := ctrl.chatSvc.ListFeedbacks(c.Request.Context(), userID, offset, limit)
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, out)
}

func (ctrl *Controller) GetTrace(c *gin.Context) {
	userID, ok := middleware.CurrentUserID(c)
	if !ok {
		return
	}
	isAdmin := middleware.IsCurrentUserAdmin(c)
	traceID := c.Param("trace_id")
	if traceID == "" {
		response.BadRequest(c, "trace_id 不能为空")
		return
	}
	trace, err := ctrl.chatSvc.GetTrace(c.Request.Context(), userID, traceID, isAdmin)
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, trace)
}

func (ctrl *Controller) ListSessionTraces(c *gin.Context) {
	userID, sessionID, ok := ctrl.userAndSessionID(c)
	if !ok {
		return
	}
	isAdmin := middleware.IsCurrentUserAdmin(c)
	offset, limit := listParams(c, 0, 50)
	out, err := ctrl.chatSvc.ListSessionTraces(c.Request.Context(), userID, sessionID, isAdmin, offset, limit)
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, out)
}

func (ctrl *Controller) AdminListTraces(c *gin.Context) {
	var (
		sessionID = c.Query("session_id")
		status    = c.Query("status")
	)
	rating, _ := strconv.Atoi(c.Query("rating"))
	offset, limit := listParams(c, 0, 50)
	out, err := ctrl.chatSvc.AdminListTraces(c.Request.Context(), sessionID, rating, status, offset, limit)
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, out)
}

func (ctrl *Controller) AdminGetTrace(c *gin.Context) {
	traceID := c.Param("trace_id")
	if traceID == "" {
		response.BadRequest(c, "trace_id 不能为空")
		return
	}
	trace, err := ctrl.chatSvc.GetTrace(c.Request.Context(), "", traceID, true)
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, trace)
}

func (ctrl *Controller) MetricsSnapshot(c *gin.Context) {
	snap, err := ctrl.chatSvc.GetMetricsSnapshot()
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, snap)
}

func (ctrl *Controller) AdminListSessions(c *gin.Context) {
	var req requestdto.AdminSessionListRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	result, err := ctrl.adminSessionService.List(c.Request.Context(), &req)
	if err != nil {
		response.BizError(c, err)
		return
	}

	response.Success(c, result)
}

func (ctrl *Controller) AdminDeleteSession(c *gin.Context) {
	sessionID := c.Param("id")
	if !middleware.IsUUID(sessionID) {
		response.BadRequest(c, "会话 ID 格式错误")
		return
	}

	if err := ctrl.adminSessionService.Delete(c.Request.Context(), sessionID); err != nil {
		response.BizError(c, err)
		return
	}

	response.Success(c, nil)
}

func (ctrl *Controller) AdminCleanupSessions(c *gin.Context) {
	var req struct {
		RetentionDays int `json:"retention_days"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		req.RetentionDays = 90
	}

	deleted, err := ctrl.adminSessionService.CleanupExpired(c.Request.Context(), req.RetentionDays)
	if err != nil {
		response.BizError(c, err)
		return
	}

	response.Success(c, gin.H{"deleted": deleted})
}

func (ctrl *Controller) userAndSessionID(c *gin.Context) (string, string, bool) {
	userID, ok := middleware.CurrentUserID(c)
	if !ok {
		return "", "", false
	}
	sessionID := c.Param("id")
	if !middleware.IsUUID(sessionID) {
		response.BadRequest(c, "会话 ID 格式错误")
		return "", "", false
	}
	return userID, sessionID, true
}

func listParams(c *gin.Context, defaultOffset, defaultLimit int) (int, int) {
	offset := defaultOffset
	limit := defaultLimit
	if raw := c.Query("offset"); raw != "" {
		if v, e := strconv.Atoi(raw); e == nil && v >= 0 {
			offset = v
		}
	}
	if raw := c.Query("limit"); raw != "" {
		if v, e := strconv.Atoi(raw); e == nil && v > 0 {
			limit = v
		}
	}
	return offset, limit
}
