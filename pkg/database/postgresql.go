package database

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"solvify-agent/pkg/config"
	"solvify-agent/pkg/logger"
)

// OpenPostgreSQL 初始化 PostgreSQL 连接
func OpenPostgreSQL(cfg *config.PostgresConfig) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(buildPostgreSQLDSN(cfg)), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("连接 PostgreSQL 失败: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("获取 PostgreSQL 连接池失败: %w", err)
	}

	sqlDB.SetMaxIdleConns(cfg.MaxIdleConns)
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetConnMaxLifetime(time.Duration(cfg.ConnMaxLifetimeMinutes) * time.Minute)

	if err := sqlDB.Ping(); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("检查 PostgreSQL 连接失败: %w", err)
	}

	if cfg.EnablePGVector {
		if err := enablePGVector(db); err != nil {
			_ = sqlDB.Close()
			return nil, err
		}
	}

	logger.Info("PostgreSQL 连接成功",
		zap.String("host", cfg.Host),
		zap.Int("port", cfg.Port),
		zap.String("database", cfg.Database),
		zap.String("user", cfg.Username),
	)
	return db, nil
}

// ClosePostgreSQL 关闭 PostgreSQL 连接
func ClosePostgreSQL(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("获取 PostgreSQL 连接池失败: %w", err)
	}
	if err := sqlDB.Close(); err != nil {
		return fmt.Errorf("关闭 PostgreSQL 连接失败: %w", err)
	}
	return nil
}

// buildPostgreSQLDSN 生成 PostgreSQL 连接地址
func buildPostgreSQLDSN(cfg *config.PostgresConfig) string {
	dsn := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(cfg.Username, cfg.Password),
		Host:   net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Path:   cfg.Database,
	}
	if cfg.TimeZone != "" {
		dsn.RawQuery = "TimeZone=" + cfg.TimeZone
	}
	return dsn.String()
}

// enablePGVector 启用 pgvector 扩展
func enablePGVector(db *gorm.DB) error {
	if err := db.Exec("CREATE EXTENSION IF NOT EXISTS vector").Error; err != nil {
		return fmt.Errorf("启用 pgvector 扩展失败: %w", err)
	}
	logger.Info("pgvector 扩展检查完成")
	return nil
}

// EnsurePGVectorIndex 检查并确保 document_chunks 表的 pgvector 向量索引存在。
//
// 逻辑：
//  0. 表不存在 / 无 embedding 列 → 跳过（AutoMigrate 或 schema 会补上）
//  1. embedding 列是「无维度」的 vector → 直接给出可执行的修复语句并返回。
//     无维度列建不了 ivfflat/hnsw，硬试只会每次启动刷一条 SQLSTATE 22023 的 WARN，
//     而真正的解法（ALTER COLUMN ... TYPE vector(N)）必须由人执行，所以在这里一次说清。
//  2. 按「定义」而不是「名字」判断向量索引是否可用（见 ensureIndex）
//  3. 不可用 → 创建 ivfflat 索引（lists=100, cosine, partial WHERE embedding IS NOT NULL）
//  4. 同名但定义不同 → 打警告（不自动重建，重建会顶掉现有索引，需人工判断）
func EnsurePGVectorIndex(db *gorm.DB) error {
	// 先检查表是否存在
	var tableExists bool
	if err := db.Raw(
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'document_chunks')`,
	).Scan(&tableExists).Error; err != nil {
		return fmt.Errorf("检查 document_chunks 表存在性失败: %w", err)
	}
	if !tableExists {
		logger.Warn("[pgvector] document_chunks 表不存在，跳过向量索引检查")
		return nil
	}

	// 检查 embedding 列是否存在
	var colExists bool
	if err := db.Raw(
		`SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = 'document_chunks' AND column_name = 'embedding'
		)`,
	).Scan(&colExists).Error; err != nil {
		return fmt.Errorf("检查 embedding 列存在性失败: %w", err)
	}
	if !colExists {
		logger.Warn("[pgvector] document_chunks.embedding 列不存在，跳过向量索引检查")
		return nil
	}

	// embedding 列必须是「带维度的 vector(N)」——这是建 ivfflat/hnsw 的前置条件。
	// 无维度的 vector 列在 PostgreSQL 里合法，但任何索引方法都用不了它（SQLSTATE 22023）。
	// 把前置条件提前判定，失败原因就不再需要靠猜：要么维度没定，要么是别的确证错误。
	var formatted string
	if err := db.Raw(`
		SELECT format_type(a.atttypid, a.atttypmod)
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relname = 'document_chunks' AND a.attname = 'embedding'
	`).Scan(&formatted).Error; err != nil {
		return fmt.Errorf("查询 embedding 列类型失败: %w", err)
	}
	if vectorColumnHasNoDimension(formatted) {
		logger.Warnf("[pgvector] document_chunks.embedding 是「无维度」的 %s，ivfflat/hnsw 都无法建立；%s",
			formatted, vectorDimensionRemediation(db))
		return nil
	}

	// 建索引：判据是「定义」而不是「名字」，缺了才建（见 ensureIndex）
	if err := ensureIndex(db, pgVectorIndexSpec); err != nil {
		// ⚠️ 只按「已确证的错误类别」给建议，未归类的一律不给建议。
		// 旧版对所有失败都附「低流量环境可能需要先执行 VACUUM ANALYZE document_chunks」，
		// 而实际最常发生的 22023 是「列没有维度」——VACUUM 对它完全无效，只会把排查带偏。
		if hint := pgIndexFailureHint(err); hint != "" {
			logger.Warnf("[pgvector] 自动创建 ivfflat 索引失败: %v；%s", err, hint)
		} else {
			logger.Warnf("[pgvector] 自动创建 ivfflat 索引失败: %v（未归类的错误，请按原始 SQLSTATE 排查）", err)
		}
		return nil // 不阻塞启动，只是警告
	}
	return nil
}

// EnsureKeywordsGINIndex 检查并确保 document_chunks 表的 keywords 列有 GIN 索引。
// keywords && ?::text[] 这种数组重叠操作必须用 GIN 索引加速，否则每次关键词检索都是全表扫描。
func EnsureKeywordsGINIndex(db *gorm.DB) error {
	var tableExists bool
	if err := db.Raw(
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'document_chunks')`,
	).Scan(&tableExists).Error; err != nil {
		return fmt.Errorf("检查 document_chunks 表存在性失败: %w", err)
	}
	if !tableExists {
		logger.Warn("[pgvector] document_chunks 表不存在，跳过 keywords GIN 索引检查")
		return nil
	}

	var colExists bool
	if err := db.Raw(
		`SELECT EXISTS (
			SELECT 1 FROM information_schema.columns
			WHERE table_name = 'document_chunks' AND column_name = 'keywords'
		)`,
	).Scan(&colExists).Error; err != nil {
		return fmt.Errorf("检查 keywords 列存在性失败: %w", err)
	}
	if !colExists {
		logger.Warn("[pgvector] document_chunks.keywords 列不存在，跳过 GIN 索引检查")
		return nil
	}

	if err := ensureIndex(db, keywordsGINIndexSpec); err != nil {
		logger.Warnf("[pgvector] keywords GIN 索引检查失败: %v", err)
	}
	return nil
}

// EnsureContextIndexes 检查并确保 RAG 上下文加载链路高频查询涉及的三张表有正确索引。
// chat_messages 的 (session_id, created_at) 复合索引是 initContext → BuildContext 里 FindRecent / SearchRecentByKeywords 的核心加速。
// chat_sessions 和 user_memories 同理，ListActive / FindByUserID 都是高频操作。
// 索引的「名字与定义」统一声明在 index_guard.go，以 scripts/init_knowledge_schema.sql 为基准；
// 是否存在按「定义」判断 —— 历史事故正是「同一件事两个来源各起一个名字」建出了两份。
func EnsureContextIndexes(db *gorm.DB) error {
	for _, spec := range contextIndexSpecs {
		if err := ensureIndex(db, spec); err != nil {
			logger.Warnf("[context] 索引检查失败 table=%s index=%s: %v", spec.Table, spec.Name, err)
		}
	}
	return nil
}

// EnsureToolProviderSchema 补齐 tool_providers 表缺失的列。
// 早期 AutoMigrate 建表后 entity 新增了 is_system 列，AutoMigrate 不会为已存在的表 ADD COLUMN，
// 需要手动补齐以区分系统预置和管理员自定义的 MCP 供应商。
func EnsureToolProviderSchema(db *gorm.DB) error {
	var tableExists bool
	if err := db.Raw(
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'tool_providers')`,
	).Scan(&tableExists).Error; err != nil {
		return fmt.Errorf("检查 tool_providers 表存在性失败: %w", err)
	}
	if !tableExists {
		return nil
	}

	type colDef struct {
		name string
		ddl  string
	}
	missingCols := []colDef{
		{name: "is_system", ddl: "boolean NOT NULL DEFAULT false"},
		{name: "mcp_tool_manifest", ddl: "jsonb"},
	}

	for _, mc := range missingCols {
		var exists bool
		if err := db.Raw(
			`SELECT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_name = 'tool_providers' AND column_name = ?
			)`, mc.name,
		).Scan(&exists).Error; err != nil {
			logger.Warnf("[tool_provider] 检查列 %s 失败: %v", mc.name, err)
			continue
		}
		if exists {
			continue
		}
		logger.Infof("[tool_provider] 列 %s 不存在，正在 ALTER TABLE ADD COLUMN", mc.name)
		if err := db.Exec(
			fmt.Sprintf("ALTER TABLE tool_providers ADD COLUMN IF NOT EXISTS %s %s", mc.name, mc.ddl),
		).Error; err != nil {
			logger.Warnf("[tool_provider] 自动补列 %s 失败: %v", mc.name, err)
		} else {
			logger.Infof("[tool_provider] 列 %s 已补齐", mc.name)
		}
	}
	return nil
}

// EnsureMessageFeedbackSchema 补齐 message_feedback 表缺失的列。
// 这张表是早期 AutoMigrate 创建的，后来 entity 加了 reasons / is_quick / trace_id 等列，
// 但 AutoMigrate 不会给已存在的表 ADD COLUMN，导致 INSERT 时报 column "xxx" does not exist。
func EnsureMessageFeedbackSchema(db *gorm.DB) error {
	var tableExists bool
	if err := db.Raw(
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'message_feedback')`,
	).Scan(&tableExists).Error; err != nil {
		return fmt.Errorf("检查 message_feedback 表存在性失败: %w", err)
	}
	if !tableExists {
		return nil
	}

	type colDef struct {
		name string
		ddl  string // ALTER TABLE ... ADD COLUMN ... 的列定义（不含列名）
	}
	missingCols := []colDef{
		{name: "reasons", ddl: "jsonb"},
		{name: "is_quick", ddl: "boolean NOT NULL DEFAULT false"},
		{name: "trace_id", ddl: "varchar(128)"},
		{name: "reason_tag", ddl: "varchar(64)"},
	}

	for _, mc := range missingCols {
		var exists bool
		if err := db.Raw(
			`SELECT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_name = 'message_feedback' AND column_name = ?
			)`, mc.name,
		).Scan(&exists).Error; err != nil {
			logger.Warnf("[feedback] 检查列 %s 失败: %v", mc.name, err)
			continue
		}
		if exists {
			continue
		}
		logger.Infof("[feedback] 列 %s 不存在，正在 ALTER TABLE ADD COLUMN", mc.name)
		if err := db.Exec(
			fmt.Sprintf("ALTER TABLE message_feedback ADD COLUMN IF NOT EXISTS %s %s", mc.name, mc.ddl),
		).Error; err != nil {
			logger.Warnf("[feedback] 自动补列 %s 失败: %v", mc.name, err)
		} else {
			logger.Infof("[feedback] 列 %s 已补齐", mc.name)
		}
	}
	return nil
}

// EnsureChatTraceSchema 补齐 chat_traces 表缺失的列与索引。
//
// 背景：chat_traces 由 SQL 脚本建表（scripts/init_knowledge_schema.sql），没有走 AutoMigrate；
// entity.ChatTrace 后来新增了 otel_trace_id（双轨 traceID 对齐，见 internal/observability），
// 已存在的库不会自动加列，而 GORM Create 会把该列写进 INSERT →
// 报 column "otel_trace_id" does not exist，直接导致所有 trace 落库失败。
// 这里在启动期幂等补齐，避免要求运维手工执行 DDL。
func EnsureChatTraceSchema(db *gorm.DB) error {
	var tableExists bool
	if err := db.Raw(
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'chat_traces')`,
	).Scan(&tableExists).Error; err != nil {
		return fmt.Errorf("检查 chat_traces 表存在性失败: %w", err)
	}
	if !tableExists {
		return nil
	}

	type colDef struct {
		name string
		ddl  string // ALTER TABLE ... ADD COLUMN ... 的列定义（不含列名）
	}
	// otel_trace_id：自研 traceID 对应的 OTel traceID，用于从本系统跳转到三方追踪平台
	missingCols := []colDef{
		{name: "otel_trace_id", ddl: "varchar(32)"},
	}

	for _, mc := range missingCols {
		var exists bool
		if err := db.Raw(
			`SELECT EXISTS (
				SELECT 1 FROM information_schema.columns
				WHERE table_name = 'chat_traces' AND column_name = ?
			)`, mc.name,
		).Scan(&exists).Error; err != nil {
			logger.Warnf("[trace] 检查列 %s 失败: %v", mc.name, err)
			continue
		}
		if exists {
			continue
		}
		logger.Infof("[trace] 列 %s 不存在，正在 ALTER TABLE ADD COLUMN", mc.name)
		if err := db.Exec(
			fmt.Sprintf("ALTER TABLE chat_traces ADD COLUMN IF NOT EXISTS %s %s", mc.name, mc.ddl),
		).Error; err != nil {
			logger.Warnf("[trace] 自动补列 %s 失败: %v", mc.name, err)
		} else {
			logger.Infof("[trace] 列 %s 已补齐", mc.name)
		}
	}

	// 索引：按 OTel traceID 反查对话（在三方平台看到异常 trace 后回查本系统的业务信息）
	// 判据是「定义」而不是「名字」，免得与建库基线各建一份等价索引
	if err := ensureIndex(db, chatTraceOTelIndexSpec); err != nil {
		logger.Warnf("[trace] 索引 idx_chat_traces_otel_trace_id 检查失败: %v", err)
	}
	return nil
}

// vectorColumnHasNoDimension 判断 format_type 的输出是否是「无维度」的 vector 列。
// pgvector 允许 vector 列不带维度修饰符，但这样的列无法建立 ivfflat/hnsw 索引，
// PostgreSQL 会报 SQLSTATE 22023: column does not have dimensions。
// 判据用「有没有维度修饰符」，而不是去猜列名或比对字符串常量。
func vectorColumnHasNoDimension(formatted string) bool {
	return strings.HasPrefix(formatted, "vector") && !strings.Contains(formatted, "(")
}

// vectorDimensionRemediation 给出一条可直接执行的修复语句。
// 维度取自「库里真实存在的向量」（vector_dims），而不是配置或常量——
// 这样提示里写出的数字永远与库中数据一致，不会因为改过模型而变成另一个猜测值。
func vectorDimensionRemediation(db *gorm.DB) string {
	var dim int
	if err := db.Raw(
		`SELECT vector_dims(embedding) FROM document_chunks WHERE embedding IS NOT NULL LIMIT 1`,
	).Scan(&dim).Error; err != nil || dim <= 0 {
		return "请确认向量模型维度后执行：ALTER TABLE document_chunks ALTER COLUMN embedding TYPE vector(<维度>)"
	}
	return fmt.Sprintf(
		"执行 ALTER TABLE document_chunks ALTER COLUMN embedding TYPE vector(%d) 即可正常建立（VACUUM ANALYZE 对此无效）",
		dim,
	)
}

// pgIndexFailureHint 按 SQLSTATE 返回「已被证实」的处置建议。
// 规则：只有能确证成因的错误才给建议，其余返回空串。
// 报错信息宁可少说，也不能说错——说错的提示会把人往反方向带（见上面 22023 的历史）。
// 证据：test1/sqlstate_probe.go 实测该错误是未包裹的 *pgconn.PgError，Code 可直接取到。
func pgIndexFailureHint(err error) string {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return ""
	}
	switch {
	// ⚠️ 22023 是「无效参数值」大类，不能只看码：pgvector 也用 22023 报其它参数错误
	// （如 lists 越界、维度超过索引方法上限）。只看码就断言「列没有维度」，
	// 等于重犯本函数要修的那个毛病——拿未确证的归因去指路。所以必须连 Message 一起核对。
	case pgErr.Code == sqlstateInvalidParameterValue &&
		strings.Contains(pgErr.Message, "does not have dimensions"):
		return "该列是无维度的 vector，ivfflat/hnsw 都无法建立；需先用 ALTER COLUMN ... TYPE vector(<维度>) 固定维度"
	case pgErr.Code == sqlstateInsufficientPrivilege: // 42501
		return "当前数据库角色没有建索引权限，请改用表属主或超级用户执行"
	case pgErr.Code == sqlstateUndefinedTable: // 42P01
		return "目标表不存在，请先确认 schema 已完整应用"
	default:
		return ""
	}
}

// PostgreSQL SQLSTATE（只列本文件用到的）。
const (
	sqlstateInvalidParameterValue = "22023" // 无效参数值（pgvector 建索引时的几类错误共用）
	sqlstateInsufficientPrivilege = "42501" // 权限不足
	sqlstateUndefinedTable        = "42P01" // 表不存在
)
