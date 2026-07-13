package service

import (
	"context"

	"solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/model/dto/response"
)

// UserToolConfigService 用户工具配置服务接口
type UserToolConfigService interface {
	Create(ctx context.Context, userID string, req request.CreateUserToolConfigRequest) (*response.UserToolConfigInfo, error)
	Update(ctx context.Context, userID, id string, req request.UpdateUserToolConfigRequest) (*response.UserToolConfigInfo, error)
	Delete(ctx context.Context, userID, id string) error
	Get(ctx context.Context, userID, id string) (*response.UserToolConfigInfo, error)
	List(ctx context.Context, userID string) (*response.ListUserToolConfigsResponse, error)
	ListEnabled(ctx context.Context, userID string) (*response.ListUserToolConfigsResponse, error)
}
