package service

import (
	"context"
	"encoding/json"

	"gorm.io/datatypes"

	"solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
	apperrors "solvify-agent/pkg/errors"
)

type userToolConfigService struct {
	repo         repository.UserToolConfigRepository
	typeRepo     repository.ToolTypeRepository
	providerRepo repository.ToolProviderRepository
}

// NewUserToolConfigService 创建用户工具配置服务实例
func NewUserToolConfigService(
	repo repository.UserToolConfigRepository,
	typeRepo repository.ToolTypeRepository,
	providerRepo repository.ToolProviderRepository,
) UserToolConfigService {
	return &userToolConfigService{
		repo:         repo,
		typeRepo:     typeRepo,
		providerRepo: providerRepo,
	}
}

func (s *userToolConfigService) Create(ctx context.Context, userID string, req request.CreateUserToolConfigRequest) (*response.UserToolConfigInfo, error) {
	// 检查工具类型是否存在
	toolType, err := s.typeRepo.GetByID(ctx, req.ToolTypeID)
	if err != nil {
		return nil, apperrors.NewDefault(apperrors.CodeToolTypeNotFound)
	}

	// 检查供应商是否存在
	provider, err := s.providerRepo.GetByID(ctx, req.ProviderID)
	if err != nil {
		return nil, apperrors.NewDefault(apperrors.CodeToolProviderNotFound)
	}

	// 检查是否已配置该工具类型
	existing, _ := s.repo.GetByUserAndToolType(ctx, userID, req.ToolTypeID)
	if existing != nil {
		return nil, apperrors.NewDefault(apperrors.CodeBadRequest)
	}

	config := &entity.UserToolConfig{
		UserID:      userID,
		ToolTypeID:  req.ToolTypeID,
		ProviderID:  req.ProviderID,
		DisplayName: req.DisplayName,
		Config:      datatypes.JSON(req.Config),
		IsEnabled:   true,
	}

	if config.DisplayName == "" {
		config.DisplayName = provider.Name
	}

	if err := s.repo.Create(ctx, config); err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	return s.toConfigInfo(config, toolType, provider), nil
}

func (s *userToolConfigService) Update(ctx context.Context, userID, id string, req request.UpdateUserToolConfigRequest) (*response.UserToolConfigInfo, error) {
	config, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, apperrors.NewDefault(apperrors.CodeBadRequest)
	}

	// 验证归属
	if config.UserID != userID {
		return nil, apperrors.NewDefault(apperrors.CodeBadRequest)
	}

	if req.ProviderID != nil {
		config.ProviderID = *req.ProviderID
	}
	if req.DisplayName != nil {
		config.DisplayName = *req.DisplayName
	}
	if req.Config != nil {
		config.Config = datatypes.JSON(*req.Config)
	}
	if req.IsEnabled != nil {
		config.IsEnabled = *req.IsEnabled
	}

	if err := s.repo.Update(ctx, config); err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	// 重新加载关联数据
	toolType, _ := s.typeRepo.GetByID(ctx, config.ToolTypeID)
	provider, _ := s.providerRepo.GetByID(ctx, config.ProviderID)

	return s.toConfigInfo(config, toolType, provider), nil
}

func (s *userToolConfigService) Delete(ctx context.Context, userID, id string) error {
	config, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return apperrors.NewDefault(apperrors.CodeBadRequest)
	}

	if config.UserID != userID {
		return apperrors.NewDefault(apperrors.CodeBadRequest)
	}

	return s.repo.Delete(ctx, id)
}

func (s *userToolConfigService) Get(ctx context.Context, userID, id string) (*response.UserToolConfigInfo, error) {
	config, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, apperrors.NewDefault(apperrors.CodeBadRequest)
	}

	if config.UserID != userID {
		return nil, apperrors.NewDefault(apperrors.CodeBadRequest)
	}

	toolType, _ := s.typeRepo.GetByID(ctx, config.ToolTypeID)
	provider, _ := s.providerRepo.GetByID(ctx, config.ProviderID)

	return s.toConfigInfo(config, toolType, provider), nil
}

func (s *userToolConfigService) List(ctx context.Context, userID string) (*response.ListUserToolConfigsResponse, error) {
	configs, err := s.repo.ListByUserID(ctx, userID)
	if err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	infos := make([]response.UserToolConfigInfo, len(configs))
	for i, c := range configs {
		toolType, _ := s.typeRepo.GetByID(ctx, c.ToolTypeID)
		provider, _ := s.providerRepo.GetByID(ctx, c.ProviderID)
		infos[i] = *s.toConfigInfo(&c, toolType, provider)
	}

	return &response.ListUserToolConfigsResponse{Configs: infos}, nil
}

func (s *userToolConfigService) ListEnabled(ctx context.Context, userID string) (*response.ListUserToolConfigsResponse, error) {
	configs, err := s.repo.ListEnabledByUserID(ctx, userID)
	if err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	infos := make([]response.UserToolConfigInfo, len(configs))
	for i, c := range configs {
		toolType, _ := s.typeRepo.GetByID(ctx, c.ToolTypeID)
		provider, _ := s.providerRepo.GetByID(ctx, c.ProviderID)
		infos[i] = *s.toConfigInfo(&c, toolType, provider)
	}

	return &response.ListUserToolConfigsResponse{Configs: infos}, nil
}

func (s *userToolConfigService) toConfigInfo(
	config *entity.UserToolConfig,
	toolType *entity.ToolType,
	provider *entity.ToolProvider,
) *response.UserToolConfigInfo {
	info := &response.UserToolConfigInfo{
		ID:          config.ID,
		ToolTypeID:  config.ToolTypeID,
		ProviderID:  config.ProviderID,
		DisplayName: config.DisplayName,
		Config:      json.RawMessage(config.Config),
		IsEnabled:   config.IsEnabled,
	}

	if toolType != nil {
		info.ToolTypeName = toolType.Name
		info.ToolTypeKey = toolType.ToolKey
	}
	if provider != nil {
		info.ProviderName = provider.Name
	}

	return info
}
