package request

// AdminSessionListRequest 管理员会话列表请求
type AdminSessionListRequest struct {
	Page     int    `form:"page" binding:"required,min=1"`
	PageSize int    `form:"pageSize" binding:"required,min=1,max=100"`
	Keyword  string `form:"keyword"`
	Status   string `form:"status"`
}
