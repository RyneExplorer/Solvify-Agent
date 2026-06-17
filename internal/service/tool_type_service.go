package service

import (
	"context"
	"fmt"

	"solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
	apperrors "solvify-agent/pkg/errors"
)

type toolTypeService struct {
	repo repository.ToolTypeRepository
}

// NewToolTypeService 创建工具类型服务实例
func NewToolTypeService(repo repository.ToolTypeRepository) ToolTypeService {
	return &toolTypeService{repo: repo}
}

func (s *toolTypeService) Create(ctx context.Context, req request.CreateToolTypeRequest) (*response.ToolTypeInfo, error) {
	// 检查 tool_key 是否已存在
	exists, err := s.repo.ExistsByKey(ctx, req.ToolKey)
	if err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}
	if exists {
		return nil, apperrors.New(apperrors.CodeToolTypeExists, fmt.Sprintf("工具标识 '%s' 已存在", req.ToolKey))
	}

	executionMode := req.ExecutionMode
	if executionMode == "" {
		executionMode = "sync"
	}

	toolType := &entity.ToolType{
		Name:          req.Name,
		ToolKey:       req.ToolKey,
		Description:   req.Description,
		ExecutionMode: executionMode,
		IsEnabled:     true,
	}

	if err := s.repo.Create(ctx, toolType); err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	return s.toToolTypeInfo(toolType, 0), nil
}

func (s *toolTypeService) Update(ctx context.Context, id string, req request.UpdateToolTypeRequest) (*response.ToolTypeInfo, error) {
	toolType, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, apperrors.NewDefault(apperrors.CodeToolTypeNotFound)
	}

	if req.Name != nil {
		toolType.Name = *req.Name
	}
	if req.Description != nil {
		toolType.Description = *req.Description
	}
	if req.ExecutionMode != nil {
		toolType.ExecutionMode = *req.ExecutionMode
	}
	if req.IsEnabled != nil {
		toolType.IsEnabled = *req.IsEnabled
	}

	if err := s.repo.Update(ctx, toolType); err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	return s.toToolTypeInfo(toolType, 0), nil
}

func (s *toolTypeService) Delete(ctx context.Context, id string) error {
	return s.repo.Delete(ctx, id)
}

func (s *toolTypeService) GetByID(ctx context.Context, id string) (*response.ToolTypeInfo, error) {
	toolType, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, apperrors.NewDefault(apperrors.CodeToolTypeNotFound)
	}
	return s.toToolTypeInfo(toolType, 0), nil
}

func (s *toolTypeService) List(ctx context.Context) (*response.ListToolTypesResponse, error) {
	toolTypes, err := s.repo.List(ctx)
	if err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	infos := make([]response.ToolTypeInfo, len(toolTypes))
	for i, tt := range toolTypes {
		infos[i] = *s.toToolTypeInfo(&tt, 0)
	}

	return &response.ListToolTypesResponse{ToolTypes: infos}, nil
}

func (s *toolTypeService) ListEnabled(ctx context.Context) (*response.ListToolTypesResponse, error) {
	toolTypes, err := s.repo.ListEnabled(ctx)
	if err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	infos := make([]response.ToolTypeInfo, len(toolTypes))
	for i, tt := range toolTypes {
		infos[i] = *s.toToolTypeInfo(&tt, 0)
	}

	return &response.ListToolTypesResponse{ToolTypes: infos}, nil
}

func (s *toolTypeService) toToolTypeInfo(tt *entity.ToolType, providerCount int) *response.ToolTypeInfo {
	return &response.ToolTypeInfo{
		ID:            tt.ID,
		Name:          tt.Name,
		ToolKey:       tt.ToolKey,
		Description:   tt.Description,
		ExecutionMode: tt.ExecutionMode,
		IsEnabled:     tt.IsEnabled,
		ProviderCount: providerCount,
	}
}
