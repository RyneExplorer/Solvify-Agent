package auth

import (
	"regexp"
	"solvify-agent/internal/middleware"
	"solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/service"
	"solvify-agent/pkg/captcha"
	"solvify-agent/pkg/logger"
	"solvify-agent/pkg/response"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Controller 认证控制器
type Controller struct {
	authService service.AuthServiceInterface
	userService service.UserServiceInterface
}

// NewController 创建认证控制器
func NewController(authService service.AuthServiceInterface, userService service.UserServiceInterface) *Controller {
	return &Controller{
		authService: authService,
		userService: userService,
	}
}

// Register 用户注册
func (ctrl *Controller) Register(c *gin.Context) {
	var req request.RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "数据参数格式错误!")
		return
	}
	if req.Password != req.ConfirmPassword {
		response.BadRequest(c, "密码不一致")
		return
	}
	// 用户名校验
	var regexpAlphaNum = regexp.MustCompile(`^[a-zA-Z0-9]+$`)
	if !regexpAlphaNum.MatchString(req.Username) {
		response.BadRequest(c, "用户名只能包含字母或数字")
		return
	}

	// 邮箱校验
	qqEmailRegex := regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9_.]{2,14})[a-zA-Z0-9]@qq\.com$`)
	if qqEmailRegex.MatchString(req.Email) == false {
		response.BadRequest(c, "目前仅支持qq邮箱")
		return
	}

	if err := ctrl.authService.Register(&req); err != nil {
		response.BizError(c, err)
		return
	}

	response.Success(c, nil)
}

// Login 用户登录
func (ctrl *Controller) Login(c *gin.Context) {
	var req request.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	var regexpAlphaNum = regexp.MustCompile(`^[a-zA-Z0-9]+$`)
	if !regexpAlphaNum.MatchString(req.Username) {
		response.BadRequest(c, "用户名只能包含字母或数字")
		return
	}

	resp, err := ctrl.authService.Login(&req)
	if err != nil {
		response.BizError(c, err)
		return
	}

	response.Success(c, resp)
}

// RefreshToken 刷新 Token
func (ctrl *Controller) RefreshToken(c *gin.Context) {
	var req request.RefreshTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}

	newToken, err := ctrl.authService.RefreshToken(req.Token)
	if err != nil {
		response.BizError(c, err)
		return
	}

	response.Success(c, map[string]string{
		"token": newToken,
	})
}

// SendEmailCode 发送邮箱验证码
func (ctrl *Controller) SendEmailCode(c *gin.Context) {
	var req request.SendEmailCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "邮箱格式错误!")
		return
	}
	if err := ctrl.authService.SendEmailCode(req.Email); err != nil {
		logger.Error("发送验证码失败:", zap.Error(err))
		response.BizError(c, err)
		return
	}
	response.Success(c, nil)
}

// Logout 用户登出
func (ctrl *Controller) Logout(c *gin.Context) {
	token := middleware.GetToken(c)
	if token == "" {
		response.Unauthorized(c, "未登录或 token 无效")
		return
	}

	if err := ctrl.authService.Logout(token); err != nil {
		response.BizError(c, err)
		return
	}

	response.Success(c, "登出成功")
}

// ResetPassword 重置密码
func (ctrl *Controller) ResetPassword(c *gin.Context) {
	var req request.ResetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "请求参数错误")
		return
	}
	if req.NewPassword != req.ConfirmPassword {
		response.BadRequest(c, "两次输入密码不一致")
		return
	}
	if err := ctrl.authService.ResetPassword(&req); err != nil {
		response.BizError(c, err)
		return
	}
	response.Success(c, nil)
}

// GetCaptcha 获取验证码
func (ctrl *Controller) GetCaptcha(c *gin.Context) {
	id, b64s, err := captcha.GenerateCaptcha()
	if err != nil {
		response.InternalError(c, "生成图形验证码失败，请刷新重试")
		return
	}
	response.Success(c, gin.H{
		"captcha_id": id,
		"captcha":    b64s,
	})
}
