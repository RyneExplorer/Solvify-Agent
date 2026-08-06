package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
	"solvify-agent/pkg/captcha"
	"solvify-agent/pkg/email"
	apperrors "solvify-agent/pkg/errors"
	"solvify-agent/pkg/jwt"
	"solvify-agent/pkg/logger"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

type authService struct {
	userRepo    repository.UserRepository
	userService UserServiceInterface
	redis       *redis.Client
}

const tokenBlacklistPrefix = "auth:token:blacklist:"

// NewAuthService 创建认证服务
func NewAuthService(
	userRepo repository.UserRepository,
	userService UserServiceInterface,
	redisClient *redis.Client,
) AuthServiceInterface {
	return &authService{
		userRepo:    userRepo,
		userService: userService,
		redis:       redisClient,
	}
}
func (s *authService) Logout(token string) error {
	claims, err := jwt.ParseToken(token)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return apperrors.NewWithErr(apperrors.CodeTokenExpired, "token 已过期", err)
		}
		return apperrors.NewWithErr(apperrors.CodeInvalidToken, "无效的 token", err)
	}
	if claims.ExpiresAt == nil {
		return apperrors.New(apperrors.CodeInvalidToken, "无效的 token")
	}

	ttl := time.Until(claims.ExpiresAt.Time)
	if ttl <= 0 {
		return apperrors.New(apperrors.CodeTokenExpired, "token 已过期")
	}

	if err := s.redis.Set(context.Background(), tokenBlacklistKey(token), "1", ttl).Err(); err != nil {
		return apperrors.NewWithErr(apperrors.CodeInternalError, "写入 token 黑名单失败", err)
	}
	return nil
}

func (s *authService) IsTokenRevoked(ctx context.Context, token string) (bool, error) {
	count, err := s.redis.Exists(ctx, tokenBlacklistKey(token)).Result()
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *authService) ResetPassword(req *request.ResetPasswordRequest) error {
	ctx := context.Background()
	codeKey := fmt.Sprintf("email_code:%s", req.Email)

	// 1. 从 Redis 读取邮箱验证码，并校验用户提交的验证码是否一致
	cacheCode, err := s.redis.Get(ctx, codeKey).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return apperrors.New(apperrors.CodeInvalidParam, "验证码已过期或未发送，请重新获取")
		}
		return apperrors.NewWithErr(apperrors.CodeInternalError, "获取验证码缓存失败", err)
	}
	if req.EmailCaptcha != cacheCode {
		return apperrors.New(apperrors.CodeInvalidParam, "验证码输入错误，请重新核对")
	}

	// 2. 验证通过后立即消费验证码，避免同一验证码被重复使用
	_ = s.redis.Del(ctx, codeKey).Err()

	// 3. 确认邮箱对应用户存在后，对新密码加密并执行更新
	user, err := s.userRepo.FindByEmail(req.Email)
	if err != nil {
		return err
	}
	if user == nil {
		return apperrors.New(apperrors.CodeUserNotFound, "用户不存在")
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	return s.userRepo.Update(user.ID, map[string]interface{}{
		"password": string(hashedPassword),
	})
}

func (s *authService) Login(req *request.LoginRequest) (*dto.LoginResponse, error) {
	// 1. 先校验图形验证码，避免在无效请求上继续消耗数据库与加密计算资源
	if !captcha.Verify(req.CaptchaID, req.Captcha) {
		return nil, apperrors.New(apperrors.CodeInvalidCaptcha, "无效的验证码")
	}

	// 2. 再查询用户并校验账号状态，确保只有正常状态的用户可以继续登录
	user, err := s.userRepo.FindByUsername(req.Username)
	if err != nil {
		logger.Errorf("查找用户名失败: username=%s, err=%v", req.Username, err)
		return nil, apperrors.NewWithErr(apperrors.CodeInternalError, "查找用户名失败", err)
	}
	if user == nil {
		return nil, apperrors.New(apperrors.CodeInvalidCredentials, "用户名或密码错误")
	}
	if user.Status != 1 {
		return nil, apperrors.New(apperrors.CodeUserDisabled, "用户状态异常")
	}

	// 3. 最后校验密码、签发 JWT，并组装登录响应返回给调用方
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		logger.Errorf("密码校验失败: username=%s, err=%v", req.Username, err)
		return nil, apperrors.NewWithErr(apperrors.CodeInvalidCredentials, "登录失败：密码错误", err)
	}

	token, err := jwt.GenerateToken(user.ID, user.Username, user.Role)
	if err != nil {
		logger.Error("JWT 令牌生成失败", zap.Error(err))
		return nil, apperrors.NewWithErr(apperrors.CodeInvalidToken, "无效的令牌", err)
	}

	userResp := s.userService.GetUserResponse(user)
	return &dto.LoginResponse{
		Token: token,
		User:  *userResp,
	}, nil
}

func (s *authService) Register(req *request.RegisterRequest) error {
	// 1. 先校验用户名和邮箱是否已被占用，尽量在创建前就拦住冲突请求
	exists, err := s.userRepo.ExistsByUsername(req.Username)
	if err != nil {
		return err
	}
	if exists {
		return apperrors.New(apperrors.CodeUserAlreadyExists, "用户名已存在")
	}

	exists, err = s.userRepo.ExistsByEmail(req.Email)
	if err != nil {
		return err
	}
	if exists {
		return apperrors.New(apperrors.CodeUserAlreadyExists, "邮箱已被注册")
	}

	// 2. 再校验并消费邮箱验证码，确保注册动作与邮箱验证强绑定
	ctx := context.Background()
	codeKey := fmt.Sprintf("email_code:%s", req.Email)
	cacheCode, err := s.redis.Get(ctx, codeKey).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return apperrors.New(apperrors.CodeInvalidParam, "验证码已过期或未发送，请重新获取")
		}
		return apperrors.NewWithErr(apperrors.CodeInternalError, "获取验证码缓存失败", err)
	}
	if req.EmailCaptcha != cacheCode {
		return apperrors.New(apperrors.CodeInvalidParam, "验证码输入错误，请重新核对")
	}
	if delErr := s.redis.Del(ctx, codeKey).Err(); delErr != nil {
		logger.Error("删除验证码失败", zap.Error(delErr))
	}

	// 3. 最后加密密码并创建用户，避免明文密码落库
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	user := &entity.User{
		Username: req.Username,
		Password: string(hashedPassword),
		Email:    req.Email,
		Status:   1,
	}
	return s.userRepo.Create(user)
}

func (s *authService) RefreshToken(token string) (string, error) {
	newToken, err := jwt.RefreshToken(token)
	if err != nil {
		return "", err
	}
	return newToken, nil
}

func (s *authService) SendEmailCode(emailStr string) error {
	// 1. 生成一次性邮箱验证码
	rnd := rand.New(rand.NewSource(time.Now().UnixNano()))
	code := fmt.Sprintf("%06d", rnd.Intn(1000000))

	// 2. 将验证码写入 Redis，并设置有效期控制时效
	ctx := context.Background()
	key := fmt.Sprintf("email_code:%s", emailStr)
	err := s.redis.Set(ctx, key, code, 5*time.Minute).Err()
	if err != nil {
		return apperrors.NewWithErr(apperrors.CodeInternalError, "缓存验证码失败", err)
	}

	// 3. 调用邮件组件把验证码发送给目标邮箱
	subject := "Solvify 邮箱验证码"
	body := fmt.Sprintf(`
<table width="100%%" cellpadding="0" cellspacing="0" border="0" style="background-color:#f1f5f9;padding:32px 16px;font-family:Arial,'Microsoft YaHei',sans-serif;color:#0f172a;">
  <tr>
    <td align="center">
      <table width="560" cellpadding="0" cellspacing="0" border="0" style="width:100%%;max-width:560px;background-color:#ffffff;border:1px solid #e2e8f0;border-radius:12px;overflow:hidden;">
        <tr>
          <td style="background-color:#0f172a;padding:22px 30px;color:#ffffff;font-size:20px;font-weight:600;">
            Solvify
          </td>
        </tr>
        <tr>
          <td style="padding:32px 30px 26px;">
            <h1 style="margin:0 0 14px;font-size:22px;line-height:1.4;font-weight:600;color:#0f172a;">验证您的邮箱</h1>
            <p style="margin:0;font-size:14px;line-height:1.8;color:#475569;">您好，您正在进行 Solvify 邮箱验证，请使用以下验证码完成操作：</p>
            <div style="margin:24px 0;padding:20px 12px;text-align:center;background-color:#f8fafc;border:1px solid #e2e8f0;border-radius:8px;font-size:30px;line-height:1.2;font-weight:600;letter-spacing:8px;color:#0f172a;">%s</div>
            <p style="margin:0;font-size:14px;line-height:1.8;color:#475569;">验证码将在 5 分钟后失效。</p>
            <div style="margin-top:22px;padding:10px 14px;background-color:#f8fafc;border-left:3px solid #64748b;font-size:13px;line-height:1.7;color:#64748b;">如果这不是您本人的操作，请忽略此邮件。请勿向任何人泄露验证码。</div>
          </td>
        </tr>
        <tr>
          <td style="padding:18px 30px;border-top:1px solid #e2e8f0;font-size:12px;line-height:1.6;color:#94a3b8;">
            此邮件由 Solvify 系统自动发送，请勿直接回复。
          </td>
        </tr>
      </table>
    </td>
  </tr>
</table>`, code)
	if err := email.SendEmail(emailStr, subject, body); err != nil {
		return apperrors.NewWithErr(apperrors.CodeInternalError, "发送邮件失败", err)
	}

	return nil
}

func tokenBlacklistKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return tokenBlacklistPrefix + hex.EncodeToString(sum[:])
}
