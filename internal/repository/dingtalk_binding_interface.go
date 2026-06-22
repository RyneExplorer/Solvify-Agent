package repository

import (
	"context"

	"solvify-agent/internal/model/entity"
)

// DingTalkBindingRepository 定义钉钉用户绑定数据访问能力
type DingTalkBindingRepository interface {
	FindByUserID(ctx context.Context, userID string) (entity.DingTalkUserBinding, bool, error)
	FindByUnionID(ctx context.Context, unionID string) (entity.DingTalkUserBinding, bool, error)
	UpsertByUserID(ctx context.Context, binding entity.DingTalkUserBinding) error
	DeleteByUserID(ctx context.Context, userID string) (bool, error)
}
