package database

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"solvify-agent/pkg/logger"
)

// ---------------------------------------------------------------------------
// 索引声明表
//
// ⚠️ 这里的「名字」与「定义」必须与 scripts/init_knowledge_schema.sql（生产建库基线）
// 逐字一致。理由：
//   - 基线只在「首次创建空数据卷」时执行一次（deploy/compose.prod.yaml 把它挂到
//     docker-entrypoint-initdb.d/001-init-schema.sql），而本文件的 Ensure* 每次启动都跑；
//   - 两边只要有一处名字不同，空库首次启动就会同时存在两份等价索引，
//     此后每次写表都要维护两份 —— document_chunks.keywords 的历史事故正是如此：
//     基线建 idx_document_chunks_keywords_gin，代码去找 idx_document_chunks_keywords，
//     「名字没找到」于是再建一份，两个索引功能完全相同；
//   - deploy/CI-CD-DESIGN.md 已规定「后续结构变更用独立迁移脚本，不直接改初始化基线」，
//     基线是冻结的，所以要对齐的只能是这里。
//
// 键列一律写「列名保序」，不写 ASC/DESC —— 排序方向不参与判据，理由见 covers()。
// ---------------------------------------------------------------------------
var (
	// 向量索引的定义（注意：建不建得起来还取决于 embedding 列有没有维度，见 EnsurePGVectorIndex）
	pgVectorIndexSpec = indexSpec{
		Table:     "document_chunks",
		Name:      "idx_document_chunks_embedding",
		Method:    "ivfflat",
		Columns:   []string{"embedding"},
		Predicate: "embedding IS NOT NULL",
		DDL: `CREATE INDEX IF NOT EXISTS idx_document_chunks_embedding
			ON document_chunks USING ivfflat (embedding vector_cosine_ops)
			WITH (lists = 100) WHERE embedding IS NOT NULL`,
	}

	// keywords 数组重叠检索（keywords && ?::text[]）的 GIN 索引
	keywordsGINIndexSpec = indexSpec{
		Table:     "document_chunks",
		Name:      "idx_document_chunks_keywords_gin",
		Method:    "gin",
		Columns:   []string{"keywords"},
		Predicate: "keywords IS NOT NULL",
		DDL: `CREATE INDEX IF NOT EXISTS idx_document_chunks_keywords_gin
			ON document_chunks USING gin (keywords) WHERE keywords IS NOT NULL`,
	}

	// 上下文加载链路高频索引
	contextIndexSpecs = []indexSpec{
		{
			// FindRecent / FindRecentForContext / SearchRecentByKeywords 的
			// WHERE session_id = ? ORDER BY created_at DESC LIMIT n
			Table:   "chat_messages",
			Name:    "idx_chat_messages_session_id_created_at",
			Method:  "btree",
			Columns: []string{"session_id", "created_at"},
			DDL:     `CREATE INDEX IF NOT EXISTS idx_chat_messages_session_id_created_at ON chat_messages (session_id, created_at)`,
		},
		{
			// 会话列表按用户 + 状态过滤
			Table:   "chat_sessions",
			Name:    "idx_chat_sessions_user_status",
			Method:  "btree",
			Columns: []string{"user_id", "status"},
			DDL:     `CREATE INDEX IF NOT EXISTS idx_chat_sessions_user_status ON chat_sessions (user_id, status)`,
		},
		{
			// ListActive 的 WHERE user_id = ? AND is_active = true ORDER BY updated_at DESC LIMIT n
			// 三列缺一不可：只建 (user_id, is_active) 的话 ORDER BY 那一步仍要排序。
			Table:   "user_memories",
			Name:    "idx_user_memories_user_active",
			Method:  "btree",
			Columns: []string{"user_id", "is_active", "updated_at"},
			DDL:     `CREATE INDEX IF NOT EXISTS idx_user_memories_user_active ON user_memories (user_id, is_active, updated_at DESC)`,
		},
	}

	// 用 OTel traceID 反查对话（在三方平台看到异常 trace 后回查本系统业务信息）
	chatTraceOTelIndexSpec = indexSpec{
		Table:   "chat_traces",
		Name:    "idx_chat_traces_otel_trace_id",
		Method:  "btree",
		Columns: []string{"otel_trace_id"},
		DDL:     `CREATE INDEX IF NOT EXISTS idx_chat_traces_otel_trace_id ON chat_traces (otel_trace_id)`,
	}
)

// indexSpec 声明「代码期望库里存在的一个索引」。
type indexSpec struct {
	Table     string   // 表名（public schema）
	Name      string   // 索引名，必须与建库基线一致
	Method    string   // 访问方法：btree / gin / ivfflat / hnsw
	Columns   []string // 键列，必须保序（列序决定它能服务哪些查询）
	Predicate string   // WHERE 子句文本，空表示无谓词
	Unique    bool     // 是否唯一索引
	DDL       string   // 确认缺失时执行的创建语句
}

// existingIndex 是库里已存在索引的定义快照。
type existingIndex struct {
	Name      string `gorm:"column:index_name"`
	Method    string `gorm:"column:am_name"`
	Columns   string `gorm:"column:key_columns"` // 逗号连接，保序
	Predicate string `gorm:"column:predicate"`
	Unique    bool   `gorm:"column:is_unique"`
	Def       string `gorm:"column:def"` // pg_get_indexdef 去掉索引名后的原文
}

// definition 取出可比对的「定义形状」。
func (s indexSpec) definition() indexDefinition {
	return indexDefinition{s.Method, s.Columns, s.Predicate, s.Unique}
}

func (e existingIndex) definition() indexDefinition {
	return indexDefinition{e.Method, splitColumns(e.Columns), e.Predicate, e.Unique}
}

// indexDefinition 是索引定义的「可比形状」。
type indexDefinition struct {
	Method    string
	Columns   []string
	Predicate string
	Unique    bool
}

// ensureIndex 确保 spec 描述的索引在库里可用，判据是「定义」而不是「名字」。
//
// 为什么不能按名字判存在（两类反向的静默事故）：
//   - 名字不同、定义等价 → 两边各建一份，白付双倍写放大（keywords GIN 的历史事故）；
//   - 名字相同、定义不同（列序/谓词/唯一性）→ 静默认为「已就绪」，
//     实际在跑的是另一个索引（user_memories 一度声明两列、库里却是三列）。
//
// 判据是「服务能力覆盖」（covers）：库里已有索引能服务相同查询就不再创建。
// 但覆盖不等于「一模一样」，所以三种情况分别出日志，让「不精确匹配」显性化：
//
//	同名同定义         → Info：已就绪
//	同名定义不同       → Warn：库里是什么、期望是什么（不自动重建，重建要人拍板）
//	异名但服务能力覆盖 → Warn：已有等价索引顶着，未重复创建（并点名冗余副本）
func ensureIndex(db *gorm.DB, spec indexSpec) error {
	existing, err := loadTableIndexes(db, spec.Table)
	if err != nil {
		return fmt.Errorf("查询 %s 的索引定义失败: %w", spec.Table, err)
	}

	// 1) 同名索引：名字被占了就只能看它够不够用，不能建第二个同名索引
	for _, e := range existing {
		if e.Name != spec.Name {
			continue
		}
		switch {
		case equivalent(e.definition(), spec.definition()):
			logger.Infof("[index] %s.%s 已就绪（%s）", spec.Table, spec.Name, e.Def)
		case covers(e.definition(), spec.definition()):
			logger.Warnf("[index] %s.%s 同名但定义与期望不一致（功能已被覆盖，故未重建）：库里=%s；期望=%s",
				spec.Table, spec.Name, e.Def, spec.describe())
		default:
			logger.Warnf("[index] %s.%s 同名但不是同一个索引，未自动重建（重建会顶掉现有索引，需人工判断）：库里=%s；期望=%s",
				spec.Table, spec.Name, e.Def, spec.describe())
		}
		warnRedundantIndexes(existing, e)
		return nil
	}

	// 2) 名字不同但已有索引能服务同一批查询（历史分叉留下的），不重复创建
	for _, e := range existing {
		if covers(e.definition(), spec.definition()) {
			logger.Warnf("[index] %s 上已有等价索引 %s（与期望的 %s 不同名），未重复创建；建议只保留一个以避免双倍写代价",
				spec.Table, e.Name, spec.Name)
			return nil
		}
	}

	// 3) 确实缺 → 建
	logger.Infof("[index] 创建索引 %s.%s（%s）", spec.Table, spec.Name, spec.describe())
	if err := db.Exec(spec.DDL).Error; err != nil {
		return err
	}
	logger.Infof("[index] 索引 %s 创建完成", spec.Name)
	return nil
}

// warnRedundantIndexes 报告与 keep 定义等价的其他索引：等价即可互换，留着两份只增加写代价。
func warnRedundantIndexes(all []existingIndex, keep existingIndex) {
	var others []string
	for _, o := range all {
		if o.Name != keep.Name && equivalent(o.definition(), keep.definition()) {
			others = append(others, o.Name)
		}
	}
	if len(others) == 0 {
		return
	}
	sort.Strings(others)
	logger.Warnf("[index] 索引 %s 存在定义完全相同的冗余副本：%s —— 每次写表都要维护多份，建议只保留 %s",
		keep.Name, strings.Join(others, "、"), keep.Name)
}

// tableIndexesSQL 读取某张表的全部索引定义。
//
// 键列用 unnest(indkey) WITH ORDINALITY 保留**真实列序**：btree 的列序决定它能服务
// 哪些查询，按字母排序会把 (a,b) 和 (b,a) 误判成同一个定义。
//
// ⚠️ 这段 SQL 里除表名那一个 `?` 之外**不能再出现 `?`**：GORM 的 db.Raw 会把每个 `?`
// 当参数占位符依次替换实参。曾经这里用正则 '(UNIQUE )?INDEX' 在 SQL 侧剥索引名，
// 那个 `?` 被吃掉、表名被塞进正则，整条 SQL 变成语法错误（SQLSTATE 42601），
// 而这个错误只有连真库才会暴露 —— 纯函数单测全绿。所以剥离动作改在 Go 侧做
// （stripIndexName），并由 TestTableIndexesSQLHasExactlyOnePlaceholder 守住这条约定。
const tableIndexesSQL = `
	SELECT i.relname AS index_name,
	       am.amname AS am_name,
	       COALESCE((SELECT string_agg(a.attname, ',' ORDER BY k.ord)
	                   FROM unnest(x.indkey) WITH ORDINALITY AS k(attnum, ord)
	                   JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = k.attnum), '') AS key_columns,
	       COALESCE(pg_get_expr(x.indpred, x.indrelid), '') AS predicate,
	       x.indisunique AS is_unique,
	       pg_get_indexdef(i.oid) AS def
	  FROM pg_index x
	  JOIN pg_class c ON c.oid = x.indrelid
	  JOIN pg_class i ON i.oid = x.indexrelid
	  JOIN pg_am am ON am.oid = i.relam
	  JOIN pg_namespace n ON n.oid = c.relnamespace
	 WHERE n.nspname = 'public' AND c.relname = ?
	 ORDER BY i.relname
`

func loadTableIndexes(db *gorm.DB, table string) ([]existingIndex, error) {
	var rows []existingIndex
	if err := db.Raw(tableIndexesSQL, table).Scan(&rows).Error; err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].Def = stripIndexName(rows[i].Def, rows[i].Name)
	}
	return rows, nil
}

// stripIndexName 把 pg_get_indexdef 原文里的索引名抠掉，便于日志对照两个定义。
// 索引名只会出现在开头 "CREATE [UNIQUE] INDEX <name> ON " 这一段，按该片段精确替换，
// 不会误伤出现在列名或谓词里的同名子串。
func stripIndexName(def, name string) string {
	return strings.Replace(def, "INDEX "+name+" ON ", "INDEX ON ", 1)
}

// equivalent 判断两个索引定义是否**等价到可以互换**：访问方法、键列（保序）、谓词、唯一性全同。
// 等价的两个索引互为冗余 —— 每个写操作都要同时维护两份，而查询只会选其中一份。
func equivalent(a, b indexDefinition) bool {
	return a.Method == b.Method &&
		strings.Join(a.Columns, ",") == strings.Join(b.Columns, ",") &&
		normalizePredicate(a.Predicate) == normalizePredicate(b.Predicate) &&
		a.Unique == b.Unique
}

// covers 判断已有索引 a 能否服务「目标索引 b 所服务的全部查询」。
//
// 规则（三条都必须成立）：
//  1. 访问方法属于同一族：ivfflat 与 hnsw 都是 pgvector 的近似最近邻索引，
//     服务的是同一类查询（ORDER BY 向量距离 LIMIT k），可互换；其余要求方法相同。
//  2. b 的键列是 a 键列的**前缀**（保序）。例：(user_id, is_active, updated_at) 覆盖
//     (user_id, is_active)；反过来不成立。
//  3. 谓词不弱于目标：b 要全量索引时 a 也必须是全量的（partial 索引少了谓词之外的行）；
//     b 只要 partial 时 a 是全量索引也能用。
//
// ⚠️ **刻意不比较 ASC/DESC**：btree 可以双向扫描，(a, b DESC) 与 (a, b) 在
// 「能给哪些查询省掉排序」上等价。若把排序方向纳入判据，两份等价索引会被判成
// 「不覆盖」→ 各建一份 → 正是本函数要防的重复。报账靠 equivalent()（它比的是严格定义），
// 两者分工：covers 决定「建不建」，equivalent 决定「是不是冗余、要不要点名」。
func covers(a, b indexDefinition) bool {
	if indexMethodFamily(a.Method) != indexMethodFamily(b.Method) {
		return false
	}
	// 唯一索引能覆盖非唯一需求；非唯一索引覆盖不了唯一约束
	if b.Unique && !a.Unique {
		return false
	}
	if len(b.Columns) > len(a.Columns) {
		return false
	}
	for i, col := range b.Columns {
		if a.Columns[i] != col {
			return false
		}
	}
	switch {
	case b.Predicate == "":
		return a.Predicate == ""
	case a.Predicate == "":
		return true
	default:
		return normalizePredicate(a.Predicate) == normalizePredicate(b.Predicate)
	}
}

// indexMethodFamily 把「服务同一类查询」的访问方法归为一族。
func indexMethodFamily(method string) string {
	switch strings.ToLower(method) {
	case "ivfflat", "hnsw":
		return "ann"
	default:
		return strings.ToLower(method)
	}
}

// normalizePredicate 把谓词归一化到可比较的形态。
// pg_get_expr 会补上外层括号（`(keywords IS NOT NULL)`），而声明里写的是
// `keywords IS NOT NULL`，所以去掉空白与外层括号后再比。
//
// ⚠️ 这是**文本**比较，不是语义比较：语义等价但写法不同的谓词（例如
// `is_active = true` 与 `is_active`）会被判成不同，从而可能多建一个索引 ——
// 此时 ensureIndex 会打 Warn 点名，不会静默。
func normalizePredicate(pred string) string {
	p := strings.ReplaceAll(pred, " ", "")
	p = strings.ReplaceAll(p, "(", "")
	p = strings.ReplaceAll(p, ")", "")
	return strings.ToLower(p)
}

// splitColumns 把逗号连接的键列串拆开；空串返回 nil。
func splitColumns(joined string) []string {
	if joined == "" {
		return nil
	}
	return strings.Split(joined, ",")
}

// describe 把声明渲染成可读形式，用于日志里对照「库里 vs 期望」。
func (s indexSpec) describe() string {
	uniq := ""
	if s.Unique {
		uniq = "UNIQUE "
	}
	pred := ""
	if s.Predicate != "" {
		pred = " WHERE " + s.Predicate
	}
	return fmt.Sprintf("%sUSING %s (%s)%s", uniq, s.Method, strings.Join(s.Columns, ","), pred)
}

// defSig 供测试与日志使用：把定义压成一个可比较的字符串。
func (d indexDefinition) defSig() string {
	return strings.Join([]string{
		strings.ToLower(d.Method),
		strings.Join(d.Columns, ","),
		normalizePredicate(d.Predicate),
		strconv.FormatBool(d.Unique),
	}, "|")
}
