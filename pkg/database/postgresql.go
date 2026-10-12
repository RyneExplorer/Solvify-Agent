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

// EnsureChatMessageEmbeddingSchema 补齐 chat_messages.embedding 列并核对维度。
//
// 语义召回要读写这一列，而它不进 AutoMigrate 也没有启动期检查，于是出现过
// 「代码已按有向量写、库里 932 行全是 NULL」的状态：写侧批量报错、召回侧永远查不到，
// 聊天本身照常工作，没有任何用户可见症状。
//
// 维度在这里核对（而不是等到写入失败）：配置里的 dimension 与列的实际维度必须一致，
// 不一致时 INSERT/UPDATE 会报 `expected 1024 dimensions, not N`—— 但那是一行一行的
// 失败，混在业务日志里没人看。启动期一次说清「配置要 N、库是 M」，错配就显形了。
func EnsureChatMessageEmbeddingSchema(db *gorm.DB, wantDim int) error {
	var tableExists bool
	if err := db.Raw(
		`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'chat_messages')`,
	).Scan(&tableExists).Error; err != nil {
		return fmt.Errorf("检查 chat_messages 表存在性失败: %w", err)
	}
	if !tableExists {
		logger.Warn("[embedding] chat_messages 表不存在，跳过向量列检查")
		return nil
	}

	var colType string
	err := db.Raw(`
		SELECT COALESCE(format_type(a.atttypid, a.atttypmod), '')
		FROM pg_attribute a
		JOIN pg_class c ON c.oid = a.attrelid
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = 'public' AND c.relname = 'chat_messages' AND a.attname = 'embedding'
	`).Scan(&colType).Error
	if err != nil {
		return fmt.Errorf("查询 chat_messages.embedding 列定义失败: %w", err)
	}

	// 1) 列不存在 → 按配置维度补上。补完仍要核对，所以不 return。
	if colType == "" {
		if wantDim <= 0 {
			logger.Warnf("[embedding] chat_messages.embedding 列不存在，且 embedding.dimension=%d 无法用于建列，跳过自动补列", wantDim)
			return nil
		}
		logger.Infof("[embedding] chat_messages.embedding 列不存在，正在按 dimension=%d 补列", wantDim)
		if err := db.Exec(fmt.Sprintf(
			"ALTER TABLE chat_messages ADD COLUMN IF NOT EXISTS embedding vector(%d)", wantDim,
		)).Error; err != nil {
			return fmt.Errorf("补 chat_messages.embedding 列失败: %w", err)
		}
		logger.Infof("[embedding] chat_messages.embedding 列已补齐")
		colType = fmt.Sprintf("vector(%d)", wantDim)
	}

	// 2) 列是无维度的 vector → 建不了 ANN 索引，且维度无从核对。
	// 与 EnsurePGVectorIndex 同一处置：给出可直接执行的修复语句，不擅自 ALTER
	// （改列类型会重写全表，且存量向量可能真的不是这个维度）。
	if vectorColumnHasNoDimension(colType) {
		logger.Warnf("[embedding] chat_messages.embedding 是「无维度」的 %s：ANN 索引无法建立，语义召回会退化成全表精确扫描；%s",
			colType, chatMessageVectorRemediation(db, wantDim))
		return nil
	}

	// 3) 维度核对：配置要的维度与列的实际维度必须一致。
	colDim := vectorDimensionFromType(colType)
	if wantDim > 0 && colDim > 0 && colDim != wantDim {
		logger.Errorf("[embedding] 维度错配：embedding.dimension=%d，但 chat_messages.embedding 实际是 %s —— "+
			"所有消息向量写入都会被 pgvector 拒绝（expected %d dimensions, not %d），语义召回恒为空。"+
			"修复：改 configs/config.yaml 的 embedding.dimension 使其等于 %d，或执行 %s",
			wantDim, colType, wantDim, colDim, colDim,
			fmt.Sprintf("ALTER TABLE chat_messages ALTER COLUMN embedding TYPE vector(%d)", colDim))
		return nil
	}

	// 4) 已写入多少行 —— 维度对得上但一行都没有，说明向量侧从未真正落库，
	// 这跟「维度错配」是两种完全不同的故障，必须分开报。
	var filled, total int64
	if err := db.Raw(
		`SELECT COUNT(*) AS total, COUNT(embedding) AS filled FROM chat_messages`,
	).Row().Scan(&total, &filled); err != nil {
		return fmt.Errorf("统计 chat_messages 向量覆盖率失败: %w", err)
	}
	if total > 0 && filled == 0 {
		logger.Warnf("[embedding] chat_messages.embedding 列已就绪（%s），但 %d 条历史消息全部没有向量：语义召回在历史数据上无效。"+
			"新消息会自动写入，历史消息需运行一次回填任务", colType, total)
		return nil
	}
	logger.Infof("[embedding] chat_messages.embedding 已就绪（%s），向量覆盖 %d/%d 条消息", colType, filled, total)
	return nil
}

// vectorDimensionFromType 从 `vector(1024)` 里取出 1024。
// 取不出（非 vector 类型 / 无维度）时返回 0，由调用方按「无从核对」处理。
func vectorDimensionFromType(colType string) int {
	open := strings.Index(colType, "(")
	close := strings.Index(colType, ")")
	if open < 0 || close <= open {
		return 0
	}
	dim, err := strconv.Atoi(strings.TrimSpace(colType[open+1 : close]))
	if err != nil {
		return 0
	}
	return dim
}

// chatMessageVectorRemediation 的维度取自库里真实存在的向量，取不到才退回配置值。
// 与 vectorDimensionRemediation 同思路：提示里的数字必须与库中数据一致。
func chatMessageVectorRemediation(db *gorm.DB, wantDim int) string {
	var dim int
	if err := db.Raw(
		`SELECT vector_dims(embedding) FROM chat_messages WHERE embedding IS NOT NULL LIMIT 1`,
	).Scan(&dim).Error; err == nil && dim > 0 {
		return fmt.Sprintf("执行 ALTER TABLE chat_messages ALTER COLUMN embedding TYPE vector(%d) 即可正常建立", dim)
	}
	if wantDim > 0 {
		return fmt.Sprintf("当前无存量向量，可直接执行 ALTER TABLE chat_messages ALTER COLUMN embedding TYPE vector(%d)", wantDim)
	}
	return "请确认向量模型维度后执行：ALTER TABLE chat_messages ALTER COLUMN embedding TYPE vector(<维度>)"
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
