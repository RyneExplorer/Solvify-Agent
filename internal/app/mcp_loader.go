package app

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"

	"solvify-agent/internal/model/entity"
	"solvify-agent/pkg/config"
	"solvify-agent/pkg/logger"
)

// mcpToolKey 系统预置 MCP 服务的 ToolType.ToolKey
const mcpToolKey = "mcp"

// loadSystemMCPServers 从配置文件加载系统预置 MCP 服务器到数据库
// 启动时调用：确保 config.yaml 中配置的 MCP 服务器在数据库中存在（is_system=true）
func (a *App) loadSystemMCPServers(ctx context.Context) error {
	mcpServers := a.cfg.Tools.MCPServers
	if len(mcpServers) == 0 {
		return nil
	}

	logger.Infof("[MCPLoader] 开始加载系统预置 MCP 服务器: count=%d", len(mcpServers))

	// 1. 确保存在 "mcp" ToolType
	mcpToolType, err := a.ensureMCPToolType(ctx)
	if err != nil {
		return err
	}

	// 2. 遍历配置中的 MCP 服务器，同步到数据库
	for i := range mcpServers {
		srv := &mcpServers[i]
		if err := a.upsertSystemMCPProvider(ctx, mcpToolType.ID, srv); err != nil {
			logger.Warnf("[MCPLoader] 加载系统预置 MCP 服务器失败，跳过: name=%s, err=%v", srv.Name, err)
			continue
		}
	}

	logger.Info("[MCPLoader] 系统预置 MCP 服务器加载完成")
	return nil
}

// ensureMCPToolType 确保数据库中存在 "mcp" ToolType，不存在则创建
func (a *App) ensureMCPToolType(ctx context.Context) (*entity.ToolType, error) {
	db := a.postgresqlDB.WithContext(ctx)

	var toolType entity.ToolType
	err := db.Where("tool_key = ?", mcpToolKey).First(&toolType).Error
	if err == nil {
		// 已存在
		return &toolType, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	// 创建 MCP ToolType
	toolType = entity.ToolType{
		Name:          "MCP 工具集",
		ToolKey:       mcpToolKey,
		Description:   "通过 MCP 协议接入的外部工具服务器，一个 MCP Server 可提供多个工具",
		ExecutionMode: "sync",
		IsEnabled:     true,
	}
	if err := db.Create(&toolType).Error; err != nil {
		return nil, err
	}
	logger.Infof("[MCPLoader] 创建 MCP ToolType 成功: id=%s", toolType.ID)
	return &toolType, nil
}

// upsertSystemMCPProvider 创建或更新系统预置 MCP Provider
func (a *App) upsertSystemMCPProvider(ctx context.Context, toolTypeID string, srv *config.SystemMCPServerConfig) error {
	db := a.postgresqlDB.WithContext(ctx)

	// 构造 provider_config（MCP 配置）
	mcpCfg := map[string]interface{}{
		"mcp": map[string]interface{}{
			"transport": srv.Transport,
			"command":   srv.Command,
			"args":      srv.Args,
			"env":       srv.Env,
			"url":       srv.URL,
			"headers":   srv.Headers,
			"timeout":   srv.Timeout,
		},
	}
	providerConfigJSON, _ := json.Marshal(mcpCfg)

	// 查找是否已存在同 provider_key 的记录
	var provider entity.ToolProvider
	err := db.Where("tool_type_id = ? AND provider_key = ?", toolTypeID, srv.Name).First(&provider).Error
	if err == nil {
		// 已存在：仅更新配置（保留 is_system 标记）
		provider.Name = srv.Name
		provider.Description = srv.Description
		provider.ProviderConfig = datatypes.JSON(providerConfigJSON)
		provider.IsSystem = true
		provider.IsEnabled = true
		if err := db.Save(&provider).Error; err != nil {
			return err
		}
		logger.Infof("[MCPLoader] 更新系统预置 MCP 服务器: name=%s", srv.Name)
		return nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	// 不存在：创建
	provider = entity.ToolProvider{
		ToolTypeID:     toolTypeID,
		ProviderKey:    srv.Name,
		Name:           srv.Name,
		Description:    srv.Description,
		ProviderType:   "mcp",
		ProviderConfig: datatypes.JSON(providerConfigJSON),
		IsEnabled:      true,
		IsSystem:       true,
	}
	if err := db.Create(&provider).Error; err != nil {
		return err
	}
	logger.Infof("[MCPLoader] 创建系统预置 MCP 服务器: name=%s, id=%s", srv.Name, provider.ID)
	return nil
}

// loadSystemMCPServersWithTimeout 带超时的加载包装
func (a *App) loadSystemMCPServersWithTimeout() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := a.loadSystemMCPServers(ctx); err != nil {
		logger.Errorf("[MCPLoader] 加载系统预置 MCP 服务器失败: %v", err)
	}
}
