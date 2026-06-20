package service

import (
	"solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/pkg/response"
)

// AdminUserServiceInterface 管理员用户服务接口
type AdminUserServiceInterface interface {
	List(adminID string, req *request.AdminUserListRequest) (*response.PageResponse, error)
	Create(adminID string, req *request.AdminCreateUserRequest) (*dto.AdminUserListItem, error)
	Update(adminID, userID string, req *request.AdminUpdateUserRequest) error
	Delete(adminID, userID string) error
	ResetPassword(adminID, userID string, req *request.AdminResetPasswordRequest) error
}
