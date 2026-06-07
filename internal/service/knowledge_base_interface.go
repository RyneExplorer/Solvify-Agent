package service

import (
	"context"

	requestdto "solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
)

// KnowledgeBaseServiceInterface 定义知识库服务接口
type KnowledgeBaseServiceInterface interface {
	Create(ctx context.Context, userID string, req requestdto.CreateKnowledgeBaseRequest) (dto.KnowledgeBaseResponse, error)
	List(ctx context.Context, userID string) ([]dto.KnowledgeBaseResponse, error)
	Detail(ctx context.Context, userID, kbID string) (dto.KnowledgeBaseResponse, error)
	Update(ctx context.Context, userID, kbID string, req requestdto.UpdateKnowledgeBaseRequest) (dto.KnowledgeBaseResponse, error)
	Delete(ctx context.Context, userID, kbID string) error
	Stats(ctx context.Context, userID, kbID string) (dto.KnowledgeBaseStatsResponse, error)
}
