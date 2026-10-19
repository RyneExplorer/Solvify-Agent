package repository_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
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
	"solvify-agent/pkg/config"
)

func TestMCPToolSwitchPostgresConcurrencyAndPersistence(t *testing.T) {
	if os.Getenv("SOLVIFY_MCP_DB_TEST") != "1" {
		t.Skip("设置SOLVIFY_MCP_DB_TEST=1验证真实数据库MCP工具开关")
	}
	cfg, err := config.Load("../../configs/config.yaml")
	if err != nil {
		t.Fatal("读取配置失败")
	}
	d := cfg.Database.Postgres
	u := url.URL{Scheme: "postgres", User: url.UserPassword(d.Username, d.Password), Host: net.JoinHostPort(d.Host, strconv.Itoa(d.Port)), Path: d.Database}
	admin, err := gorm.Open(postgres.Open(u.String()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("数据库不可达")
	}
	adminPool, _ := admin.DB()
	defer adminPool.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	schema := os.Getenv("SOLVIFY_MCP_SWITCH_SCHEMA")
	child := schema != ""
	if !child {
		schema = "mcp_switch_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		if err = admin.WithContext(ctx).Exec("CREATE SCHEMA " + schema).Error; err != nil {
			t.Fatal(err)
		}
		defer func() {
			cleanup, c := context.WithTimeout(context.Background(), 10*time.Second)
			defer c()
			if e := admin.WithContext(cleanup).Exec("DROP SCHEMA " + schema + " CASCADE").Error; e != nil {
				t.Error("清理隔离schema失败")
			}
		}()
	}
	if !strings.HasPrefix(schema, "mcp_switch_test_") || strings.ContainsAny(schema, " ;\"'") {
		t.Fatal("隔离schema无效")
	}
	query := u.Query()
	query.Set("search_path", schema)
	u.RawQuery = query.Encode()
	db, err := gorm.Open(postgres.Open(u.String()), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("连接隔离schema失败")
	}
	pool, _ := db.DB()
	defer pool.Close()
	repo := repository.NewMCPRepository(db)
	if child {
		c, e := repo.Get(ctx, "owner", "switch-binding")
		if e != nil {
			t.Fatal("独立进程读回失败")
		}
		var fields struct {
			APIKey   string   `json:"api_key"`
			Disabled []string `json:"disabled_tools"`
		}
		if json.Unmarshal(c.Config, &fields) != nil || fields.APIKey != "kept-secret" || len(fields.Disabled) != 8 {
			t.Fatal("工具偏好或凭据未持久保存")
		}
		return
	}
	if err = db.WithContext(ctx).AutoMigrate(&entity.MCPTemplate{}, &entity.MCPConnection{}); err != nil {
		t.Fatal(err)
	}
	c := &entity.MCPConnection{ID: "switch-binding", UserID: "owner", DisplayName: "开关测试", IsEnabled: true, Connection: []byte(`{"transport":"http","url":"https://example.invalid"}`), Config: []byte(`{"api_key":"kept-secret"}`)}
	if err = repo.Create(ctx, c); err != nil {
		t.Fatal(err)
	}
	stale := *c
	var wg sync.WaitGroup
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			failures <- repo.SetToolEnabled(ctx, "owner", c.ID, fmt.Sprintf("tool_%d", i), false)
		}(i)
	}
	wg.Wait()
	close(failures)
	for e := range failures {
		if e != nil {
			t.Fatal(e)
		}
	}
	stale.DisplayName = "过期编辑"
	if err = repo.Update(ctx, &stale); !errors.Is(err, repository.ErrMCPConnectionChanged) {
		t.Fatal("过期表单覆盖了并发开关", err)
	}
	if err = repo.SetToolEnabled(ctx, "other", c.ID, "tool_0", true); err == nil {
		t.Fatal("跨用户修改工具开关")
	}
	rollback := errors.New("回滚探针")
	err = repository.NewTxManager(db).InTx(ctx, func(txCtx context.Context) error {
		if e := repo.SetToolEnabled(txCtx, "owner", c.ID, "tool_0", true); e != nil {
			return e
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal("工具开关未参加事务", err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, binary, "-test.run=^TestMCPToolSwitchPostgresConcurrencyAndPersistence$", "-test.v")
	command.Env = append(os.Environ(), "SOLVIFY_MCP_SWITCH_SCHEMA="+schema)
	if output, e := command.CombinedOutput(); e != nil {
		t.Fatalf("独立进程验证失败：%v %s", e, output)
	}
	// 原子启用移除指定项，保留其他工具与凭据；重复停用不能产生重复项。
	if err = repo.SetToolEnabled(ctx, "owner", c.ID, "tool_0", true); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = repo.SetToolEnabled(ctx, "owner", c.ID, "tool_0", false); err != nil {
			t.Fatal(err)
		}
	}
	updated, err := repo.Get(ctx, "owner", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	var values struct {
		Disabled []string `json:"disabled_tools"`
	}
	_ = json.Unmarshal(updated.Config, &values)
	if len(values.Disabled) != 8 {
		t.Fatal("重复操作产生重复开关")
	}
	updated.IsEnabled = false
	if err = repo.Update(ctx, updated); err != nil {
		t.Fatal("停用连接失败", err)
	}
	updated.IsEnabled = true
	if err = repo.Update(ctx, updated); err != nil {
		t.Fatal("返回的数据库版本不可用于后续更新", err)
	}
	names := make([]string, 128)
	for i := range names {
		names[i] = fmt.Sprintf("limit_%d", i)
	}
	updated.Config, _ = json.Marshal(map[string]interface{}{"api_key": "kept-secret", "disabled_tools": names})
	if err = repo.Update(ctx, updated); err != nil {
		t.Fatal(err)
	}
	if err = repo.SetToolEnabled(ctx, "owner", c.ID, "over_limit", false); !errors.Is(err, repository.ErrMCPToolStateChanged) {
		t.Fatal("数据库未限制停用名单大小", err)
	}
	if err = repo.SetToolEnabled(ctx, "owner", c.ID, "limit_0", false); err != nil {
		t.Fatal("达到上限时重复停用被拒绝", err)
	}
	if err = repo.SetToolEnabled(ctx, "owner", c.ID, "limit_0", true); err != nil {
		t.Fatal("达到上限时不能重新启用", err)
	}
	t.Log("并发8工具无丢失、凭据保留、过期编辑拒绝、事务回滚、跨用户隔离和独立进程持久化通过")
}
