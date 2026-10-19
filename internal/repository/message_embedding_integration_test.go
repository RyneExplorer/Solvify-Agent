package repository

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlog "gorm.io/gorm/logger"
	"solvify-agent/internal/model/entity"
	"solvify-agent/pkg/config"
	"solvify-agent/pkg/database"
)

func TestMessageEmbeddingPostgresRecovery(t *testing.T) {
	if os.Getenv("SOLVIFY_EMBEDDING_DB_TEST") != "1" {
		t.Skip("设置SOLVIFY_EMBEDDING_DB_TEST=1验证隔离PostgreSQL任务恢复")
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
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	schema := os.Getenv("SOLVIFY_EMBEDDING_TEST_SCHEMA")
	child := schema != ""
	if !child {
		schema = "embedding_test_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	if !regexp.MustCompile(`^embedding_test_[0-9]+$`).MatchString(schema) {
		t.Fatal("拒绝无效测试schema")
	}
	admin := stdlib.OpenDB(*pgcfg)
	defer admin.Close()
	if !child {
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
	}
	pgcfg.RuntimeParams["search_path"] = schema + ",public"
	pool := stdlib.OpenDB(*pgcfg)
	defer pool.Close()
	pool.SetMaxOpenConns(12)
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: pool}), &gorm.Config{Logger: gormlog.Default.LogMode(gormlog.Silent)})
	if err != nil {
		t.Fatal("测试连接失败")
	}
	repo := NewMessageEmbeddingRepository(db)
	const restartedID = "c597042c-5d9e-4d54-8f48-c5dbb2d608be"
	if child {
		// 独立进程不依赖内存状态：接管另一进程遗留的过期租约。
		job, e := repo.Claim(ctx, 3, time.Minute)
		if e != nil || job == nil || job.ID != restartedID || job.EmbeddingAttempts != 2 {
			t.Fatal("独立进程恢复领取失败", job, e)
		}
		if ok, e := repo.Complete(ctx, *job, entity.FloatVector{.2, .3}); e != nil || !ok {
			t.Fatal("独立进程结果未提交", e)
		}
		return
	}
	mustExec := func(query string, args ...any) {
		t.Helper()
		if err := db.WithContext(ctx).Exec(query, args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	// 构造升级前表，向量服务不参与本测试。
	mustExec(`CREATE TABLE chat_messages (
 id uuid PRIMARY KEY DEFAULT gen_random_uuid(),session_id uuid NOT NULL,role varchar(20) NOT NULL,content text NOT NULL DEFAULT '',
 model_id varchar(36),search_mode varchar(20) NOT NULL DEFAULT 'quick',knowledge_base_ids jsonb NOT NULL DEFAULT '[]',
 sources jsonb,metadata jsonb NOT NULL DEFAULT '{}',embedding vector(2),created_at timestamptz NOT NULL DEFAULT now())`)
	oldID := uuid.NewString()
	mustExec(`INSERT INTO chat_messages(id,session_id,role,content) VALUES (?,?,'user','旧消息')`, oldID, uuid.NewString())
	if database.CheckMessageEmbeddingSchema(ctx, db) == nil {
		t.Fatal("未升级表结构未被识别")
	}
	migration, err := os.ReadFile("../../scripts/migrations/20261017_message_embedding_jobs.sql")
	if err != nil {
		t.Fatal(err)
	}
	migrate := func() {
		t.Helper()
		if _, e := pool.ExecContext(ctx, string(migration)); e != nil {
			t.Fatal("迁移失败", e)
		}
	}
	migrate()
	migrate()
	if err = database.CheckMessageEmbeddingSchema(ctx, db); err != nil {
		t.Fatal(err)
	}
	assertState := func(id, status string, attempts int) {
		t.Helper()
		var row entity.ChatMessage
		if err := db.WithContext(ctx).First(&row, "id=?", id).Error; err != nil || row.EmbeddingStatus != status || row.EmbeddingAttempts != attempts {
			t.Fatalf("状态错误 id=%s want=%s/%d got=%s/%d err=%v", id, status, attempts, row.EmbeddingStatus, row.EmbeddingAttempts, err)
		}
	}
	assertState(oldID, "none", 0)
	if job, e := repo.Claim(ctx, 3, time.Minute); e != nil || job != nil {
		t.Fatal("升级自动登记了旧消息", e)
	}
	messages := NewChatMessageRepository(db)
	insert := func(id, status, content string) {
		t.Helper()
		if e := messages.Create(ctx, &entity.ChatMessage{ID: id, SessionID: uuid.NewString(), Role: "user", Content: content, EmbeddingStatus: status}); e != nil {
			t.Fatal(e)
		}
	}
	rollbackID := uuid.NewString()
	rollback := errors.New("rollback")
	err = NewTxManager(db).InTx(ctx, func(txCtx context.Context) error {
		if e := messages.Create(txCtx, &entity.ChatMessage{ID: rollbackID, SessionID: uuid.NewString(), Role: "user", Content: "回滚", EmbeddingStatus: "queued"}); e != nil {
			return e
		}
		if job, e := repo.Claim(ctx, 3, time.Minute); e != nil || job != nil {
			t.Fatal("提交前领取到任务", e)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	var count int64
	db.WithContext(ctx).Model(&entity.ChatMessage{}).Where("id=?", rollbackID).Count(&count)
	if count != 0 {
		t.Fatal("正文与任务回滚失败")
	}
	// 多消费者竞争同一任务，只能有一个取得租约。
	id := uuid.NewString()
	insert(id, "queued", "并发消息")
	var wg sync.WaitGroup
	claims := make(chan *ClaimedMessageEmbedding, 8)
	failures := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			job, e := repo.Claim(ctx, 3, time.Minute)
			if e != nil {
				failures <- e
			}
			if job != nil {
				claims <- job
			}
		}()
	}
	wg.Wait()
	close(failures)
	for e := range failures {
		t.Error(e)
	}
	if len(claims) != 1 {
		t.Fatalf("重复领取: %d", len(claims))
	}
	job := <-claims
	if ok, e := repo.Fail(ctx, *job, true, time.Hour, "connection"); e != nil || !ok {
		t.Fatal(e)
	}
	if next, e := repo.Claim(ctx, 3, time.Minute); e != nil || next != nil {
		t.Fatal("忽略退避时间", e)
	}
	mustExec(`UPDATE chat_messages SET embedding_available_at=now() WHERE id=?`, id)
	next, e := repo.Claim(ctx, 3, time.Minute)
	if e != nil || next == nil || next.EmbeddingAttempts != 2 {
		t.Fatal("重试未领取", e)
	}
	if ok, e := repo.Complete(ctx, *job, entity.FloatVector{1, 2}); e != nil || ok {
		t.Fatal("旧令牌能写入结果", e)
	}
	if ok, e := repo.Release(ctx, *next); e != nil || !ok {
		t.Fatal("停机归还失败", e)
	}
	assertState(id, "queued", 1)
	next, e = repo.Claim(ctx, 3, time.Minute)
	if e != nil || next == nil {
		t.Fatal(e)
	}
	mustExec(`UPDATE chat_messages SET embedding_lease_until=now()-interval '1 second' WHERE id=?`, id)
	if ok, e := repo.Complete(ctx, *next, entity.FloatVector{1, 2}); e != nil || ok {
		t.Fatal("过期租约能提交", e)
	}
	last, e := repo.Claim(ctx, 3, time.Minute)
	if e != nil || last == nil || last.EmbeddingAttempts != 3 {
		t.Fatal("过期任务未恢复", e)
	}
	mustExec(`UPDATE chat_messages SET embedding_lease_until=now()-interval '1 second' WHERE id=?`, id)
	if next, e := repo.Claim(ctx, 3, time.Minute); e != nil || next != nil {
		t.Fatal("超次数继续执行", e)
	}
	assertState(id, "failed", 3)
	// 删除消息会同时消除任务，迟到结果不能重新创建消息。
	deleted := uuid.NewString()
	insert(deleted, "queued", "删除")
	next, e = repo.Claim(ctx, 3, time.Minute)
	if e != nil || next == nil {
		t.Fatal(e)
	}
	mustExec(`DELETE FROM chat_messages WHERE id=?`, deleted)
	if ok, e := repo.Complete(ctx, *next, entity.FloatVector{1, 2}); e != nil || ok {
		t.Fatal("删除后提交成功", e)
	}
	// 手动补齐有限且幂等，不包含空文本或已有向量，失败默认不重置。
	empty := uuid.NewString()
	insert(empty, "none", "   ")
	if n, e := repo.QueueMissing(ctx, 1, false); e != nil || n != 1 {
		t.Fatal("补齐旧消息失败", n, e)
	}
	assertState(id, "failed", 3)
	if n, e := repo.QueueMissing(ctx, 10, false); e != nil || n != 0 {
		t.Fatal("补齐重复或包含空文本", n, e)
	}
	next, e = repo.Claim(ctx, 3, time.Minute)
	if e != nil || next == nil || next.ID != oldID {
		t.Fatal(e)
	}
	if ok, e := repo.Complete(ctx, *next, entity.FloatVector{.1, .2}); e != nil || !ok {
		t.Fatal("向量与终态写入失败", e)
	}
	assertState(oldID, "succeeded", 1)
	migrate()
	assertState(oldID, "succeeded", 1)
	if n, e := repo.QueueMissing(ctx, 10, true); e != nil || n != 1 {
		t.Fatal("显式重新排队失败", n, e)
	}
	assertState(id, "queued", 0)
	next, e = repo.Claim(ctx, 3, time.Minute)
	if e != nil || next == nil {
		t.Fatal(e)
	}
	if ok, e := repo.Fail(ctx, *next, false, 0, "authentication"); e != nil || !ok {
		t.Fatal(e)
	}
	assertState(id, "failed", 1)
	insert(restartedID, "queued", "跨进程恢复")
	abandoned, e := repo.Claim(ctx, 3, time.Minute)
	if e != nil || abandoned == nil || abandoned.ID != restartedID {
		t.Fatal(e)
	}
	mustExec(`UPDATE chat_messages SET embedding_lease_until=now()-interval '1 second' WHERE id=?`, restartedID)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, binary, "-test.run=^TestMessageEmbeddingPostgresRecovery$", "-test.v")
	command.Env = append(os.Environ(), "SOLVIFY_EMBEDDING_TEST_SCHEMA="+schema)
	if output, e := command.CombinedOutput(); e != nil {
		t.Fatalf("独立进程恢复失败: %v %s", e, output)
	}
	assertState(restartedID, "succeeded", 2)
	if ok, e := repo.Complete(ctx, *abandoned, entity.FloatVector{9, 9}); e != nil || ok {
		t.Fatal("旧进程覆盖恢复结果", e)
	}
	// 数据库约束拒绝不完整运行态。
	if e := db.WithContext(ctx).Exec(`UPDATE chat_messages SET embedding_status='running' WHERE id=?`, empty).Error; e == nil {
		t.Fatal("缺少租约的运行态未被约束拒绝")
	}
	t.Log(fmt.Sprintf("隔离schema %s：重复迁移、原子登记/回滚、8消费者竞争、退避/租约/次数上限、停机归还、删除及独立进程恢复通过", schema))
}
