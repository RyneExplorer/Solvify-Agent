package request

// RegisterRequest 用户注册请求
type RegisterRequest struct {
	Username        string `json:"username" binding:"required,min=3,max=50"`
	Password        string `json:"password" binding:"required,min=6,max=300"`
	ConfirmPassword string `json:"confirm_password" binding:"required"`
	Email           string `json:"email" binding:"required,email"`
	EmailCaptcha    string `json:"captcha" binding:"required,len=6"`
}

// LoginRequest 用户登录请求
type LoginRequest struct {
	Username  string `json:"username" binding:"required"`
	Password  string `json:"password" binding:"required"`
	CaptchaID string `json:"captcha_id" binding:"required"`
	Captcha   string `json:"captcha" binding:"required,len=4"`
}

// UpdateUserRequest 更新用户信息请求
type UpdateUserRequest struct {
	Avatar string `json:"avatar" binding:"omitempty,url,max=255"`
	Email  string `json:"email" binding:"omitempty,email,max=100"`
}

// ChangePasswordRequest 修改密码请求
type ChangePasswordRequest struct {
	OldPassword string `json:"old_password" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=6,max=50"`
}

// AdminUserListRequest 管理员用户列表请求
type AdminUserListRequest struct {
	Page     int    `form:"page" binding:"required,min=1"`
	PageSize int    `form:"pageSize" binding:"required,min=1,max=100"`
	Username string `form:"username"`
	Nickname string `form:"nickname"`
	Status   *int   `form:"status"`
}
