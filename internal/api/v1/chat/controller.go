package chat

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/gin-gonic/gin"

	apiv1 "solvify-agent/internal/api/v1"
	requestdto "solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/service"
	"solvify-agent/pkg/logger"
	"solvify-agent/pkg/response"
)

// Controller 处理聊天模块请求
type Controller struct {
	chatSvc service.ChatServiceInterface
}

// NewController 创建聊天控制器
func NewController(chatSvc service.ChatServiceInterface) *Controller {
	return &Controller{chatSvc: chatSvc}
}

// CreateSession 创建聊天会话
func (ctrl *Controller) CreateSession(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		response.BadRequest(c, "用户身份无效")
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

// GetSession 获取会话详情
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

// ListSessions 获取会话列表
func (ctrl *Controller) ListSessions(c *gin.Context) {
	userID := c.GetString("user_id")
	if userID == "" {
		response.BadRequest(c, "用户身份无效")
		return
	}

	output, err := ctrl.chatSvc.ListSessions(c.Request.Context(), userID)
	if err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, gin.H{"sessions": output})
}

// UpdateSession 更新会话标题
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

// DeleteSession 删除会话
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

// SendMessage 发送消息（SSE 流式响应）
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
	fmt.Println(input)

	eventCh, err := ctrl.chatSvc.SendMessage(c.Request.Context(), userID, sessionID, input)
	if err != nil {
		response.BizError(c, err)
		return
	}

	// 设置 SSE 响应头
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	// 流式写入事件
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

// GetMessages 获取会话消息列表
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

// userAndSessionID 读取当前用户和会话 ID
func (ctrl *Controller) userAndSessionID(c *gin.Context) (string, string, bool) {
	userID := c.GetString("user_id")
	if userID == "" {
		response.BadRequest(c, "用户身份无效")
		return "", "", false
	}
	sessionID := c.Param("id")
	if !apiv1.IsUUID(sessionID) {
		response.BadRequest(c, "会话 ID 格式错误")
		return "", "", false
	}
	return userID, sessionID, true
}
