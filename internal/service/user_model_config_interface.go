package service

import (
	"context"

	requestdto "solvify-agent/internal/model/dto/request"
	responsedto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
)

// UserModelConfigServiceInterface 定义用户模型配置服务接口
type UserModelConfigServiceInterface interface {
	Create(ctx context.Context, userID string, req requestdto.CreateUserModelConfigRequest) (responsedto.UserModelConfigInfo, error)
	Update(ctx context.Context, userID string, configID string, req requestdto.UpdateUserModelConfigRequest) (responsedto.UserModelConfigInfo, error)
	Delete(ctx context.Context, userID string, configID string) error
	Get(ctx context.Context, userID string, configID string) (responsedto.UserModelConfigInfo, error)
	List(ctx context.Context, userID string) (responsedto.ListUserModelConfigsResponse, error)
	ResolveModelConfig(ctx context.Context, userID string, configID string) (*entity.UserModelConfig, error)
	Test(ctx context.Context, req requestdto.TestModelRequest) (responsedto.TestResult, error)
}
