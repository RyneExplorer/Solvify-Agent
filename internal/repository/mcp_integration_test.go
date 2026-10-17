package repository

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"os"
	"os/exec"
	"solvify-agent/internal/model/entity"
	"solvify-agent/pkg/config"
	"solvify-agent/pkg/database"
	"strings"
	"testing"
	"time"
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
	isolated, err := gorm.Open(postgres.New(postgres.Config{Conn: conn}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	repo := NewMCPRepository(isolated)
	const legacyID = "6e3c84d5-afb7-449c-afab-31fb4818f623"
	const userID = "f4d214b3-1720-48c1-ad67-9e048ef10ca2"
	if child {
		c, e := repo.Get(ctx, userID, legacyID)
		if e != nil || c.ID != legacyID || c.DisplayName != "已编辑的搜索" || c.Template == nil || c.Template.ProviderType != "mcp" {
			t.Fatal("独立进程未读到迁移或更新结果", e)
		}
		if _, e = repo.Get(ctx, "other-user", legacyID); e == nil {
			t.Fatal("仓库跨用户读取")
		}
		return
	}
	if err = isolated.AutoMigrate(&entity.ToolType{}, &entity.ToolProvider{}, &entity.UserToolConfig{}); err != nil {
		t.Fatal(err)
	}
	typeID, providerID := uuid.NewString(), uuid.NewString()
	if err = isolated.Create(&entity.ToolType{ID: typeID, Name: "MCP", ToolKey: "mcp", IsEnabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err = isolated.Create(&entity.ToolProvider{ID: providerID, ToolTypeID: typeID, ProviderKey: "search", Name: "搜索", ProviderType: "mcp", IsEnabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err = isolated.WithContext(ctx).Create(&entity.UserToolConfig{ID: legacyID, UserID: userID, ToolTypeID: typeID, ProviderID: providerID, DisplayName: "旧搜索", Config: []byte(`{"api_key":"test-secret"}`), IsEnabled: true}).Error; err != nil {
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
	run()
	c, err := repo.Get(ctx, userID, legacyID)
	if err != nil || c.TemplateID == nil || *c.TemplateID != providerID {
		t.Fatal("迁移没有保留MCP身份", err)
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
	if err = repo.Create(ctx, &duplicate); err == nil {
		t.Fatal("数据库未阻止重复绑定")
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
	t.Log("迁移保留ID、重复升级、数据库唯一约束、隔离归属、独立进程读取和删除防重导入均通过")
}
