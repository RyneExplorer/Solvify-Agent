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
	Email    string `form:"email"`
	Status   *int   `form:"status"`
	Role     *int   `form:"role"`
}

// AdminCreateUserRequest 管理员创建用户请求
type AdminCreateUserRequest struct {
	Username string `json:"username" binding:"required,min=3,max=50"`
	Email    string `json:"email" binding:"required,email,max=100"`
	Password string `json:"password" binding:"required,min=6,max=50"`
	Status   int    `json:"status" binding:"oneof=1 2 3 4"`
	Role     int    `json:"role" binding:"oneof=1 2"`
}

// AdminUpdateUserRequest 管理员更新用户请求
type AdminUpdateUserRequest struct {
	Username string `json:"username" binding:"omitempty,min=3,max=50"`
	Email    string `json:"email" binding:"omitempty,email,max=100"`
	Status   *int   `json:"status" binding:"omitempty,oneof=1 2 3 4"`
	Role     *int   `json:"role" binding:"omitempty,oneof=1 2"`
}

// AdminResetPasswordRequest 管理员重置密码请求
type AdminResetPasswordRequest struct {
	Password string `json:"password" binding:"required,min=6,max=50"`
}
