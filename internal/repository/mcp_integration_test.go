package repository_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
	"solvify-agent/internal/tool"
	"solvify-agent/pkg/config"
	"solvify-agent/pkg/database"
)

func TestMCPPostgresMigrationAndProcessRead(t *testing.T) {
	if os.Getenv("SOLVIFY_MCP_DB_TEST") != "1" {
		t.Skip("设置SOLVIFY_MCP_DB_TEST=1验证隔离MCP数据库迁移")
	}
	cfg, err := config.Load("../../configs/config.yaml")
	if err != nil {
		t.Fatal("测试配置加载失败")
	}
	db, err := database.OpenPostgreSQL(&cfg.Database.Postgres)
	if err != nil {
		t.Fatal("测试数据库不可达")
	}
	defer database.ClosePostgreSQL(db)
	sqlDB, _ := db.DB()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	schemaName := os.Getenv("SOLVIFY_MCP_TEST_SCHEMA")
	child := schemaName != ""
	if !child {
		schemaName = "mcp_test_" + fmt.Sprint(time.Now().UnixNano())
		if _, err = conn.ExecContext(ctx, "CREATE SCHEMA "+schemaName); err != nil {
			t.Fatal(err)
		}
		defer func() {
			cleanup, c := context.WithTimeout(context.Background(), 10*time.Second)
			defer c()
			if _, e := sqlDB.ExecContext(cleanup, "DROP SCHEMA "+schemaName+" CASCADE"); e != nil {
				t.Error("测试schema清理失败")
			}
		}()
	}
	if !strings.HasPrefix(schemaName, "mcp_test_") {
		t.Fatal("测试schema无效")
	}
	if _, err = conn.ExecContext(ctx, "SET search_path TO "+schemaName); err != nil {
		t.Fatal(err)
	}
	var sqlLogs bytes.Buffer
	isolated, err := gorm.Open(postgres.New(postgres.Config{Conn: conn}), &gorm.Config{Logger: logger.New(log.New(&sqlLogs, "", 0), logger.Config{LogLevel: logger.Error})})
	if err != nil {
		t.Fatal(err)
	}
	repo := repository.NewMCPRepository(isolated)
	const legacyID = "6e3c84d5-afb7-449c-afab-31fb4818f623"
	const userID = "f4d214b3-1720-48c1-ad67-9e048ef10ca2"
	if child {
		c, e := repo.Get(ctx, userID, legacyID)
		if e != nil || c.ID != legacyID || c.DisplayName != "已编辑的搜索" || c.Template == nil || c.Template.Key != "search" {
			t.Fatal("独立进程未读到迁移或更新结果", e)
		}
		if _, e = repo.Get(ctx, "other-user", legacyID); e == nil {
			t.Fatal("仓库跨用户读取")
		}
		cfg, prefix, e := tool.ResolveMCPConnection(c)
		if e != nil || prefix != "mcp_search" || cfg.Headers["Authorization"] != "Bearer test-secret" || cfg.ConnectionTimeout() != 90*time.Second || cfg.ToolCallTimeout() != 120*time.Second {
			t.Fatal("独立进程读回凭据/工具名/时限不一致", e)
		}
		return
	}
	fixture, err := os.ReadFile("testdata/mcp_legacy.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.ExecContext(ctx, string(fixture)); err != nil {
		t.Fatal(err)
	}
	typeID, providerID := uuid.NewString(), uuid.NewString()
	if err = isolated.Exec("INSERT INTO tool_types(id,tool_key,name,is_enabled) VALUES(?,?,?,true)", typeID, "mcp", "MCP").Error; err != nil {
		t.Fatal(err)
	}
	if err = isolated.Exec(`INSERT INTO tool_providers(id,tool_type_id,provider_key,name,provider_type,is_enabled,provider_config,config_schema) VALUES(?,?,?,'搜索','mcp',true,'{"mcp":{"transport":"http","url":"https://search.example/mcp","headers":{"Authorization":"Bearer {{api_key}}"},"admin_timeouts":true,"connect_timeout":90,"call_timeout":120}}','{"type":"object","properties":{"api_key":{"type":"string"}},"required":["api_key"]}')`, providerID, typeID, "search").Error; err != nil {
		t.Fatal(err)
	}
	if err = isolated.Exec(`INSERT INTO user_tool_configs(id,user_id,tool_type_id,provider_id,display_name,config,is_enabled) VALUES(?,?,?,?,'旧搜索','{"api_key":"test-secret"}',true)`, legacyID, userID, typeID, providerID).Error; err != nil {
		t.Fatal(err)
	}
	// 同标识可来自不同旧类型；父类型停用不能被升级重新启用。
	legacyType, legacyProvider := uuid.NewString(), uuid.NewString()
	if err = isolated.Exec(`INSERT INTO tool_types VALUES(?,'web','联网',false)`, legacyType).Error; err != nil {
		t.Fatal(err)
	}
	if err = isolated.Exec(`INSERT INTO tool_providers(id,tool_type_id,provider_key,name,provider_type,provider_config,is_enabled) VALUES(?,?,'search','旧搜索','mcp','{"mcp":{"transport":"http","url":"https://example.invalid"}}',true)`, legacyProvider, legacyType).Error; err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../scripts/migrations/20261017_mcp_connections.sql")
	if err != nil {
		t.Fatal(err)
	}
	run := func() {
		t.Helper()
		if _, e := conn.ExecContext(ctx, string(migration)); e != nil {
			t.Fatal("MCP迁移失败", e)
		}
	}
	// 先升级用户连接，再升级独立模板。
	run()
	templateMigration, e := os.ReadFile("../../scripts/migrations/20261019_mcp_templates.sql")
	if e != nil {
		t.Fatal(e)
	}
	migration = templateMigration
	// 无法迁移的绑定必须使升级整体回滚，不能留下半成品schema。
	invalidProvider, invalidBinding := uuid.NewString(), uuid.NewString()
	if err = isolated.Exec(`INSERT INTO tool_providers(id,tool_type_id,provider_key,name,provider_type,is_enabled) VALUES(?,?,'old-http','旧HTTP','http',true)`, invalidProvider, typeID).Error; err != nil {
		t.Fatal(err)
	}
	if err = isolated.Exec(`INSERT INTO mcp_connections(id,user_id,template_id,display_name) VALUES(?,?,?,'异常绑定')`, invalidBinding, userID, invalidProvider).Error; err != nil {
		t.Fatal(err)
	}
	if _, err = conn.ExecContext(ctx, string(migration)); err == nil {
		t.Fatal("异常绑定迁移应失败")
	}
	if _, err = conn.ExecContext(ctx, "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	var exists bool
	if err = conn.QueryRowContext(ctx, `SELECT to_regclass('mcp_templates') IS NOT NULL`).Scan(&exists); err != nil || exists {
		t.Fatal("失败升级留下半成品模板表", err)
	}
	if err = isolated.Exec(`DELETE FROM mcp_connections WHERE id=?`, invalidBinding).Error; err != nil {
		t.Fatal(err)
	}
	run()
	c, err := repo.Get(ctx, userID, legacyID)
	if err != nil || c.TemplateID == nil || *c.TemplateID != providerID || c.Template.ToolPrefix != "mcp_search" {
		t.Fatal("迁移没有保留MCP身份", err)
	}
	templates := repository.NewMCPTemplateRepository(isolated)
	legacy, err := templates.GetByID(ctx, legacyProvider)
	if err != nil || legacy.Key != "legacy/"+legacyProvider || legacy.IsEnabled || legacy.ToolPrefix != "web_search" {
		t.Fatal("旧类型状态/同名隔离/前缀迁移失败", err)
	}
	// 真实事务回滚及过期写入冲突，不能靠先查后写保证并发安全。
	original, err := templates.GetByID(ctx, providerID)
	if err != nil {
		t.Fatal(err)
	}
	sentinel := errors.New("rollback")
	if e := repository.NewTxManager(isolated).InTx(ctx, func(txCtx context.Context) error {
		v := *original
		v.Name = "应回滚"
		if e := templates.Update(txCtx, &v); e != nil {
			return e
		}
		return sentinel
	}); !errors.Is(e, sentinel) {
		t.Fatal(e)
	}
	after, err := templates.GetByID(ctx, providerID)
	if err != nil || after.Name != original.Name {
		t.Fatal("模板更新未随事务回滚", err)
	}
	if err = templates.UpdateManifest(ctx, providerID, after.Connection, []byte(`[{"name":"echo"}]`)); err != nil {
		t.Fatal(err)
	}
	manifestRead, err := templates.GetByID(ctx, providerID)
	if err != nil || !strings.Contains(string(manifestRead.MCPToolManifest), `"echo"`) {
		t.Fatal("MCP发现清单未持久保存", err)
	}
	stale := *after
	after.Name = "新名称"
	if err = templates.Update(ctx, after); err != nil {
		t.Fatal(err)
	}
	stale.Name = "过期覆盖"
	if err = templates.Update(ctx, &stale); err == nil {
		t.Fatal("过期编辑覆盖并发更新")
	}
	duplicateTemplate := *after
	duplicateTemplate.ID = uuid.NewString()
	if err = templates.Create(ctx, &duplicateTemplate); !errors.Is(err, gorm.ErrDuplicatedKey) {
		t.Fatal("数据库模板唯一约束无效", err)
	}
	c.DisplayName = "已编辑的搜索"
	if err = repo.Update(ctx, c); err != nil {
		t.Fatal(err)
	}
	run()
	c, err = repo.Get(ctx, userID, legacyID)
	if err != nil || c.DisplayName != "已编辑的搜索" {
		t.Fatal("重复迁移覆盖用户编辑", err)
	}
	duplicate := *c
	duplicate.ID = uuid.NewString()
	sqlLogs.Reset()
	if err = repo.Create(ctx, &duplicate); err == nil {
		t.Fatal("数据库未阻止重复绑定")
	}
	if strings.Contains(sqlLogs.String(), "test-secret") {
		t.Fatal("数据库错误日志泄露MCP凭据")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, binary, "-test.run=^TestMCPPostgresMigrationAndProcessRead$", "-test.v")
	command.Env = append(os.Environ(), "SOLVIFY_MCP_TEST_SCHEMA="+schemaName)
	if output, e := command.CombinedOutput(); e != nil {
		t.Fatalf("独立进程读回失败: %v %s", e, output)
	}
	if err = repo.Delete(ctx, userID, legacyID); err != nil {
		t.Fatal(err)
	}
	run()
	if _, err = repo.Get(ctx, userID, legacyID); err == nil {
		t.Fatal("重复升级重新导入已删除连接")
	}
	// 删除模板由新外键原子清理绑定，重复升级不能复活。
	binding := &entity.MCPConnection{ID: uuid.NewString(), UserID: userID, TemplateID: &providerID, DisplayName: "新绑定", Connection: []byte(`{}`), Config: []byte(`{"api_key":"test-secret"}`), IsEnabled: true}
	if err = repo.Create(ctx, binding); err != nil {
		t.Fatal(err)
	}
	if err = templates.Delete(ctx, providerID); err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Get(ctx, userID, binding.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatal("删除模板未级联清理绑定", err)
	}
	run()
	if _, err = templates.GetByID(ctx, providerID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatal("重复升级复活已删模板", err)
	}
	t.Log("迁移保留ID、重复升级、数据库唯一约束、隔离归属、独立进程读取和删除防重导入均通过")
}
