package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"solvify-agent/internal/model/entity"
	"solvify-agent/pkg/config"
	"solvify-agent/pkg/database"
)

// Opt-in: all writes are isolated in a newly created schema; application tables are untouched.
func TestContextArtifactPostgresMigrationQuotaAndCleanup(t *testing.T) {
	if os.Getenv("SOLVIFY_CONTEXT_DB_TEST") != "1" {
		t.Skip("设置 SOLVIFY_CONTEXT_DB_TEST=1 验证临时 PostgreSQL schema")
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
	conn, err := sqlDB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	schemaName := "context_test_" + fmt.Sprint(time.Now().UnixNano())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err = conn.ExecContext(ctx, "CREATE SCHEMA "+schemaName); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := sqlDB.ExecContext(cleanup, "DROP SCHEMA "+schemaName+" CASCADE"); err != nil {
			t.Error("临时schema清理失败")
		}
	}()
	if _, err = conn.ExecContext(ctx, "SET search_path TO "+schemaName); err != nil {
		t.Fatal(err)
	}
	testDB, err := gorm.Open(postgres.New(postgres.Config{Conn: conn}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err = testDB.Exec("CREATE TABLE chat_sessions(id uuid PRIMARY KEY,user_id uuid NOT NULL); CREATE TABLE agent_checkpoints(id text PRIMARY KEY,session_id uuid NOT NULL,expired_at timestamptz NOT NULL)").Error; err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err = database.EnsureContextArtifactSchema(ctx, testDB); err != nil {
			t.Fatal(err)
		}
	}
	owner, session := uuid.NewString(), uuid.NewString()
	if err = testDB.Exec("INSERT INTO chat_sessions VALUES(?,?)", session, owner).Error; err != nil {
		t.Fatal(err)
	}
	newArtifact := func(id string) *entity.ContextArtifact {
		content := "0123456789"
		hash := sha256.Sum256([]byte(content))
		return &entity.ContextArtifact{ID: id, UserID: owner, SessionID: session, RunID: "run", AgentID: "agent", TaskID: "task", ToolCallID: id, Content: content, Hash: hex.EncodeToString(hash[:]), Bytes: 10, Version: 1, CreatedAt: time.Now().Add(-3 * time.Hour), ExpiredAt: time.Now().Add(-time.Hour)}
	}
	repo := NewContextArtifactRepository(testDB)
	limits := ArtifactLimits{10, 30, 30}
	var successes atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	// Independent connections and transaction locks exercise the cross-process database quota boundary.
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, err := sqlDB.Conn(ctx)
			if err != nil {
				errs <- err
				return
			}
			defer c.Close()
			if _, err = c.ExecContext(ctx, "SET search_path TO "+schemaName); err != nil {
				errs <- err
				return
			}
			defer c.ExecContext(context.Background(), "RESET search_path")
			isolated, err := gorm.Open(postgres.New(postgres.Config{Conn: c}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			if err != nil {
				errs <- err
				return
			}
			err = NewContextArtifactRepository(isolated).SaveImmutable(ctx, newArtifact(fmt.Sprintf("artifact-%d", i)), limits)
			if err == nil {
				successes.Add(1)
			} else if !errors.Is(err, ErrArtifactQuota) {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if successes.Load() != 3 {
		t.Fatalf("原子配额成功数=%d，期望3", successes.Load())
	}
	var saved entity.ContextArtifact
	if err = testDB.Take(&saved).Error; err != nil {
		t.Fatal(err)
	}
	if err = repo.SaveImmutable(ctx, &saved, limits); err != nil {
		t.Fatal("幂等保存失败", err)
	}
	scope := ArtifactScope{owner, session, "run", "agent", "task"}
	if _, err = repo.FindScoped(ctx, scope, saved.ID); !errors.Is(err, ErrArtifactUnavailable) {
		t.Fatal("过期产物未拒绝")
	}
	if err = testDB.Exec("INSERT INTO agent_checkpoints VALUES('active',?,?)", session, time.Now().Add(time.Hour)).Error; err != nil {
		t.Fatal(err)
	}
	if _, err = repo.FindScoped(ctx, scope, saved.ID); err != nil {
		t.Fatal("有效checkpoint原文不可读", err)
	}
	if n, err := repo.DeleteExpired(ctx, time.Now()); err != nil || n != 0 {
		t.Fatal("有效checkpoint产物被清理", n, err)
	}
	other := scope
	other.UserID = uuid.NewString()
	if _, err = repo.FindScoped(ctx, other, saved.ID); !errors.Is(err, ErrArtifactUnavailable) {
		t.Fatal("跨用户放行")
	}
	if err = testDB.Exec("DELETE FROM agent_checkpoints").Error; err != nil {
		t.Fatal(err)
	}
	if n, err := repo.DeleteExpired(ctx, time.Now()); err != nil || n != 3 {
		t.Fatal("过期产物未清理", n, err)
	}
	rollback := errors.New("业务动作失败")
	if err = NewTxManager(testDB).InTx(ctx, func(txCtx context.Context) error {
		if err := repo.SaveImmutable(txCtx, newArtifact("rollback"), limits); err != nil {
			return err
		}
		return rollback
	}); !errors.Is(err, rollback) {
		t.Fatal("事务失败未透传", err)
	}
	var remaining int64
	if err = testDB.Model(&entity.ContextArtifact{}).Count(&remaining).Error; err != nil || remaining != 0 {
		t.Fatal("产物写入未随业务事务回滚", remaining, err)
	}
	if _, err = conn.ExecContext(ctx, "RESET search_path"); err != nil {
		t.Fatal(err)
	}
}
