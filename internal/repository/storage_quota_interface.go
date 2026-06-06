package repository

import (
	"context"

	"solvify-agent/internal/model/entity"
)

// StorageQuotaRepository 定义存储配额数据访问能力
type StorageQuotaRepository interface {
	FindByUserID(ctx context.Context, userID string) (entity.StorageQuota, bool, error)
	AddUsedStorage(ctx context.Context, userID string, maxStorageBytes, addBytes int64) error
}
