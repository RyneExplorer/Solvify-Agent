package service

import (
	"context"

	requestdto "solvify-agent/internal/model/dto/request"
	responsedto "solvify-agent/internal/model/dto/response"
)

// ModelServiceInterface 定义系统模型服务接口
type ModelServiceInterface interface {
	Create(ctx context.Context, req requestdto.CreateModelRequest) (responsedto.ModelInfo, error)
	Update(ctx context.Context, id string, req requestdto.UpdateModelRequest) error
	Delete(ctx context.Context, id string) error
	List(ctx context.Context) (responsedto.ListModelsResponse, error)
	GetByID(ctx context.Context, id string) (responsedto.ModelInfo, error)
	Test(ctx context.Context, req requestdto.TestModelRequest) (responsedto.TestResult, error)
}
