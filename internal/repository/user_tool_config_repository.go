package repository

import (
	"context"

	"gorm.io/gorm"

	"solvify-agent/internal/model/entity"
)

type userToolConfigRepository struct {
	db *gorm.DB
}

// NewUserToolConfigRepository 创建用户工具配置仓储实例
func NewUserToolConfigRepository(db *gorm.DB) UserToolConfigRepository {
	return &userToolConfigRepository{db: db}
}

func (r *userToolConfigRepository) Create(ctx context.Context, config *entity.UserToolConfig) error {
	return dbFor(ctx, r.db).Create(config).Error
}

func (r *userToolConfigRepository) Update(ctx context.Context, config *entity.UserToolConfig) error {
	return dbFor(ctx, r.db).Save(config).Error
}

func (r *userToolConfigRepository) Delete(ctx context.Context, id string) error {
	return dbFor(ctx, r.db).Delete(&entity.UserToolConfig{}, "id = ?", id).Error
}

func (r *userToolConfigRepository) GetByID(ctx context.Context, id string) (*entity.UserToolConfig, error) {
	var config entity.UserToolConfig
	err := dbFor(ctx, r.db).
		Preload("ToolType").
		Preload("ToolProvider").
		First(&config, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &config, nil
}

func (r *userToolConfigRepository) GetByUserAndToolType(ctx context.Context, userID, toolTypeID string) (*entity.UserToolConfig, error) {
	var config entity.UserToolConfig
	err := dbFor(ctx, r.db).
		Preload("ToolType").
		Preload("ToolProvider").
		Where("user_id = ? AND tool_type_id = ?", userID, toolTypeID).
		First(&config).Error
	if err != nil {
		return nil, err
	}
	return &config, nil
}

func (r *userToolConfigRepository) GetByUserAndProvider(ctx context.Context, userID, providerID string) (*entity.UserToolConfig, error) {
	var config entity.UserToolConfig
	err := dbFor(ctx, r.db).
		Preload("ToolType").
		Preload("ToolProvider").
		Where("user_id = ? AND provider_id = ?", userID, providerID).
		First(&config).Error
	if err != nil {
		return nil, err
	}
	return &config, nil
}

func (r *userToolConfigRepository) DisableOthersByToolType(ctx context.Context, userID, toolTypeID, exceptID string) error {
	query := dbFor(ctx, r.db).
		Model(&entity.UserToolConfig{}).
		Where("user_id = ? AND tool_type_id = ?", userID, toolTypeID).
		Where("is_enabled = ?", true)
	if exceptID != "" {
		query = query.Where("id <> ?", exceptID)
	}
	return query.Update("is_enabled", false).Error
}

func (r *userToolConfigRepository) ListByUserID(ctx context.Context, userID string) ([]entity.UserToolConfig, error) {
	var configs []entity.UserToolConfig
	err := dbFor(ctx, r.db).
		Preload("ToolType").
		Preload("ToolProvider").
		Where("user_id = ?", userID).
		Find(&configs).Error
	return configs, err
}

func (r *userToolConfigRepository) ListEnabledByUserID(ctx context.Context, userID string) ([]entity.UserToolConfig, error) {
	var configs []entity.UserToolConfig
	err := dbFor(ctx, r.db).
		Preload("ToolType").
		Preload("ToolProvider").
		Where("user_id = ? AND is_enabled = ?", userID, true).
		Find(&configs).Error
	return configs, err
}

// ── 级联删除：user_tool_configs 的写入口 ────────────────────────────────────
//
// 供应商 / 工具类型被删除时要连带删掉用户对它的配置。这两处级联**必须**走本仓库：
// 只有缓存装饰器知道该失效哪些 tool:config:user:<uid>。
// 返回受影响 userID 列表——调用方拿不到这个事实，既无法、也不该自己去失效缓存。

// DeleteByProviderID 删除某供应商下的全部用户配置，返回受影响 userID（去重）。
func (r *userToolConfigRepository) DeleteByProviderID(ctx context.Context, providerID string) ([]string, error) {
	return deleteUserToolConfigsBy(dbFor(ctx, r.db), "provider_id = ?", providerID)
}

// DeleteByToolTypeID 删除某工具类型下的全部用户配置，返回受影响 userID（去重）。
func (r *userToolConfigRepository) DeleteByToolTypeID(ctx context.Context, toolTypeID string) ([]string, error) {
	return deleteUserToolConfigsBy(dbFor(ctx, r.db), "tool_type_id = ?", toolTypeID)
}

// deleteUserToolConfigsBy 是上面两个方法共用的实现：先摘出受影响的 userID，再删行。
//
// 顺序不可颠倒——删完再查就查不到 userID 了，那些用户的缓存将永远失效不了。
// 两条语句都挂在同一个 db 句柄上，因此调用方用 InTx 包起来时它们同处一个事务。
func deleteUserToolConfigsBy(db *gorm.DB, where string, arg any) ([]string, error) {
	var userIDs []string
	if err := db.Model(&entity.UserToolConfig{}).
		Where(where, arg).
		Distinct().
		Pluck("user_id", &userIDs).Error; err != nil {
		return nil, err
	}
	if len(userIDs) == 0 {
		return nil, nil
	}
	if err := db.Where(where, arg).Delete(&entity.UserToolConfig{}).Error; err != nil {
		return nil, err
	}
	return userIDs, nil
}
