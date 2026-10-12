package repository

import (
	"context"

	"solvify-agent/internal/model/entity"
)

// UserToolConfigRepository 用户工具配置仓储接口
//
// ⚠️ user_tool_configs 带缓存层（tool:config:user:<uid>），所以本接口是这张表的**唯一**
// 写入口：任何级联删除都必须走这里，否则缓存不会失效（审查报告 P0-5）。
type UserToolConfigRepository interface {
	Create(ctx context.Context, config *entity.UserToolConfig) error
	Update(ctx context.Context, config *entity.UserToolConfig) error
	Delete(ctx context.Context, id string) error
	// DeleteByProviderID / DeleteByToolTypeID 是「级联删除」的唯一入口。
	//
	// 返回受影响配置所属的 userID 列表：调用方不该自己再查一遍——「哪些用户的配置没了」
	// 正是这次写操作产生的**事实**，而 tool:config:user:<uid> 的失效必须依赖它。
	// 拆成两步写，迟早出现「删了行却漏了某个 userID」的缺口（审查报告 P0-5）。
	//
	// 仓库包里只有本接口的实现允许写 user_tool_configs 表；想删这张表的其他地方必须改调
	// 这两个方法——守卫见 cached_table_write_guard_test.go。
	DeleteByProviderID(ctx context.Context, providerID string) ([]string, error)
	DeleteByToolTypeID(ctx context.Context, toolTypeID string) ([]string, error)
	GetByID(ctx context.Context, id string) (*entity.UserToolConfig, error)
	GetByUserAndToolType(ctx context.Context, userID, toolTypeID string) (*entity.UserToolConfig, error)
	GetByUserAndProvider(ctx context.Context, userID, providerID string) (*entity.UserToolConfig, error)
	DisableOthersByToolType(ctx context.Context, userID, toolTypeID, exceptID string) error
	ListByUserID(ctx context.Context, userID string) ([]entity.UserToolConfig, error)
	ListEnabledByUserID(ctx context.Context, userID string) ([]entity.UserToolConfig, error)
}
