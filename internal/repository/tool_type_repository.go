package repository

import (
	"context"

	"gorm.io/gorm"

	"solvify-agent/internal/model/entity"
)

type toolTypeRepository struct {
	db *gorm.DB
}

// NewToolTypeRepository 创建工具类型仓储实例
func NewToolTypeRepository(db *gorm.DB) ToolTypeRepository {
	return &toolTypeRepository{db: db}
}

func (r *toolTypeRepository) Create(ctx context.Context, toolType *entity.ToolType) error {
	return dbFor(ctx, r.db).Create(toolType).Error
}

func (r *toolTypeRepository) Update(ctx context.Context, toolType *entity.ToolType) error {
	return dbFor(ctx, r.db).Save(toolType).Error
}

// Delete 只删 tool_types 自己这一行。
//
// 此前它在一个事务里连删 user_tool_configs + tool_providers + tool_types 三张表，其中
// user_tool_configs 带缓存却没有跟着失效（审查报告 P0-5）。级联删除已上移到
// tool_type_service.Delete：由 service 用同一个事务编排三处写，写用户配置表的那一步
// 必须经由 UserToolConfigRepository（唯一会失效缓存的地方）。
func (r *toolTypeRepository) Delete(ctx context.Context, id string) error {
	return dbFor(ctx, r.db).Delete(&entity.ToolType{}, "id = ?", id).Error
}

func (r *toolTypeRepository) GetByID(ctx context.Context, id string) (*entity.ToolType, error) {
	var toolType entity.ToolType
	err := dbFor(ctx, r.db).First(&toolType, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &toolType, nil
}

func (r *toolTypeRepository) GetByKey(ctx context.Context, toolKey string) (*entity.ToolType, error) {
	var toolType entity.ToolType
	err := dbFor(ctx, r.db).First(&toolType, "tool_key = ?", toolKey).Error
	if err != nil {
		return nil, err
	}
	return &toolType, nil
}

func (r *toolTypeRepository) List(ctx context.Context) ([]entity.ToolType, error) {
	var toolTypes []entity.ToolType
	err := dbFor(ctx, r.db).Order("name ASC, id").Find(&toolTypes).Error
	return toolTypes, err
}

func (r *toolTypeRepository) ListEnabled(ctx context.Context) ([]entity.ToolType, error) {
	var toolTypes []entity.ToolType
	err := dbFor(ctx, r.db).Where("is_enabled = ?", true).Order("name ASC, id").Find(&toolTypes).Error
	return toolTypes, err
}

func (r *toolTypeRepository) ExistsByKey(ctx context.Context, toolKey string) (bool, error) {
	var count int64
	err := dbFor(ctx, r.db).Model(&entity.ToolType{}).Where("tool_key = ?", toolKey).Count(&count).Error
	return count > 0, err
}

// GetProviderCounts 获取所有工具类型的供应商数量
func (r *toolTypeRepository) GetProviderCounts(ctx context.Context) (map[string]int, error) {
	type result struct {
		ToolTypeID string
		Count      int
	}
	var results []result

	err := dbFor(ctx, r.db).
		Model(&entity.ToolProvider{}).
		Select("tool_type_id, count(*) as count").
		Group("tool_type_id").
		Find(&results).Error
	if err != nil {
		return nil, err
	}

	counts := make(map[string]int, len(results))
	for _, r := range results {
		counts[r.ToolTypeID] = r.Count
	}
	return counts, nil
}
