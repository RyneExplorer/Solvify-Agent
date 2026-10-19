package app

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlog "gorm.io/gorm/logger"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
	"solvify-agent/internal/service"
	"solvify-agent/pkg/config"
)

func TestMessageEmbeddingAppPostgresLifecycle(t *testing.T) {
	if os.Getenv("SOLVIFY_EMBEDDING_DB_TEST") != "1" {
		t.Skip("需显式启用隔离数据库验证")
	}
	cfg, err := config.Load("../../configs/config.yaml")
	if err != nil {
		t.Fatal("测试配置加载失败")
	}
	d := cfg.Database.Postgres
	address := url.URL{Scheme: "postgres", User: url.UserPassword(d.Username, d.Password), Host: net.JoinHostPort(d.Host, strconv.Itoa(d.Port)), Path: d.Database}
	pgcfg, err := pgx.ParseConfig(address.String())
	if err != nil {
		t.Fatal("测试数据库配置无效")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	schema := "embedding_app_test_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	admin := stdlib.OpenDB(*pgcfg)
	defer admin.Close()
	if _, err = admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		if _, err := admin.ExecContext(cleanup, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error("隔离schema清理失败", err)
		}
	}()
	pgcfg.RuntimeParams["search_path"] = schema + ",public"
	pool := stdlib.OpenDB(*pgcfg)
	defer pool.Close()
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: pool}), &gorm.Config{Logger: gormlog.Default.LogMode(gormlog.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := os.ReadFile("../../scripts/init_knowledge_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.ReplaceAll(string(baseline), `\set ON_ERROR_STOP on`, "")
	sql = strings.ReplaceAll(sql, `"public".`, schema+".")
	sql = strings.ReplaceAll(sql, "public.", schema+".")
	// pgvector扩展类型位于public，业务对象使用测试schema。
	sql = strings.ReplaceAll(sql, schema+`."vector"`, `public."vector"`)
	sql = strings.ReplaceAll(sql, schema+`."vector_cosine_ops"`, `public."vector_cosine_ops"`)
	sql = strings.ReplaceAll(sql, schema+`.vector`, `public.vector`)
	if _, err = pool.ExecContext(ctx, sql); err != nil {
		t.Fatal("新库初始化基线失败", err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		vector := make([]float64, 1024)
		vector[0] = .1
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": vector}}, "model": "test"})
	}))
	defer server.Close()
	cfg.Embedding.BaseURL = server.URL + "/v1"
	cfg.Embedding.APIKey = "test"
	cfg.Embedding.Model = "test"
	cfg.Embedding.Dimension = 1024
	cfg.Embedding.MessageJobs.PollSeconds = 1
	cfg.Database.Postgres.EnablePGVector = true
	cfg.Tools.MCPServers = nil
	cfg.Observability = config.ObservabilityConfig{}
	a := &App{cfg: cfg, postgresqlDB: db}
	defer func() {
		a.stopMessageEmbeddingWorker()
		a.stopContextCleanup()
		if a.mcpClientPool != nil {
			_ = a.mcpClientPool.Close()
		}
	}()
	repo := repository.NewChatMessageRepository(db)
	oldID := uuid.NewString()
	if err = repo.Create(ctx, &entity.ChatMessage{ID: oldID, SessionID: uuid.NewString(), Role: "user", Content: "旧消息"}); err != nil {
		t.Fatal(err)
	}
	if err = a.initDependencies(); err != nil {
		t.Fatal("应用依赖初始化失败", err)
	}
	// 超过一个轮询周期，观察真正运行的Worker；历史none不应触发HTTP。
	select {
	case <-time.After(1200 * time.Millisecond):
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if calls.Load() != 0 {
		t.Fatal("启动调用了历史消息向量化")
	}
	n, err := service.NewMessageEmbeddingBackfiller(repository.NewMessageEmbeddingRepository(db)).Enqueue(ctx, 1, false)
	if err != nil || n != 1 {
		t.Fatal("手动排队失败", n, err)
	}
	ticker := time.NewTicker(30 * time.Millisecond)
	defer ticker.Stop()
	for {
		row, e := repo.FindByID(ctx, oldID)
		if e != nil {
			t.Fatal(e)
		}
		if row.EmbeddingStatus == "succeeded" {
			if len(row.Embedding) != 1024 || row.EmbeddingAttempts != 1 {
				t.Fatal("结果或次数不正确")
			}
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("应用Worker未提交任务")
		}
	}
	a.stopMessageEmbeddingWorker()
	if calls.Load() != 1 {
		t.Fatal("后台重复请求", calls.Load())
	}
	fmt.Println("新库初始化、App装配、启动不处理旧消息、手动登记后真实HTTP向量写入、停止等待通过")
}
