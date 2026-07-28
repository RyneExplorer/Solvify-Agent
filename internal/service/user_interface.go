package service

import (
	"solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
	"solvify-agent/pkg/response"
)

// UserServiceInterface 用户服务接口
type UserServiceInterface interface {
	GetUserByID(id string) (*entity.User, error)
	UpdateUser(id string, req *request.UpdateUserRequest) error
	UpdateProfile(id string, req *request.UpdateProfileRequest) error
	ChangePassword(id string, req *request.ChangePasswordRequest) error
	GetUserResponse(user *entity.User) *dto.UserResponse
	GetProfile(id string) (*dto.ProfileResponse, error)
	AdminListUsers(adminID string, req *request.AdminUserListRequest) (*response.PageResponse, error)
}
