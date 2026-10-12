package repository

import (
	"context"

	"gorm.io/datatypes"
	"gorm.io/gorm"

	"solvify-agent/internal/model/entity"
)

type toolProviderRepository struct {
	db *gorm.DB
}

// NewToolProviderRepository 创建工具供应商仓储实例
func NewToolProviderRepository(db *gorm.DB) ToolProviderRepository {
	return &toolProviderRepository{db: db}
}

func (r *toolProviderRepository) Create(ctx context.Context, provider *entity.ToolProvider) error {
	return dbFor(ctx, r.db).Create(provider).Error
}

func (r *toolProviderRepository) Update(ctx context.Context, provider *entity.ToolProvider) error {
	return dbFor(ctx, r.db).Save(provider).Error
}

func (r *toolProviderRepository) Delete(ctx context.Context, id string) error {
	return dbFor(ctx, r.db).Delete(&entity.ToolProvider{}, "id = ?", id).Error
}

func (r *toolProviderRepository) UpdateMCPToolManifest(ctx context.Context, id string, manifest datatypes.JSON) error {
	return dbFor(ctx, r.db).Model(&entity.ToolProvider{}).
		Where("id = ?", id).
		Update("mcp_tool_manifest", manifest).Error
}

// DeleteByToolTypeID 删除某工具类型下的全部供应商。
//
// 这里**不再**顺手删 user_tool_configs（此前是）。原因：那张表带缓存，写它的地方必须
// 同时失效 tool:config:user:<uid>，而唯一做这件事的是 UserToolConfigRepository。
// 现在级联由 service 在同一个事务里编排两处删除（见 tool_provider_service.Delete）。
// 守卫见 cached_table_write_guard_test.go。
func (r *toolProviderRepository) DeleteByToolTypeID(ctx context.Context, toolTypeID string) error {
	return dbFor(ctx, r.db).Delete(&entity.ToolProvider{}, "tool_type_id = ?", toolTypeID).Error
}

func (r *toolProviderRepository) GetByID(ctx context.Context, id string) (*entity.ToolProvider, error) {
	var provider entity.ToolProvider
	err := dbFor(ctx, r.db).First(&provider, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &provider, nil
}

func (r *toolProviderRepository) GetByProviderKey(ctx context.Context, toolTypeID, providerKey string) (*entity.ToolProvider, error) {
	var provider entity.ToolProvider
	err := dbFor(ctx, r.db).
		Where("tool_type_id = ? AND provider_key = ?", toolTypeID, providerKey).
		First(&provider).Error
	if err != nil {
		return nil, err
	}
	return &provider, nil
}

func (r *toolProviderRepository) ListByToolTypeID(ctx context.Context, toolTypeID string) ([]entity.ToolProvider, error) {
	var providers []entity.ToolProvider
	err := dbFor(ctx, r.db).Where("tool_type_id = ?", toolTypeID).Order("name ASC, id").Find(&providers).Error
	return providers, err
}

func (r *toolProviderRepository) ListEnabledByToolTypeID(ctx context.Context, toolTypeID string) ([]entity.ToolProvider, error) {
	var providers []entity.ToolProvider
	err := dbFor(ctx, r.db).Where("tool_type_id = ? AND is_enabled = ?", toolTypeID, true).Order("name ASC, id").Find(&providers).Error
	return providers, err
}

func (r *toolProviderRepository) ExistsByKey(ctx context.Context, toolTypeID, providerKey string) (bool, error) {
	var count int64
	err := dbFor(ctx, r.db).Model(&entity.ToolProvider{}).
		Where("tool_type_id = ? AND provider_key = ?", toolTypeID, providerKey).
		Count(&count).Error
	return count > 0, err
}

// MCPServerRow 管理后台 MCP 服务器聚合行：供应商 + 所属工具类型的名称与 key
type MCPServerRow struct {
	entity.ToolProvider
	ToolTypeName string
	ToolTypeKey  string
}

// ListMCPProviders 列出全部 MCP 供应商（provider_type = 'mcp'），并带出所属工具类型信息
func (r *toolProviderRepository) ListMCPProviders(ctx context.Context) ([]MCPServerRow, error) {
	db := dbFor(ctx, r.db)
	var providers []entity.ToolProvider
	if err := db.Where("provider_type = ?", "mcp").Order("name ASC, id").Find(&providers).Error; err != nil {
		return nil, err
	}
	if len(providers) == 0 {
		return nil, nil
	}
	typeIDs := make([]string, 0, len(providers))
	for _, p := range providers {
		typeIDs = append(typeIDs, p.ToolTypeID)
	}
	var types []entity.ToolType
	if err := db.Where("id IN ?", typeIDs).Find(&types).Error; err != nil {
		return nil, err
	}
	nameByID := make(map[string]entity.ToolType, len(types))
	for _, t := range types {
		nameByID[t.ID] = t
	}
	rows := make([]MCPServerRow, 0, len(providers))
	for _, p := range providers {
		tt := nameByID[p.ToolTypeID]
		rows = append(rows, MCPServerRow{ToolProvider: p, ToolTypeName: tt.Name, ToolTypeKey: tt.ToolKey})
	}
	return rows, nil
}

// CountByToolTypeID 统计某工具类型下的供应商数量
func (r *toolProviderRepository) CountByToolTypeID(ctx context.Context, toolTypeID string) (int64, error) {
	var count int64
	err := dbFor(ctx, r.db).Model(&entity.ToolProvider{}).
		Where("tool_type_id = ?", toolTypeID).
		Count(&count).Error
	return count, err
}
