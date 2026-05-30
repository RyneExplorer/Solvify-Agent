package qa

import (
	"github.com/gin-gonic/gin"

	requestdto "solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/service"
	"solvify-agent/pkg/response"
)

// Controller 处理问答模块请求
type Controller struct {
	chatService *service.ChatService
}

// NewController 创建问答控制器
func NewController(chatService *service.ChatService) *Controller {
	return &Controller{chatService: chatService}
}

// Ask 处理知识问答请求
func (ctrl *Controller) Ask(ctx *gin.Context) {
	var input requestdto.AskRequest
	if err := ctx.ShouldBindJSON(&input); err != nil {
		response.BadRequest(ctx, "请求体格式错误")
		return
	}

	output, err := ctrl.chatService.Ask(ctx.Request.Context(), input)
	if err != nil {
		response.BizError(ctx, err)
		return
	}

	response.Success(ctx, output)
}
