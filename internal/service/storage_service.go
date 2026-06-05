package service

import (
	"context"

	responsedto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/repository"
)

const defaultMaxStorageBytes int64 = 10 * 1024 * 1024 * 1024

// StorageService 封装存储配额业务用例
type StorageService struct {
	repo repository.StorageQuotaRepository
}

// NewStorageService 创建存储配额业务服务
func NewStorageService(repo repository.StorageQuotaRepository) *StorageService {
	return &StorageService{repo: repo}
}

// Quota 查询当前用户存储配额
func (s *StorageService) Quota(ctx context.Context, userID string) (responsedto.StorageQuotaResponse, error) {
	quota, ok, err := s.repo.FindByUserID(ctx, userID)
	if err != nil {
		return responsedto.StorageQuotaResponse{}, err
	}
	if !ok {
		return storageQuotaResponse(defaultMaxStorageBytes, 0), nil
	}
	return storageQuotaResponse(quota.MaxStorageBytes, quota.UsedStorageBytes), nil
}

// storageQuotaResponse 转换存储配额响应 DTO
func storageQuotaResponse(maxBytes, usedBytes int64) responsedto.StorageQuotaResponse {
	remaining := maxBytes - usedBytes
	if remaining < 0 {
		remaining = 0
	}
	return responsedto.StorageQuotaResponse{
		MaxStorageBytes:       maxBytes,
		UsedStorageBytes:      usedBytes,
		RemainingStorageBytes: remaining,
	}
}
