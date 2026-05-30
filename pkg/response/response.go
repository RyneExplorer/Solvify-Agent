package response

import (
	stderrors "errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"math"
	apperrors "solvify-agent/pkg/errors"
)

// Response 描述统一 API 响应结构
type Response struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// Success 输出成功响应
func Success(ctx *gin.Context, data any) {
	ctx.JSON(http.StatusOK, Response{
		Code:    apperrors.CodeSuccess,
		Message: apperrors.GetMessage(apperrors.CodeSuccess),
		Data:    data,
	})
}

// SuccessWithMessage 输出自定义成功消息
func SuccessWithMessage(ctx *gin.Context, message string, data any) {
	ctx.JSON(http.StatusOK, Response{
		Code:    apperrors.CodeSuccess,
		Message: message,
		Data:    data,
	})
}

// Error 输出指定错误码和错误消息
func Error(ctx *gin.Context, code int, message string) {
	ctx.JSON(http.StatusOK, Response{
		Code:    code,
		Message: message,
	})
}

// BizError 输出业务错误响应
func BizError(ctx *gin.Context, err error) {
	var bizErr *apperrors.BizError
	if stderrors.As(err, &bizErr) {
		Error(ctx, bizErr.Code, bizErr.Message)
		return
	}

	Error(ctx, apperrors.CodeInternalError, apperrors.GetMessage(apperrors.CodeInternalError))
}

// BadRequest 输出参数错误响应
func BadRequest(ctx *gin.Context, message string) {
	Error(ctx, apperrors.CodeBadRequest, message)
}

// InternalError 输出服务内部错误响应
func InternalError(ctx *gin.Context, message string) {
	Error(ctx, apperrors.CodeInternalError, message)
}

// PageRequest 分页请求参数
type PageRequest struct {
	Page int `form:"page" json:"page" binding:"required,min=1"`         // 页码，从1开始
	Size int `form:"size" json:"size" binding:"required,min=1,max=100"` // 每页大小，最大100
}

// PageResponse 分页响应结构
type PageResponse struct {
	List     interface{} `json:"list"`     // 数据列表
	Total    int64       `json:"total"`    // 总记录数
	Page     int         `json:"page"`     // 当前页码
	PageSize int         `json:"pageSize"` // 每页大小
	Pages    int         `json:"pages"`    // 总页数
}

// NewPageResponse 创建分页响应
func NewPageResponse(list interface{}, total int64, page, pageSize int) *PageResponse {
	pages := int(math.Ceil(float64(total) / float64(pageSize)))
	return &PageResponse{
		List:     list,
		Total:    total,
		Page:     page,
		PageSize: pageSize,
		Pages:    pages,
	}
}
