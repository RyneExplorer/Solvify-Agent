package tool

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
	"solvify-agent/pkg/config"
)

// 显式启用；只在随机临时schema内造数，不调用模型，不访问业务表。
func TestDocumentToolsPostgresKnowledgeBaseScope(t *testing.T) {
	if os.Getenv("SOLVIFY_KNOWLEDGE_SCOPE_DB_TEST") != "1" {
		t.Skip("设置SOLVIFY_KNOWLEDGE_SCOPE_DB_TEST=1验证真实数据库知识库范围")
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
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	admin := stdlib.OpenDB(*pgcfg)
	defer admin.Close()
	schema := "knowledge_scope_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.ExecContext(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal("无法创建隔离测试schema")
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := admin.ExecContext(cleanup, "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error("隔离测试schema清理失败")
		}
	}()
	pgcfg.RuntimeParams["search_path"] = schema
	pool := stdlib.OpenDB(*pgcfg)
	defer pool.Close()
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: pool}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal("连接隔离schema失败")
	}
	exec := func(sql string, args ...any) {
		t.Helper()
		if err := db.WithContext(ctx).Exec(sql, args...).Error; err != nil {
			t.Fatal(err)
		}
	}
	exec(`CREATE TABLE documents(id uuid PRIMARY KEY,user_id uuid NOT NULL,knowledge_base_id uuid NOT NULL,title text NOT NULL,status int NOT NULL)`)
	exec(`CREATE TABLE document_chunks(id uuid PRIMARY KEY,user_id uuid NOT NULL,knowledge_base_id uuid NOT NULL,document_id uuid NOT NULL,content text NOT NULL,keywords text[] NOT NULL)`)
	owner, foreignUser := uuid.NewString(), uuid.NewString()
	kbA, kbB, kbForeign, kbMissing := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	docA, docB, docForeign, docDeleted := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, doc := range []struct {
		id, user, kb string
		status       int
	}{
		{docA, owner, kbA, entity.DocumentStatusReady},
		{docB, owner, kbB, entity.DocumentStatusReady},
		{docForeign, foreignUser, kbForeign, entity.DocumentStatusReady},
		{docDeleted, owner, kbA, entity.DocumentStatusDeleted},
	} {
		exec(`INSERT INTO documents VALUES(?,?,?,?,?)`, doc.id, doc.user, doc.kb, "测试文档-"+doc.id, doc.status)
	}
	// 范围外分块排序更靠前：验证在LIMIT前过滤，而不是检索后丢弃结果。
	const chunkB = "00000000-0000-0000-0000-000000000001"
	const chunkA = "00000000-0000-0000-0000-000000000002"
	const chunkForeign = "00000000-0000-0000-0000-000000000003"
	const chunkDeleted = "00000000-0000-0000-0000-000000000004"
	const chunkKeyword = "00000000-0000-0000-0000-000000000005"
	for _, chunk := range []struct{ id, user, kb, doc, content string }{
		{chunkB, owner, kbB, docB, "needle other library"},
		{chunkA, owner, kbA, docA, "needle selected library"},
		{chunkForeign, foreignUser, kbForeign, docForeign, "needle other user"},
		{chunkDeleted, owner, kbA, docDeleted, "needle deleted document"},
		{chunkKeyword, owner, kbA, docA, "only keyword array matches"},
	} {
		exec(`INSERT INTO document_chunks VALUES(?,?,?,?,?,ARRAY['needle'])`, chunk.id, chunk.user, chunk.kb, chunk.doc, chunk.content)
	}
	chunkRepo := repository.NewDocumentChunkRepository(db)
	docRepo := repository.NewDocumentRepository(db)
	for _, tc := range []struct {
		name  string
		kbIDs []string
		limit int
		want  []string
	}{
		{"单库含关键词数组匹配", []string{kbA}, 10, []string{chunkA, chunkKeyword}},
		{"另一所选库", []string{kbB}, 10, []string{chunkB}},
		{"多库", []string{kbA, kbB}, 10, []string{chunkB, chunkA, chunkKeyword}},
		{"先范围过滤再LIMIT", []string{kbA}, 1, []string{chunkA}},
		{"不存在的库", []string{kbMissing}, 10, nil},
		{"其他用户的库", []string{kbForeign}, 10, nil},
		{"混入其他用户的库", []string{kbA, kbForeign}, 10, []string{chunkA, chunkKeyword}},
		{"未选择", nil, 10, nil},
		{"空范围", []string{}, 10, nil},
	} {
		t.Run("grep/"+tc.name, func(t *testing.T) {
			args, _ := json.Marshal(GrepChunksInput{Keyword: "needle", Limit: tc.limit})
			raw, err := NewGrepChunksTool(chunkRepo)(owner, tc.kbIDs).InvokableRun(ctx, string(args))
			var result struct {
				Success bool `json:"success"`
				Data    []struct {
					ID string `json:"chunk_id"`
				} `json:"data"`
			}
			if err != nil || json.Unmarshal([]byte(raw), &result) != nil {
				t.Fatal("关键词工具调用失败", err)
			}
			var ids []string
			for _, row := range result.Data {
				ids = append(ids, row.ID)
			}
			if !slices.Equal(ids, tc.want) || result.Success != (len(tc.kbIDs) > 0) {
				t.Fatalf("关键词检索范围错误：want=%v result=%s", tc.want, raw)
			}
		})
	}
	for _, tc := range []struct {
		name, doc string
		kbIDs     []string
		allowed   bool
	}{
		{"所选库", docA, []string{kbA}, true},
		{"多库", docB, []string{kbA, kbB}, true},
		{"其他库的ID", docB, []string{kbA}, false},
		{"不存在的库", docA, []string{kbMissing}, false},
		{"其他用户的文档", docForeign, []string{kbForeign}, false},
		{"已删除文档", docDeleted, []string{kbA}, false},
		{"未选择", docA, nil, false},
	} {
		t.Run("info/"+tc.name, func(t *testing.T) {
			args, _ := json.Marshal(GetDocumentInfoInput{DocumentID: tc.doc})
			raw, err := NewGetDocumentInfoTool(docRepo)(owner, tc.kbIDs).InvokableRun(ctx, string(args))
			var result ToolResponse
			if err != nil || json.Unmarshal([]byte(raw), &result) != nil {
				t.Fatal("文档工具调用失败", err)
			}
			if result.Success != tc.allowed || (!tc.allowed && result.Data != nil) {
				t.Fatalf("文档读取范围错误：%s", raw)
			}
		})
	}
	// 全局搜索仍可搜索本人的多个库，但不包含其他用户和删除文档。
	global, err := chunkRepo.SearchByKeyword(ctx, owner, "needle", 10)
	var globalIDs []string
	for _, row := range global {
		globalIDs = append(globalIDs, row.ID)
	}
	if err != nil || !slices.Equal(globalIDs, []string{chunkB, chunkA, chunkKeyword}) {
		t.Fatal("全局搜索语义发生变化", globalIDs, err)
	}
	// 仓库自身也必须拒绝空范围，不能仅依赖工具入口。
	for _, scope := range [][]string{nil, {}} {
		rows, err := chunkRepo.SearchByKeywordInKnowledgeBases(ctx, owner, scope, "needle", 10)
		if err != nil || len(rows) != 0 {
			t.Fatal("仓库空范围检索应为空", rows, err)
		}
	}
}
