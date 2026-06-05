package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"solvify-agent/internal/model/entity"
)

// storageQuotaRepository 封装存储配额 GORM 数据访问
type storageQuotaRepository struct {
	db *gorm.DB
}

// NewStorageQuotaRepository 创建存储配额数据仓储
func NewStorageQuotaRepository(db *gorm.DB) StorageQuotaRepository {
	return &storageQuotaRepository{db: db}
}

// FindByUserID 查询用户存储配额
func (r *storageQuotaRepository) FindByUserID(ctx context.Context, userID string) (entity.StorageQuota, bool, error) {
	var quota entity.StorageQuota
	err := r.db.WithContext(ctx).Where("user_id = ?", userID).First(&quota).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return entity.StorageQuota{}, false, nil
	}
	return quota, err == nil, err
}
