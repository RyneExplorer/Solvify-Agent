package service

import (
	"context"

	requestdto "solvify-agent/internal/model/dto/request"
	"solvify-agent/pkg/response"
)

// AdminSessionServiceInterface 管理员会话服务接口
type AdminSessionServiceInterface interface {
	// List 管理员分页查询会话列表
	List(ctx context.Context, req *requestdto.AdminSessionListRequest) (*response.PageResponse, error)
	// Delete 管理员删除指定会话
	Delete(ctx context.Context, sessionID string) error
	// CleanupExpired 清理过期会话，返回删除数量
	CleanupExpired(ctx context.Context, retentionDays int) (int64, error)
}
