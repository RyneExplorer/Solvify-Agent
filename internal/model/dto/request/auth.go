package request

// RefreshTokenRequest 刷新 Token 请求
type RefreshTokenRequest struct {
	Token string `json:"token" binding:"required"`
}

// SendEmailCodeRequest 发送邮箱验证码请求
type SendEmailCodeRequest struct {
	Email string `json:"email" binding:"required,email"`
}

// ResetPasswordRequest 重置密码请求（使用邮箱验证码）
type ResetPasswordRequest struct {
	Email           string `json:"email" binding:"required,email"`
	EmailCaptcha    string `json:"captcha" binding:"required,len=6"`
	NewPassword     string `json:"new_password" binding:"required,min=6,max=50"`
	ConfirmPassword string `json:"confirm_password" binding:"required,min=6,max=50"`
}
