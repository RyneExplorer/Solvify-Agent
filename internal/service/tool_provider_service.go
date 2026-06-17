package service

import (
	"context"
	"encoding/json"
	"fmt"

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

	// 防线一：检查 provider_key 是否在 Registry 中已注册
	if s.registry != nil && s.registry.Get(req.ProviderKey) == nil {
		return nil, apperrors.New(apperrors.CodeBadRequest, fmt.Sprintf("供应商标识 '%s' 未在后端注册，可用值: %v", req.ProviderKey, s.registry.Keys()))
	}

	// 检查 provider_key 在当前 tool_type 下是否已存在
	exists, err := s.repo.ExistsByKey(ctx, req.ToolTypeID, req.ProviderKey)
	if err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}
	if exists {
		return nil, apperrors.New(apperrors.CodeToolProviderExists, fmt.Sprintf("供应商标识 '%s' 已存在", req.ProviderKey))
	}

	provider := &entity.ToolProvider{
		ToolTypeID:  req.ToolTypeID,
		Name:        req.Name,
		ProviderKey: req.ProviderKey,
		Description: req.Description,
		AdminConfig: datatypes.JSON(req.AdminConfig),
		RateLimit:   datatypes.JSON(req.RateLimit),
		IsEnabled:   true,
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

// ListProviderKeys 返回所有已注册的 provider_key
func (s *toolProviderService) ListProviderKeys() []string {
	if s.registry == nil {
		return nil
	}
	return s.registry.Keys()
}

func (s *toolProviderService) toProviderInfo(p *entity.ToolProvider) *response.ToolProviderInfo {
	var configSchema json.RawMessage
	if s.registry != nil {
		if provider := s.registry.Get(p.ProviderKey); provider != nil {
			if schema := provider.GetConfigSchema(); schema != nil {
				configSchema, _ = json.Marshal(schema)
			}
		}
	}
	return &response.ToolProviderInfo{
		ID:           p.ID,
		ToolTypeID:   p.ToolTypeID,
		Name:         p.Name,
		ProviderKey:  p.ProviderKey,
		Description:  p.Description,
		ConfigSchema: configSchema,
		AdminConfig:  json.RawMessage(p.AdminConfig),
		RateLimit:    json.RawMessage(p.RateLimit),
		IsEnabled:    p.IsEnabled,
		DisplayOrder: p.DisplayOrder,
	}
}
