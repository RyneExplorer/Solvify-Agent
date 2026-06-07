package service

import (
	"context"
	"time"

	requestdto "solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
	apperrors "solvify-agent/pkg/errors"
)

const (
	knowledgeBaseStatusNormal  int16 = 1
	knowledgeBaseStatusDeleted int16 = 2

	knowledgeBaseSourceLocal = "local"
	deleteRetentionDays      = 30
)

// knowledgeBaseService 封装知识库业务用例实现
type knowledgeBaseService struct {
	knowledgeBaseRepo repository.KnowledgeBaseRepository
}

// NewKnowledgeBaseService 创建知识库业务服务
func NewKnowledgeBaseService(knowledgeBaseRepo repository.KnowledgeBaseRepository) KnowledgeBaseServiceInterface {
	return &knowledgeBaseService{knowledgeBaseRepo: knowledgeBaseRepo}
}

// Create 创建本地知识库
func (s *knowledgeBaseService) Create(ctx context.Context, userID string, req requestdto.CreateKnowledgeBaseRequest) (dto.KnowledgeBaseResponse, error) {
	kb := entity.KnowledgeBase{
		UserID:      userID,
		Name:        req.Name,
		Category:    req.Category,
		Description: req.Description,
		SourceType:  knowledgeBaseSourceLocal,
		Status:      knowledgeBaseStatusNormal,
	}
	if err := s.knowledgeBaseRepo.Create(ctx, &kb); err != nil {
		return dto.KnowledgeBaseResponse{}, err
	}
	return knowledgeBaseResponse(kb), nil
}

// List 查询当前用户正常状态的知识库
func (s *knowledgeBaseService) List(ctx context.Context, userID string) ([]dto.KnowledgeBaseResponse, error) {
	items, err := s.knowledgeBaseRepo.ListNormal(ctx, userID, knowledgeBaseStatusNormal)
	if err != nil {
		return nil, err
	}

	output := make([]dto.KnowledgeBaseResponse, 0, len(items))
	for _, item := range items {
		output = append(output, knowledgeBaseResponse(item))
	}
	return output, nil
}

// Detail 查询知识库详情
func (s *knowledgeBaseService) Detail(ctx context.Context, userID, kbID string) (dto.KnowledgeBaseResponse, error) {
	kb, err := s.findNormalKnowledgeBase(ctx, userID, kbID)
	if err != nil {
		return dto.KnowledgeBaseResponse{}, err
	}
	return knowledgeBaseResponse(kb), nil
}

// Update 更新知识库基础信息
func (s *knowledgeBaseService) Update(ctx context.Context, userID, kbID string, req requestdto.UpdateKnowledgeBaseRequest) (dto.KnowledgeBaseResponse, error) {
	ok, err := s.knowledgeBaseRepo.UpdateBasic(ctx, userID, kbID, knowledgeBaseStatusNormal, req.Name, req.Category, req.Description)
	if err != nil {
		return dto.KnowledgeBaseResponse{}, err
	}
	if !ok {
		return dto.KnowledgeBaseResponse{}, apperrors.NewDefault(apperrors.CodeKnowledgeBaseNotFound)
	}
	return s.Detail(ctx, userID, kbID)
}

// Delete 软删除知识库
func (s *knowledgeBaseService) Delete(ctx context.Context, userID, kbID string) error {
	now := time.Now()
	expiredAt := now.AddDate(0, 0, deleteRetentionDays)
	ok, err := s.knowledgeBaseRepo.SoftDelete(ctx, userID, kbID, knowledgeBaseStatusNormal, knowledgeBaseStatusDeleted, now, expiredAt)
	if err != nil {
		return err
	}
	if !ok {
		return apperrors.NewDefault(apperrors.CodeKnowledgeBaseNotFound)
	}
	return nil
}

// Stats 查询知识库统计数据
func (s *knowledgeBaseService) Stats(ctx context.Context, userID, kbID string) (dto.KnowledgeBaseStatsResponse, error) {
	if _, err := s.findNormalKnowledgeBase(ctx, userID, kbID); err != nil {
		return dto.KnowledgeBaseStatsResponse{}, err
	}

	documentCount, err := s.knowledgeBaseRepo.CountDocuments(ctx, userID, kbID, 5)
	if err != nil {
		return dto.KnowledgeBaseStatsResponse{}, err
	}

	storageBytes, err := s.knowledgeBaseRepo.SumDocumentStorage(ctx, userID, kbID, 5)
	if err != nil {
		return dto.KnowledgeBaseStatsResponse{}, err
	}

	retrievableChunkCount, err := s.knowledgeBaseRepo.CountRetrievableChunks(ctx, userID, kbID)
	if err != nil {
		return dto.KnowledgeBaseStatsResponse{}, err
	}

	return dto.KnowledgeBaseStatsResponse{
		KnowledgeBaseID:       kbID,
		DocumentCount:         documentCount,
		StorageBytes:          storageBytes,
		RetrievableChunkCount: retrievableChunkCount,
	}, nil
}

// findNormalKnowledgeBase 查询当前用户正常状态的知识库
func (s *knowledgeBaseService) findNormalKnowledgeBase(ctx context.Context, userID, kbID string) (entity.KnowledgeBase, error) {
	kb, ok, err := s.knowledgeBaseRepo.FindNormal(ctx, userID, kbID, knowledgeBaseStatusNormal)
	if err != nil {
		return entity.KnowledgeBase{}, err
	}
	if !ok {
		return entity.KnowledgeBase{}, apperrors.NewDefault(apperrors.CodeKnowledgeBaseNotFound)
	}
	return kb, nil
}

// knowledgeBaseResponse 转换知识库响应 DTO
func knowledgeBaseResponse(kb entity.KnowledgeBase) dto.KnowledgeBaseResponse {
	return dto.KnowledgeBaseResponse{
		ID:             kb.ID,
		Name:           kb.Name,
		Category:       kb.Category,
		Description:    kb.Description,
		SourceType:     kb.SourceType,
		SourcePlatform: kb.SourcePlatform,
		DocumentCount:  kb.DocumentCount,
		StorageBytes:   kb.StorageBytes,
		Status:         kb.Status,
		CreatedAt:      kb.CreatedAt,
		UpdatedAt:      kb.UpdatedAt,
	}
}
