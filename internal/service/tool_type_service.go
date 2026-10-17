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
	apperrors "solvify-agent/pkg/errors"
)

type toolTypeService struct {
	repo repository.ToolTypeRepository
	// 删除类型时，在同一事务清理供应商及旧用户配置。
	providerRepo repository.ToolProviderRepository
	configRepo   repository.UserToolConfigRepository
	txMgr        repository.TxManager
}

// NewToolTypeService 创建工具类型服务实例
func NewToolTypeService(
	repo repository.ToolTypeRepository,
	providerRepo repository.ToolProviderRepository,
	configRepo repository.UserToolConfigRepository,
	txMgr repository.TxManager,
) ToolTypeService {
	return &toolTypeService{
		repo:         repo,
		providerRepo: providerRepo,
		configRepo:   configRepo,
		txMgr:        txMgr,
	}
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
		InputSchema:   datatypes.JSON(req.InputSchema),
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
		return nil, apperrors.NotFoundOrInternal(apperrors.CodeToolTypeNotFound, err)
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
	if req.InputSchema != nil {
		toolType.InputSchema = datatypes.JSON(*req.InputSchema)
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
	// 先确认存在，口径与 GetByID 一致（不存在 → 404，而不是把「查询失败」也说成不存在）。
	// 放在事务外做，免得在事务里塞一次只读预检。
	if _, err := s.repo.GetByID(ctx, id); err != nil {
		return apperrors.NotFoundOrInternal(apperrors.CodeToolTypeNotFound, err)
	}

	// 同一事务依次清理旧用户配置、供应商和工具类型。
	// 任一步失败整体回滚，不留孤儿。
	return s.txMgr.InTx(ctx, func(ctx context.Context) error {
		if err := s.configRepo.DeleteByToolTypeID(ctx, id); err != nil {
			return apperrors.WrapDefault(apperrors.CodeInternalError, err)
		}
		if err := s.providerRepo.DeleteByToolTypeID(ctx, id); err != nil {
			return apperrors.WrapDefault(apperrors.CodeInternalError, err)
		}
		if err := s.repo.Delete(ctx, id); err != nil {
			return apperrors.WrapDefault(apperrors.CodeInternalError, err)
		}
		return nil
	})
}

func (s *toolTypeService) GetByID(ctx context.Context, id string) (*response.ToolTypeInfo, error) {
	toolType, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, apperrors.NotFoundOrInternal(apperrors.CodeToolTypeNotFound, err)
	}
	return s.toToolTypeInfo(toolType, 0), nil
}

func (s *toolTypeService) List(ctx context.Context) (*response.ListToolTypesResponse, error) {
	toolTypes, err := s.repo.List(ctx)
	if err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	// 获取所有工具类型的供应商数量
	providerCounts, err := s.repo.GetProviderCounts(ctx)
	if err != nil {
		providerCounts = make(map[string]int)
	}

	infos := make([]response.ToolTypeInfo, len(toolTypes))
	for i, tt := range toolTypes {
		infos[i] = *s.toToolTypeInfo(&tt, providerCounts[tt.ID])
	}

	return &response.ListToolTypesResponse{ToolTypes: infos}, nil
}

func (s *toolTypeService) ListEnabled(ctx context.Context) (*response.ListToolTypesResponse, error) {
	toolTypes, err := s.repo.ListEnabled(ctx)
	if err != nil {
		return nil, apperrors.WrapDefault(apperrors.CodeInternalError, err)
	}

	// 获取所有工具类型的供应商数量
	providerCounts, err := s.repo.GetProviderCounts(ctx)
	if err != nil {
		providerCounts = make(map[string]int)
	}

	infos := make([]response.ToolTypeInfo, len(toolTypes))
	for i, tt := range toolTypes {
		infos[i] = *s.toToolTypeInfo(&tt, providerCounts[tt.ID])
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
		InputSchema:   json.RawMessage(tt.InputSchema),
		IsEnabled:     tt.IsEnabled,
		ProviderCount: providerCount,
	}
}
