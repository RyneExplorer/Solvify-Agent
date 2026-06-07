package service

import (
	"context"

	dto "solvify-agent/internal/model/dto/response"
)

// StorageServiceInterface 定义存储配额服务接口
type StorageServiceInterface interface {
	Quota(ctx context.Context, userID string) (dto.StorageQuotaResponse, error)
}
