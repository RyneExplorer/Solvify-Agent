package service

import (
	"context"

	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/repository"
)

const defaultMaxStorageBytes int64 = 10 * 1024 * 1024 * 1024

// storageService 封装存储配额业务用例实现
type storageService struct {
	storageQuotaRepo repository.StorageQuotaRepository
}

// NewStorageService 创建存储配额业务服务
func NewStorageService(storageQuotaRepo repository.StorageQuotaRepository) StorageServiceInterface {
	return &storageService{storageQuotaRepo: storageQuotaRepo}
}

// Quota 查询当前用户存储配额
func (s *storageService) Quota(ctx context.Context, userID string) (dto.StorageQuotaResponse, error) {
	quota, ok, err := s.storageQuotaRepo.FindByUserID(ctx, userID)
	if err != nil {
		return dto.StorageQuotaResponse{}, err
	}
	if !ok {
		return storageQuotaResponse(defaultMaxStorageBytes, 0), nil
	}
	return storageQuotaResponse(quota.MaxStorageBytes, quota.UsedStorageBytes), nil
}

// storageQuotaResponse 转换存储配额响应 DTO
func storageQuotaResponse(maxBytes, usedBytes int64) dto.StorageQuotaResponse {
	remaining := maxBytes - usedBytes
	if remaining < 0 {
		remaining = 0
	}
	return dto.StorageQuotaResponse{
		MaxStorageBytes:       maxBytes,
		UsedStorageBytes:      usedBytes,
		RemainingStorageBytes: remaining,
	}
}
