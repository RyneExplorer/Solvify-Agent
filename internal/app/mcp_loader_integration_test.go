package app

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
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
)

func TestMCPSystemTemplateConcurrentStartupAndReload(t *testing.T) {
	if os.Getenv("SOLVIFY_MCP_DB_TEST") != "1" {
		t.Skip("设置SOLVIFY_MCP_DB_TEST=1验证部署预置并发同步")
	}
	cfg, err := config.Load("../../configs/config.yaml")
	if err != nil {
		t.Fatal("配置加载失败")
	}
	d := cfg.Database.Postgres
	u := url.URL{Scheme: "postgres", User: url.UserPassword(d.Username, d.Password), Host: net.JoinHostPort(d.Host, strconv.Itoa(d.Port)), Path: d.Database}
	admin, err := gorm.Open(postgres.Open(u.String()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("测试数据库不可达")
	}
	pool, _ := admin.DB()
	defer pool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	schema := "mcp_loader_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if err = admin.WithContext(ctx).Exec("CREATE SCHEMA " + schema).Error; err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		if e := admin.WithContext(cleanup).Exec("DROP SCHEMA " + schema + " CASCADE").Error; e != nil {
			t.Error("测试schema清理失败")
		}
	}()
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	scoped, err := gorm.Open(postgres.Open(u.String()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("隔离数据库连接失败")
	}
	scopedPool, _ := scoped.DB()
	defer scopedPool.Close()
	if err = scoped.WithContext(ctx).AutoMigrate(&entity.MCPTemplate{}); err != nil {
		t.Fatal(err)
	}
	a := &App{cfg: cfg, postgresqlDB: scoped}
	srv := config.SystemMCPServerConfig{Name: "search", Transport: "http", URL: "https://example.invalid/mcp"}
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- a.upsertSystemMCPTemplate(ctx, &srv) }()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal("并发启动同步失败", e)
		}
	}
	repo := repository.NewMCPTemplateRepository(scoped)
	rows, err := repo.List(ctx)
	if err != nil || len(rows) != 1 || !rows[0].IsSystem {
		t.Fatal("并发启动产生重复或无效预置", err)
	}
	original := rows[0]
	original.IsEnabled = false
	original.Connection = []byte(`{"transport":"http","url":"https://example.invalid/mcp","admin_timeouts":true,"connect_timeout":90,"call_timeout":120}`)
	if err = repo.Update(ctx, &original); err != nil {
		t.Fatal(err)
	}
	srv.URL = "https://new.invalid/mcp"
	reloaded := &App{cfg: cfg, postgresqlDB: scoped}
	if err = reloaded.upsertSystemMCPTemplate(ctx, &srv); err != nil {
		t.Fatal(err)
	}
	saved, err := repo.GetByID(ctx, original.ID)
	if err != nil {
		t.Fatal(err)
	}
	var connection tool.MCPConfig
	if err = json.Unmarshal(saved.Connection, &connection); err != nil {
		t.Fatal(err)
	}
	if saved.IsEnabled || saved.ToolPrefix != "mcp_search" || connection.URL != srv.URL || !connection.AdminTimeouts || connection.ConnectionTimeout() != 90*time.Second || connection.ToolCallTimeout() != 120*time.Second {
		t.Fatal("重新同步丢失用户绑定身份、管理员时限或停用状态")
	}
	if err = repo.Delete(ctx, saved.ID); err == nil {
		t.Fatal("数据库删除条件未保护部署预置")
	}
}
