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

type userService struct {
	userRepo repository.UserRepository
}

// NewUserService 创建用户服务
func NewUserService(userRepo repository.UserRepository) UserServiceInterface {
	return &userService{
		userRepo: userRepo,
	}
}

// Register 用户注册
func (s *userService) Register(req *request.RegisterRequest) error {
	exists, err := s.userRepo.ExistsByUsername(req.Username)
	if err != nil {
		return err
	}
	if exists {
		return apperrors.New(apperrors.CodeUserAlreadyExists, "用户已存在")
	}

	exists, err = s.userRepo.ExistsByEmail(req.Email)
	if err != nil {
		return err
	}
	if exists {
		return apperrors.New(apperrors.CodeUserAlreadyExists, "邮箱已被注册")
	}

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

	if err := s.userRepo.Create(user); err != nil {
		return err
	}

	return nil
}

// GetUserByID 根据 ID 获取用户
func (s *userService) GetUserByID(id string) (*entity.User, error) {
	user, err := s.userRepo.FindByID(id)
	if err != nil {
		return nil, err
	}
	if user == nil {
		return nil, apperrors.New(apperrors.CodeUserNotFound, "用户不存在")
	}
	return user, nil
}

// UpdateUser 更新用户信息
func (s *userService) UpdateUser(id string, req *request.UpdateUserRequest) error {
	// 1. 先确认用户存在，并拿到当前资料用于比对邮箱等字段
	user, err := s.GetUserByID(id)
	if err != nil {
		return err
	}

	// 2. 再把本次请求中真正需要变更的字段整理成更新集合
	updates := map[string]interface{}{}
	if avatar := strings.TrimSpace(req.Avatar); avatar != "" {
		updates["avatar"] = avatar
	}

	if req.Email != "" {
		newEmail := strings.TrimSpace(req.Email)
		if newEmail != "" && newEmail != user.Email {
			// 3. 若邮箱发生变化，则额外校验邮箱唯一性后再执行更新
			other, err := s.userRepo.FindByEmail(newEmail)
			if err != nil {
				return err
			}
			if other != nil && other.ID != user.ID {
				return apperrors.New(apperrors.CodeUserAlreadyExists, "邮箱已被注册")
			}
			updates["email"] = newEmail
		}
	}

	return s.userRepo.Update(id, updates)
}

// ChangePassword 修改密码
func (s *userService) ChangePassword(id string, req *request.ChangePasswordRequest) error {
	// 1. 先确认用户存在，并校验旧密码是否正确
	user, err := s.GetUserByID(id)
	if err != nil {
		return err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.OldPassword)); err != nil {
		return apperrors.New(apperrors.CodeInvalidCredentials, "用户名或密码错误")
	}

	// 2. 再对新密码执行 bcrypt 加密，避免明文落库
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	// 3. 最后只更新密码字段，缩小本次写操作影响范围
	return s.userRepo.Update(id, map[string]interface{}{
		"password": string(hashedPassword),
	})
}

// GetUserResponse 构造用户响应对象
func (s *userService) GetUserResponse(user *entity.User) *dto.UserResponse {
	return &dto.UserResponse{
		ID:        user.ID,
		Username:  user.Username,
		Email:     user.Email,
		Avatar:    user.Avatar,
		Status:    user.Status,
		LastModel: user.LastModel,
		CreatedAt: user.CreatedAt,
		UpdatedAt: user.UpdatedAt,
	}
}

// AdminListUsers 管理员分页查询用户列表
func (s *userService) AdminListUsers(adminID string, req *request.AdminUserListRequest) (*response.PageResponse, error) {
	_ = adminID

	// 1. 先规范分页参数，避免异常值直接影响查询行为
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

	if req.Status != nil && *req.Status != 1 && *req.Status != 2 {
		return nil, apperrors.New(apperrors.CodeInvalidParam, "status 只能是 1 或 2")
	}

	// 2. 再带着筛选条件查询列表和总数，保证分页信息完整
	offset := (page - 1) * pageSize
	users, total, err := s.userRepo.AdminList(offset, pageSize, &repository.UserListFilter{
		Username: req.Username,
		Nickname: req.Nickname,
		Status:   req.Status,
	})
	if err != nil {
		return nil, err
	}

	// 3. 最后把仓储层结果转换成管理员端的用户列表响应
	list := make([]*dto.AdminUserListItem, 0, len(users))
	for _, user := range users {
		list = append(list, &dto.AdminUserListItem{
			ID:        user.ID,
			Username:  user.Username,
			Email:     user.Email,
			Avatar:    user.Avatar,
			Status:    user.Status,
			CreatedAt: user.CreatedAt,
			UpdatedAt: user.UpdatedAt,
		})
	}

	return response.NewPageResponse(list, total, page, pageSize), nil
}
