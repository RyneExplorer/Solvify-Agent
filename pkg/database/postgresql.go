package database

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"

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
//  1. 查 pg_indexes 看 idx_document_chunks_embedding 是否已存在
//  2. 不存在 → 自动创建 ivfflat 索引（lists=100, cosine, partial WHERE embedding IS NOT NULL）
//  3. 存在但类型不是 ivfflat/hnsw → 打警告（可能失效或全表扫描）
//  4. 表不存在或无 embedding 列 → 跳过（AutoMigrate 或 schema 会补上）
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

	// 查询索引信息
	type indexInfo struct {
		IndexName string `gorm:"column:indexname"`
		IndexType string `gorm:"column:indexdef"`
	}
	var existing []indexInfo
	if err := db.Raw(
		`SELECT indexname, indexdef FROM pg_indexes WHERE tablename = 'document_chunks' AND indexname = 'idx_document_chunks_embedding'`,
	).Scan(&existing).Error; err != nil {
		return fmt.Errorf("查询向量索引状态失败: %w", err)
	}

	if len(existing) > 0 {
		idxDef := existing[0].IndexType
		isValidType := contains(idxDef, "ivfflat") || contains(idxDef, "hnsw") || contains(idxDef, "pgvector")
		if !isValidType {
			logger.Warnf("[pgvector] 向量索引 idx_document_chunks_embedding 存在但类型异常，建议重建。当前定义: %s", idxDef)
		} else {
			logger.Infof("[pgvector] 向量索引 idx_document_chunks_embedding 已就绪: %s", idxDef)
		}
		return nil
	}

	// 自动创建 ivfflat 索引
	logger.Info("[pgvector] 向量索引 idx_document_chunks_embedding 不存在，正在创建 ivfflat 索引...")
	err := db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_document_chunks_embedding
		ON document_chunks
		USING ivfflat (embedding vector_cosine_ops)
		WITH (lists = 100)
		WHERE embedding IS NOT NULL
	`).Error
	if err != nil {
		logger.Warnf("[pgvector] 自动创建 ivfflat 索引失败: %v（低流量环境可能需要先执行 VACUUM ANALYZE document_chunks）", err)
		return nil // 不阻塞启动，只是警告
	}
	logger.Info("[pgvector] 向量索引 idx_document_chunks_embedding 创建完成")
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

	var exists bool
	if err := db.Raw(
		`SELECT EXISTS (
			SELECT 1 FROM pg_indexes
			WHERE tablename = 'document_chunks' AND indexname = 'idx_document_chunks_keywords'
		)`,
	).Scan(&exists).Error; err != nil {
		return fmt.Errorf("查询 keywords GIN 索引状态失败: %w", err)
	}
	if exists {
		logger.Info("[pgvector] keywords GIN 索引 idx_document_chunks_keywords 已就绪")
		return nil
	}

	logger.Info("[pgvector] keywords GIN 索引不存在，正在创建...")
	err := db.Exec(`
		CREATE INDEX IF NOT EXISTS idx_document_chunks_keywords
		ON document_chunks
		USING gin (keywords)
		WHERE keywords IS NOT NULL
	`).Error
	if err != nil {
		logger.Warnf("[pgvector] 自动创建 keywords GIN 索引失败: %v", err)
		return nil
	}
	logger.Info("[pgvector] keywords GIN 索引 idx_document_chunks_keywords 创建完成")
	return nil
}

// EnsureContextIndexes 检查并确保 RAG 上下文加载链路高频查询涉及的三张表有正确索引。
// chat_messages 的 (session_id, created_at) 复合索引是 initContext → BuildContext 里 FindRecent / SearchRecentByKeywords 的核心加速。
// chat_sessions 和 user_memories 同理，ListActive / FindByUserID 都是高频操作。
func EnsureContextIndexes(db *gorm.DB) error {
	type ctxIndex struct {
		table  string
		index  string
		sql    string
	}
	idxs := []ctxIndex{
		{
			table: "chat_messages", index: "idx_chat_messages_session_created",
			sql: `CREATE INDEX IF NOT EXISTS idx_chat_messages_session_created ON chat_messages (session_id, created_at DESC)`,
		},
		{
			table: "chat_sessions", index: "idx_chat_sessions_user_status",
			sql: `CREATE INDEX IF NOT EXISTS idx_chat_sessions_user_status ON chat_sessions (user_id, status)`,
		},
		{
			table: "user_memories", index: "idx_user_memories_user_active",
			sql: `CREATE INDEX IF NOT EXISTS idx_user_memories_user_active ON user_memories (user_id, is_active) WHERE is_active = true`,
		},
	}
	for _, it := range idxs {
		var exists bool
		if err := db.Raw(`SELECT EXISTS (SELECT 1 FROM pg_indexes WHERE tablename = ? AND indexname = ?)`, it.table, it.index).Scan(&exists).Error; err != nil {
			logger.Warnf("[context] 查询索引状态失败 table=%s index=%s: %v", it.table, it.index, err)
			continue
		}
		if exists {
			continue
		}
		logger.Infof("[context] 创建索引 table=%s index=%s", it.table, it.index)
		if err := db.Exec(it.sql).Error; err != nil {
			logger.Warnf("[context] 自动创建索引失败 table=%s index=%s: %v", it.table, it.index, err)
		} else {
			logger.Infof("[context] 索引已就绪 index=%s", it.index)
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

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
