package migrations_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"solvify-agent/pkg/config"
	"solvify-agent/pkg/database"
)

func TestModelLimitsMigrationPostgres(t *testing.T) {
	if os.Getenv("SOLVIFY_MODEL_CONFIG_DB_TEST") != "1" {
		t.Skip("设置SOLVIFY_MODEL_CONFIG_DB_TEST=1验证隔离schema升级")
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
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	name := "model_limits_test_" + fmt.Sprint(time.Now().UnixNano())
	if _, err = conn.ExecContext(ctx, "CREATE SCHEMA "+name); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := conn.ExecContext(cleanup, "DROP SCHEMA "+name+" CASCADE"); err != nil {
			t.Error("临时schema清理失败")
		}
	}()
	if _, err = conn.ExecContext(ctx, "SET search_path TO "+name); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"models", "user_model_configs"} {
		if _, err = conn.ExecContext(ctx, "CREATE TABLE "+table+"(id int PRIMARY KEY, max_context_length int NOT NULL DEFAULT 8192, config jsonb); INSERT INTO "+table+"(id,config) VALUES (1,'{\"temperature\":0.3}')"); err != nil {
			t.Fatal(err)
		}
	}
	migration, err := os.ReadFile("20261013_model_limits.sql")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err = conn.ExecContext(ctx, string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	for _, table := range []string{"models", "user_model_configs"} {
		var window int
		var raw string
		if err = conn.QueryRowContext(ctx, "SELECT max_context_length,config::text FROM "+table+" WHERE id=1").Scan(&window, &raw); err != nil {
			t.Fatal(err)
		}
		if window != 8192 || raw != `{"temperature": 0.3}` {
			t.Fatal("升级改写了旧配置")
		}
		var defaults int
		if err = conn.QueryRowContext(ctx, "SELECT count(*) FROM information_schema.columns WHERE table_schema=$1 AND table_name=$2 AND column_name='max_context_length' AND column_default IS NOT NULL", name, table).Scan(&defaults); err != nil || defaults != 0 {
			t.Fatal("仍存在窗口默认值", err)
		}
		if _, err = conn.ExecContext(ctx, "INSERT INTO "+table+"(id) VALUES (2)"); err == nil {
			t.Fatal("缺失窗口仍被默认填入")
		}
	}
}
