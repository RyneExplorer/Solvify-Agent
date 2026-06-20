package service

import (
	"context"
	"encoding/json"

	"gorm.io/datatypes"

	"solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
	"solvify-agent/internal/tool"
	apperrors "solvify-agent/pkg/errors"
)

type toolProviderService struct {
	repo     repository.ToolProviderRepository
	typeRepo repository.ToolTypeRepository
	registry tool.ProviderRegistry
}

// NewToolProviderService 创建工具供应商服务实例
func NewToolProviderService(
	repo repository.ToolProviderRepository,
	typeRepo repository.ToolTypeRepository,
	registry tool.ProviderRegistry,
) ToolProviderService {
	return &toolProviderService{
		repo:     repo,
		typeRepo: typeRepo,
		registry: registry,
	}
}

func (s *toolProviderService) Create(ctx context.Context, req request.CreateToolProviderRequest) (*response.ToolProviderInfo, error) {
	// 检查工具类型是否存在
	_, err := s.typeRepo.GetByID(ctx, req.ToolTypeID)
	if err != nil {
		return nil, apperrors.NewDefault(apperrors.CodeToolTypeNotFound)
	}

	// 检查 provider_type 是否已注册
	if s.registry.Get(req.ProviderType) == nil {
		return nil, apperrors.New(apperrors.CodeBadRequest, "供应商类型未注册")
	}

	// 检查 provider_key 在当前 tool_type 下是否已存在
	exists, err := s.repo.ExistsByKey(ctx, req.ToolTypeID, req.ProviderKey)
	if err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}
	if exists {
		return nil, apperrors.New(apperrors.CodeToolProviderExists, "供应商标识已存在")
	}

	provider := &entity.ToolProvider{
		ToolTypeID:     req.ToolTypeID,
		ProviderKey:    req.ProviderKey,
		Name:           req.Name,
		Description:    req.Description,
		ProviderType:   req.ProviderType,
		ConfigSchema:   datatypes.JSON(req.ConfigSchema),
		InputSchema:    datatypes.JSON(req.InputSchema),
		ProviderConfig: datatypes.JSON(req.ProviderConfig),
		AdminConfig:    datatypes.JSON(req.AdminConfig),
		RateLimit:      datatypes.JSON(req.RateLimit),
		IsEnabled:      true,
	}

	if err := s.repo.Create(ctx, provider); err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	return s.toProviderInfo(provider), nil
}

func (s *toolProviderService) Update(ctx context.Context, id string, req request.UpdateToolProviderRequest) (*response.ToolProviderInfo, error) {
	provider, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, apperrors.NewDefault(apperrors.CodeToolProviderNotFound)
	}

	if req.Name != nil {
		provider.Name = *req.Name
	}
	if req.Description != nil {
		provider.Description = *req.Description
	}
	if req.ProviderType != nil {
		// 检查 provider_type 是否已注册
		if s.registry.Get(*req.ProviderType) == nil {
			return nil, apperrors.New(apperrors.CodeBadRequest, "供应商类型未注册")
		}
		provider.ProviderType = *req.ProviderType
	}
	if req.ConfigSchema != nil {
		provider.ConfigSchema = datatypes.JSON(*req.ConfigSchema)
	}
	if req.InputSchema != nil {
		provider.InputSchema = datatypes.JSON(*req.InputSchema)
	}
	if req.ProviderConfig != nil {
		provider.ProviderConfig = datatypes.JSON(*req.ProviderConfig)
	}
	if req.AdminConfig != nil {
		provider.AdminConfig = datatypes.JSON(*req.AdminConfig)
	}
	if req.RateLimit != nil {
		provider.RateLimit = datatypes.JSON(*req.RateLimit)
	}
	if req.IsEnabled != nil {
		provider.IsEnabled = *req.IsEnabled
	}

	if err := s.repo.Update(ctx, provider); err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	return s.toProviderInfo(provider), nil
}

func (s *toolProviderService) Delete(ctx context.Context, id string) error {
	return s.repo.Delete(ctx, id)
}

func (s *toolProviderService) GetByID(ctx context.Context, id string) (*response.ToolProviderInfo, error) {
	provider, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, apperrors.NewDefault(apperrors.CodeToolProviderNotFound)
	}
	return s.toProviderInfo(provider), nil
}

func (s *toolProviderService) ListByToolTypeID(ctx context.Context, toolTypeID string) (*response.ListToolProvidersResponse, error) {
	providers, err := s.repo.ListByToolTypeID(ctx, toolTypeID)
	if err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	infos := make([]response.ToolProviderInfo, len(providers))
	for i, p := range providers {
		infos[i] = *s.toProviderInfo(&p)
	}

	return &response.ListToolProvidersResponse{Providers: infos}, nil
}

func (s *toolProviderService) ListEnabledByToolTypeID(ctx context.Context, toolTypeID string) (*response.ListToolProvidersResponse, error) {
	providers, err := s.repo.ListEnabledByToolTypeID(ctx, toolTypeID)
	if err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	infos := make([]response.ToolProviderInfo, len(providers))
	for i, p := range providers {
		infos[i] = *s.toProviderInfo(&p)
	}

	return &response.ListToolProvidersResponse{Providers: infos}, nil
}

// ListProviderTypes 返回所有已注册的供应商类型
func (s *toolProviderService) ListProviderTypes() []string {
	if s.registry == nil {
		return nil
	}
	return s.registry.Keys()
}

func (s *toolProviderService) toProviderInfo(p *entity.ToolProvider) *response.ToolProviderInfo {
	return &response.ToolProviderInfo{
		ID:             p.ID,
		ToolTypeID:     p.ToolTypeID,
		ProviderKey:    p.ProviderKey,
		Name:           p.Name,
		Description:    p.Description,
		ProviderType:   p.ProviderType,
		ConfigSchema:   json.RawMessage(p.ConfigSchema),
		InputSchema:    json.RawMessage(p.InputSchema),
		ProviderConfig: json.RawMessage(p.ProviderConfig),
		AdminConfig:    json.RawMessage(p.AdminConfig),
		RateLimit:      json.RawMessage(p.RateLimit),
		IsEnabled:      p.IsEnabled,
		DisplayOrder:   p.DisplayOrder,
	}
}
