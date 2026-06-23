package search

import (
	"solvify-agent/internal/middleware"
	requestdto "solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/service"
	"solvify-agent/pkg/response"

	"github.com/gin-gonic/gin"
)

// Controller 搜索控制器
type Controller struct {
	searchService service.SearchServiceInterface
}

// NewController 创建搜索控制器
func NewController(searchService service.SearchServiceInterface) *Controller {
	return &Controller{searchService: searchService}
}

// Search 统一搜索入口
func (ctrl *Controller) Search(c *gin.Context) {
	userID := middleware.GetUserID(c)

	var req requestdto.SearchRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	result, err := ctrl.searchService.Search(c.Request.Context(), userID, &req)
	if err != nil {
		response.BizError(c, err)
		return
	}

	response.Success(c, result)
}
