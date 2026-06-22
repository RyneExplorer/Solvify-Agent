package repository

import (
	"context"
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"solvify-agent/internal/model/entity"
)

// dingtalkBindingRepository 封装钉钉绑定 GORM 数据访问
type dingtalkBindingRepository struct {
	db *gorm.DB
}

// NewDingTalkBindingRepository 创建钉钉绑定仓储
func NewDingTalkBindingRepository(db *gorm.DB) DingTalkBindingRepository {
	return &dingtalkBindingRepository{db: db}
}

// FindByUserID 查询用户钉钉绑定
func (r *dingtalkBindingRepository) FindByUserID(ctx context.Context, userID string) (entity.DingTalkUserBinding, bool, error) {
	var binding entity.DingTalkUserBinding
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		First(&binding).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return entity.DingTalkUserBinding{}, false, nil
	}
	return binding, err == nil, err
}

// FindByUnionID 查询钉钉 unionId 绑定
func (r *dingtalkBindingRepository) FindByUnionID(ctx context.Context, unionID string) (entity.DingTalkUserBinding, bool, error) {
	var binding entity.DingTalkUserBinding
	err := r.db.WithContext(ctx).
		Where("ding_union_id = ?", unionID).
		First(&binding).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return entity.DingTalkUserBinding{}, false, nil
	}
	return binding, err == nil, err
}

// UpsertByUserID 按系统用户保存或更新钉钉绑定
func (r *dingtalkBindingRepository) UpsertByUserID(ctx context.Context, binding entity.DingTalkUserBinding) error {
	return r.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"ding_open_id":  gorm.Expr("COALESCE(NULLIF(EXCLUDED.ding_open_id, ''), dingtalk_user_bindings.ding_open_id)"),
			"ding_union_id": gorm.Expr("EXCLUDED.ding_union_id"),
			"corp_id":       gorm.Expr("COALESCE(NULLIF(EXCLUDED.corp_id, ''), dingtalk_user_bindings.corp_id)"),
			"nickname":      gorm.Expr("COALESCE(NULLIF(EXCLUDED.nickname, ''), dingtalk_user_bindings.nickname)"),
			"avatar":        gorm.Expr("COALESCE(NULLIF(EXCLUDED.avatar, ''), dingtalk_user_bindings.avatar)"),
			"updated_at":    gorm.Expr("EXCLUDED.updated_at"),
		}),
	}).Create(&binding).Error
}

// DeleteByUserID 删除用户钉钉绑定
func (r *dingtalkBindingRepository) DeleteByUserID(ctx context.Context, userID string) (bool, error) {
	result := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Delete(&entity.DingTalkUserBinding{})
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}
