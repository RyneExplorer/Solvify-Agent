package app

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"gorm.io/datatypes"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"

	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/tool"
	"solvify-agent/pkg/config"
	"solvify-agent/pkg/logger"
)

func (a *App) loadSystemMCPServers(ctx context.Context) error {
	for i := range a.cfg.Tools.MCPServers {
		if err := a.upsertSystemMCPTemplate(ctx, &a.cfg.Tools.MCPServers[i]); err != nil {
			return err
		}
	}
	return nil
}
func (a *App) upsertSystemMCPTemplate(ctx context.Context, srv *config.SystemMCPServerConfig) error {
	return a.postgresqlDB.Session(&gorm.Session{Logger: a.postgresqlDB.Logger.LogMode(gormlogger.Silent)}).WithContext(ctx).Transaction(func(db *gorm.DB) error {
		// 串行化同名模板的启动同步，唯一约束负责最终防重。
		if err := db.Exec("SELECT pg_advisory_xact_lock(hashtextextended(?, 0))", "mcp-template:"+srv.Name).Error; err != nil {
			return err
		}
		var t entity.MCPTemplate
		err := db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("template_key = ?", srv.Name).First(&t).Error
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		exists := err == nil
		raw, err := systemMCPConnection(srv, t.Connection)
		if err != nil {
			return err
		}
		schema, err := json.Marshal(srv.ConfigSchema)
		if err != nil {
			return err
		}
		if !exists {
			t = entity.MCPTemplate{Key: srv.Name, ToolPrefix: tool.MCPToolPrefix("mcp", srv.Name), IsEnabled: true}
		}
		t.Name = srv.Name
		t.Description = srv.Description
		t.Connection = datatypes.JSON(raw)
		t.ConfigSchema = datatypes.JSON(schema)
		t.IsSystem = true
		if exists {
			return db.Model(&entity.MCPTemplate{}).Where("id = ?", t.ID).Select("name", "description", "connection", "config_schema", "is_system", "updated_at").Updates(&t).Error
		}
		return db.Create(&t).Error
	})
}

// YAML管理连接信息；已由管理员保存的超时在重启同步时保留。
func systemMCPConnection(srv *config.SystemMCPServerConfig, existing []byte) ([]byte, error) {
	cfg := tool.MCPConfig{Transport: srv.Transport, URL: srv.URL, Headers: srv.Headers, Timeout: srv.Timeout, ConnectTimeout: srv.ConnectTimeout, CallTimeout: srv.CallTimeout}
	if err := cfg.ValidateTimeouts(); err != nil {
		return nil, err
	}
	if len(existing) > 0 {
		var stored tool.MCPConfig
		if err := json.Unmarshal(existing, &stored); err != nil {
			return nil, err
		}
		if stored.AdminTimeouts {
			cfg.Timeout = 0
			cfg.ConnectTimeout = stored.ConnectTimeout
			cfg.CallTimeout = stored.CallTimeout
			cfg.AdminTimeouts = true
		}
	}
	if err := cfg.ValidateTimeouts(); err != nil {
		return nil, err
	}
	return json.Marshal(cfg)
}
func (a *App) loadSystemMCPServersWithTimeout() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := a.loadSystemMCPServers(ctx); err != nil {
		logger.Error("加载部署MCP预置失败，请检查配置和数据库")
	}
}
