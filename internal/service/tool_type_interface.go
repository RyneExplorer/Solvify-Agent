package service

import (
	"context"

	"solvify-agent/internal/model/dto/request"
	"solvify-agent/internal/model/dto/response"
)

// ToolTypeService 工具类型服务接口
type ToolTypeService interface {
	Create(ctx context.Context, req request.CreateToolTypeRequest) (*response.ToolTypeInfo, error)
	Update(ctx context.Context, id string, req request.UpdateToolTypeRequest) (*response.ToolTypeInfo, error)
	Delete(ctx context.Context, id string) error
	GetByID(ctx context.Context, id string) (*response.ToolTypeInfo, error)
	List(ctx context.Context) (*response.ListToolTypesResponse, error)
	ListEnabled(ctx context.Context) (*response.ListToolTypesResponse, error)
}
