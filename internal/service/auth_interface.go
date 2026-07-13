package service

import (
	"context"

	"solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
)

// AuthServiceInterface 认证服务接口
type AuthServiceInterface interface {
	Login(req *request.LoginRequest) (*dto.LoginResponse, error)
	RefreshToken(token string) (string, error)
	IsTokenRevoked(ctx context.Context, token string) (bool, error)
	SendEmailCode(email string) error
	Register(req *request.RegisterRequest) error
	Logout(token string) error
	ResetPassword(req *request.ResetPasswordRequest) error
}
