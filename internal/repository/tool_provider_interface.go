package repository

import (
	"context"

	"gorm.io/datatypes"

	"solvify-agent/internal/model/entity"
)

// ToolProviderRepository 工具供应商仓储接口
type ToolProviderRepository interface {
	Create(ctx context.Context, provider *entity.ToolProvider) error
	Update(ctx context.Context, provider *entity.ToolProvider) error
	Delete(ctx context.Context, id string) error
	// UpdateMCPToolManifest 只更新某 MCP 供应商探测到的工具清单，避免全表 Save
	UpdateMCPToolManifest(ctx context.Context, id string, manifest datatypes.JSON) error
	// DeleteByToolTypeID 级联删除某工具类型下的所有供应商（删工具类型时用）。
	DeleteByToolTypeID(ctx context.Context, toolTypeID string) error
	GetByID(ctx context.Context, id string) (*entity.ToolProvider, error)
	GetByProviderKey(ctx context.Context, toolTypeID, providerKey string) (*entity.ToolProvider, error)
	ListByToolTypeID(ctx context.Context, toolTypeID string) ([]entity.ToolProvider, error)
	ListEnabledByToolTypeID(ctx context.Context, toolTypeID string) ([]entity.ToolProvider, error)
	ExistsByKey(ctx context.Context, toolTypeID, providerKey string) (bool, error)
	// ListMCPProviders 列出全部 MCP 供应商（含所属工具类型信息）
	ListMCPProviders(ctx context.Context) ([]MCPServerRow, error)
	// CountByToolTypeID 统计某工具类型下的供应商数量
	CountByToolTypeID(ctx context.Context, toolTypeID string) (int64, error)
}
