package service

import (
	"strings"

	"solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
	apperrors "solvify-agent/pkg/errors"
	"solvify-agent/pkg/response"

	"golang.org/x/crypto/bcrypt"
)

// adminUserService 管理员用户服务实现
type adminUserService struct {
	userRepo repository.UserRepository
}

// NewAdminUserService 创建管理员用户服务
func NewAdminUserService(userRepo repository.UserRepository) AdminUserServiceInterface {
	return &adminUserService{userRepo: userRepo}
}

// List 管理员分页查询用户列表
func (s *adminUserService) List(adminID string, req *request.AdminUserListRequest) (*response.PageResponse, error) {
	_ = adminID

	page := req.Page
	if page < 1 {
		page = 1
	}
	pageSize := req.PageSize
	if pageSize < 1 {
		pageSize = 10
	}
	if pageSize > 100 {
		pageSize = 100
	}

	offset := (page - 1) * pageSize
	users, total, err := s.userRepo.AdminList(offset, pageSize, &repository.UserListFilter{
		Username: req.Username,
		Email:    req.Email,
		Status:   req.Status,
		Role:     req.Role,
	})
	if err != nil {
		return nil, err
	}

	list := make([]*dto.AdminUserListItem, 0, len(users))
	for _, user := range users {
		list = append(list, &dto.AdminUserListItem{
			ID:        user.ID,
			Username:  user.Username,
			Email:     user.Email,
			Avatar:    user.Avatar,
			Role:      user.Role,
			Status:    user.Status,
			CreatedAt: user.CreatedAt,
			UpdatedAt: user.UpdatedAt,
		})
	}

	return response.NewPageResponse(list, total, page, pageSize), nil
}

// Create 管理员创建用户
func (s *adminUserService) Create(adminID string, req *request.AdminCreateUserRequest) (*dto.AdminUserListItem, error) {
	_ = adminID

	if exists, err := s.userRepo.ExistsByUsername(req.Username); err != nil {
		return nil, err
	} else if exists {
		return nil, apperrors.New(apperrors.CodeUserAlreadyExists, "用户名已存在")
	}

	if exists, err := s.userRepo.ExistsByEmail(req.Email); err != nil {
		return nil, err
	} else if exists {
		return nil, apperrors.New(apperrors.CodeUserAlreadyExists, "邮箱已被注册")
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}

	status := req.Status
	if status == 0 {
		status = 1
	}
	role := req.Role
	if role == 0 {
		role = 1
	}

	user := &entity.User{
		Username: req.Username,
		Email:    req.Email,
		Password: string(hashedPassword),
		Status:   status,
		Role:     role,
	}

	if err := s.userRepo.Create(user); err != nil {
		return nil, err
	}

	return &dto.AdminUserListItem{
		ID:        user.ID,
		Username:  user.Username,
		Email:     user.Email,
		Avatar:    user.Avatar,
		Role:      user.Role,
		Status:    user.Status,
		CreatedAt: user.CreatedAt,
		UpdatedAt: user.UpdatedAt,
	}, nil
}

// Update 管理员更新用户信息
func (s *adminUserService) Update(adminID, userID string, req *request.AdminUpdateUserRequest) error {
	_ = adminID

	user, err := s.userRepo.FindByID(userID)
	if err != nil {
		return err
	}
	if user == nil {
		return apperrors.New(apperrors.CodeUserNotFound, "用户不存在")
	}

	updates := make(map[string]interface{})

	if username := strings.TrimSpace(req.Username); username != "" && username != user.Username {
		exists, err := s.userRepo.ExistsByUsername(username)
		if err != nil {
			return err
		}
		if exists {
			return apperrors.New(apperrors.CodeUserAlreadyExists, "用户名已存在")
		}
		updates["username"] = username
	}

	if email := strings.TrimSpace(req.Email); email != "" && email != user.Email {
		exists, err := s.userRepo.ExistsByEmail(email)
		if err != nil {
			return err
		}
		if exists {
			return apperrors.New(apperrors.CodeUserAlreadyExists, "邮箱已被注册")
		}
		updates["email"] = email
	}

	if req.Status != nil {
		updates["status"] = *req.Status
	}
	if req.Role != nil {
		updates["role"] = *req.Role
	}

	if len(updates) == 0 {
		return nil
	}

	return s.userRepo.Update(userID, updates)
}

// Delete 管理员删除用户
func (s *adminUserService) Delete(adminID, userID string) error {
	if adminID == userID {
		return apperrors.New(apperrors.CodeForbidden, "不能删除当前登录的管理员账号")
	}

	user, err := s.userRepo.FindByID(userID)
	if err != nil {
		return err
	}
	if user == nil {
		return apperrors.New(apperrors.CodeUserNotFound, "用户不存在")
	}

	return s.userRepo.Delete(userID)
}

// ResetPassword 管理员重置用户密码
func (s *adminUserService) ResetPassword(adminID, userID string, req *request.AdminResetPasswordRequest) error {
	_ = adminID

	user, err := s.userRepo.FindByID(userID)
	if err != nil {
		return err
	}
	if user == nil {
		return apperrors.New(apperrors.CodeUserNotFound, "用户不存在")
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	return s.userRepo.Update(userID, map[string]interface{}{
		"password": string(hashedPassword),
	})
}
