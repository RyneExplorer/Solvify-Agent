package service

import (
	"context"
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
func (s *authService) Logout(id string) error {
	_ = id
	return nil
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
	subject := "Ryne 博客验证码"
	body := fmt.Sprintf("<h1>您的验证码是: %s</h1><p>有效期 5 分钟，请勿泄露给他人。</p>", code)
	if err := email.SendEmail(emailStr, subject, body); err != nil {
		return apperrors.NewWithErr(apperrors.CodeInternalError, "发送邮件失败", err)
	}

	return nil
}
