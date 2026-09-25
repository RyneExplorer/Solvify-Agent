package database

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// 本文件守两件事：
//
//  1. **covers / equivalent 的语义**（纯函数）：谁是「服务能力覆盖」、谁是「定义等价」。
//     历史缺陷：索引是否存在按「名字」判断，于是
//     —— 名字不同、定义等价 → 两边各建一份，白付双倍写放大；
//     —— 名字相同、定义不同 → 静默认为「已就绪」，跑的是另一个索引。
//     判据改成「定义」之后，这两类事故都不可能再发生。
//
//  2. **代码声明的索引与建库基线不分叉**（静态交叉校验）。
//     基线只在首次创建空数据卷时执行，Ensure* 每次启动都跑；
//     两边名字只要有一处不同，空库首次启动就会同时存在两份等价索引。
//     所以这里直接解析 scripts/init_knowledge_schema.sql 对账，
//     而不是靠人读代码记着「这两个名字要一起改」。

// ---------------------------------------------------------------------------
// 1. covers / equivalent 语义
// ---------------------------------------------------------------------------

func def(method string, cols []string, pred string) indexDefinition {
	return indexDefinition{Method: method, Columns: cols, Predicate: pred}
}

// TestCoversKeyColumnsMustBeOrderedPrefix 键列判据是「保序前缀」：
// (user_id, is_active, updated_at) 能服务只查 (user_id, is_active) 的语句，
// 反过来不行；(a, b) 与 (b, a) 是两个不同的索引，不能互相顶替。
func TestCoversKeyColumnsMustBeOrderedPrefix(t *testing.T) {
	cases := []struct {
		name string
		have indexDefinition
		want indexDefinition
		ok   bool
	}{
		{
			name: "三列索引覆盖两列需求（user_memories 的真实形态）",
			have: def("btree", []string{"user_id", "is_active", "updated_at"}, ""),
			want: def("btree", []string{"user_id", "is_active"}, ""),
			ok:   true,
		},
		{
			name: "两列索引覆盖不了三列需求",
			have: def("btree", []string{"user_id", "is_active"}, ""),
			want: def("btree", []string{"user_id", "is_active", "updated_at"}, ""),
			ok:   false,
		},
		{
			name: "列序不同不能互相顶替",
			have: def("btree", []string{"created_at", "session_id"}, ""),
			want: def("btree", []string{"session_id", "created_at"}, ""),
			ok:   false,
		},
		{
			name: "完全相同的键列",
			have: def("btree", []string{"session_id", "created_at"}, ""),
			want: def("btree", []string{"session_id", "created_at"}, ""),
			ok:   true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := covers(c.have, c.want); got != c.ok {
				t.Fatalf("covers = %v，期望 %v（have=%s want=%s）", got, c.ok, c.have.defSig(), c.want.defSig())
			}
		})
	}
}

// TestCoversIgnoresSortDirection 排序方向**刻意不参与判据**。
//
// ⚠️ 这是本判据最容易被「改严」的地方，改严就会重新制造重复索引：
// btree 可以双向扫描，(session_id, created_at DESC)（代码声明）
// 与 (session_id, created_at)（建库基线）在「能给哪些查询省掉排序」上等价。
// indexDefinition 里没有方向字段，所以两者天然判为「等价」——
// 于是 covers 成立、ensureIndex 不会再去建第二份。
// 线上那一对 chat_messages 索引正是这个形状。
func TestCoversIgnoresSortDirection(t *testing.T) {
	// 两个索引从 pg_index 读回来时，键列名相同（方向不在键列名里）
	codeSide := def("btree", []string{"session_id", "created_at"}, "")
	baselineSide := def("btree", []string{"session_id", "created_at"}, "")

	if !covers(baselineSide, codeSide) {
		t.Fatal("同一组键列应判为相互覆盖（方向不参与判据）")
	}
	if !equivalent(codeSide, baselineSide) {
		t.Fatal("定义等价：否则 ensureIndex 会把它报成冗余之外的东西")
	}
}

// TestCoversPredicateNotWeaker 谓词判据：partial 索引服务不了全量需求，
// 全量索引能服务 partial 需求。
func TestCoversPredicateNotWeaker(t *testing.T) {
	full := def("gin", []string{"keywords"}, "")
	partial := def("gin", []string{"keywords"}, "keywords IS NOT NULL")

	if !covers(full, partial) {
		t.Error("全量索引应能服务 partial 需求")
	}
	if covers(partial, full) {
		t.Error("partial 索引少了谓词之外的行，不能服务全量需求")
	}
	if !covers(partial, def("gin", []string{"keywords"}, "(keywords IS NOT NULL)")) {
		t.Error("谓词只差外层括号（pg_get_expr 会补），应判为相同")
	}
	if covers(partial, def("gin", []string{"keywords"}, "keywords IS NOT NULL AND lang = 'zh'")) {
		t.Error("更严的谓词覆盖面更小，不能反向覆盖")
	}
}

// TestCoversMethodFamily ivfflat 与 hnsw 都是 pgvector 的近似最近邻索引，
// 服务同一类查询，应互相覆盖；btree 与 gin 则不能。
func TestCoversMethodFamily(t *testing.T) {
	ivf := def("ivfflat", []string{"embedding"}, "embedding IS NOT NULL")
	hnsw := def("hnsw", []string{"embedding"}, "embedding IS NOT NULL")

	if !covers(hnsw, ivf) || !covers(ivf, hnsw) {
		t.Error("ivfflat 与 hnsw 服务同一类查询，应互相覆盖")
	}
	if covers(def("gin", []string{"keywords"}, ""), def("btree", []string{"keywords"}, "")) {
		t.Error("gin 与 btree 不是一族，不能互相顶替")
	}
}

// TestCoversUniqueRule 唯一索引能服务非唯一需求，非唯一索引顶不了唯一约束。
func TestCoversUniqueRule(t *testing.T) {
	uniq := indexDefinition{Method: "btree", Columns: []string{"a"}, Unique: true}
	plain := indexDefinition{Method: "btree", Columns: []string{"a"}, Unique: false}

	if !covers(uniq, plain) {
		t.Error("唯一索引应能服务非唯一需求")
	}
	if covers(plain, uniq) {
		t.Error("非唯一索引不能顶替唯一约束")
	}
}

// TestEquivalentRequiresSameColumnsInOrder 等价是严格判据（列序也算），
// 它只用来「点名冗余副本」，不用来决定建不建索引 —— 两者分工不能混。
func TestEquivalentRequiresSameColumnsInOrder(t *testing.T) {
	a := def("btree", []string{"x", "y"}, "")
	if equivalent(a, def("btree", []string{"y", "x"}, "")) {
		t.Error("列序不同不是等价索引")
	}
	if equivalent(a, def("gin", []string{"x", "y"}, "")) {
		t.Error("访问方法不同不是等价索引")
	}
	if !equivalent(a, def("btree", []string{"x", "y"}, "")) {
		t.Error("完全相同应判为等价")
	}
}

func TestNormalizePredicate(t *testing.T) {
	// pg_get_expr 会补外层括号，声明里不写 —— 两者必须判为同一个谓词
	if normalizePredicate("(keywords IS NOT NULL)") != normalizePredicate("keywords IS NOT NULL") {
		t.Error("外层括号与空白不应影响谓词比较")
	}
	// 反过来：语义不同不能因为归一化而变得相同
	if normalizePredicate("keywords IS NOT NULL") == normalizePredicate("keywords IS NULL") {
		t.Error("归一化不能把不同谓词抹成同一个")
	}
}

func TestSplitColumns(t *testing.T) {
	if got := splitColumns(""); got != nil {
		t.Fatalf("空串应返回 nil，得到 %#v", got)
	}
	got := splitColumns("user_id,is_active,updated_at")
	if len(got) != 3 || got[0] != "user_id" || got[2] != "updated_at" {
		t.Fatalf("拆分结果不符：%#v", got)
	}
}

// TestTableIndexesSQLHasExactlyOnePlaceholder 守住一条约定：这段 SQL 里只能有一个 `?`。
//
// 背景（真库才暴露的缺陷）：GORM 的 db.Raw 把每个 `?` 当参数占位符。
// 初版在 SQL 侧用正则 '(UNIQUE )?INDEX \S+ ON ' 剥索引名，那个 `?` 被当占位符吃掉、
// 表名被塞进正则，整条 SQL 语法错误（SQLSTATE 42601），而纯函数单测全绿 ——
// 只有连真库的探针才发现。所以剥名改到 Go 侧（stripIndexName），SQL 不留第二个 `?`。
func TestTableIndexesSQLHasExactlyOnePlaceholder(t *testing.T) {
	if n := strings.Count(tableIndexesSQL, "?"); n != 1 {
		t.Fatalf("tableIndexesSQL 里有 %d 个 `?`，只允许 1 个（表名占位符）；"+
			"多出来的 `?` 会被 GORM 当占位符替换，把 SQL 弄成语法错误", n)
	}
}

// TestStripIndexName 剥索引名只能动开头那一段，不能误伤列名/谓词里的同名子串。
func TestStripIndexName(t *testing.T) {
	cases := []struct {
		def  string
		name string
		want string
	}{
		{
			def:  "CREATE INDEX idx_document_chunks_keywords_gin ON public.document_chunks USING gin (keywords) WHERE (keywords IS NOT NULL)",
			name: "idx_document_chunks_keywords_gin",
			want: "CREATE INDEX ON public.document_chunks USING gin (keywords) WHERE (keywords IS NOT NULL)",
		},
		{
			def:  "CREATE UNIQUE INDEX idx_x ON public.t USING btree (a)",
			name: "idx_x",
			want: "CREATE UNIQUE INDEX ON public.t USING btree (a)",
		},
	}
	for _, c := range cases {
		if got := stripIndexName(c.def, c.name); got != c.want {
			t.Errorf("stripIndexName:\n got %q\nwant %q", got, c.want)
		}
	}
	// 名字没出现在开头那一段时原样返回（不能误删别处的同名子串）
	def := "CREATE INDEX ON public.t USING gin (keywords)"
	if got := stripIndexName(def, "keywords"); got != def {
		t.Errorf("不应误伤列名里的同名子串，得到 %q", got)
	}
}

// ---------------------------------------------------------------------------
// 2. 代码声明的索引 vs 建库基线（静态交叉校验）
// ---------------------------------------------------------------------------

// baselineExempt 记录「基线里没有、只能靠启动期自愈补上」的索引。
// 每加一条都要写清原因 —— 否则这里会变成藏分叉的角落，把本文件的意义抹掉。
var baselineExempt = map[string]string{
	"idx_chat_sessions_user_status": "基线未收录（会话列表按 user_id + status 过滤），只能由启动期 EnsureContextIndexes 补建",
}

const baselineSQLPath = "../../scripts/init_knowledge_schema.sql"

// baselineIndex 是从建库基线里解析出的索引声明。
type baselineIndex struct {
	Name      string
	Table     string
	Method    string
	Columns   []string
	Predicate string
	Unique    bool
}

func (b baselineIndex) definition() indexDefinition {
	return indexDefinition{b.Method, b.Columns, b.Predicate, b.Unique}
}

// (?s) 让 . 跨行；列清单用非贪婪匹配到第一个 ')'（btree/gin 的列清单里没有嵌套括号）；
// 尾部（WITH (...) / WHERE ...）一并抓出来再单独找 WHERE。
var baselineIndexRe = regexp.MustCompile(
	`(?s)CREATE (UNIQUE )?INDEX "([^"]+)" ON "public"\."([^"]+)" USING (\w+) \((.*?)\)(.*?);`)

var baselineColumnRe = regexp.MustCompile(`"([^"]+)"`)

func parseBaselineIndexes(t *testing.T) []baselineIndex {
	t.Helper()
	raw, err := os.ReadFile(baselineSQLPath)
	if err != nil {
		t.Skipf("读不到建库基线 %s（可能在快照里跑），跳过交叉校验: %v", baselineSQLPath, err)
	}
	var out []baselineIndex
	for _, m := range baselineIndexRe.FindAllStringSubmatch(string(raw), -1) {
		idx := baselineIndex{
			Unique: m[1] != "",
			Name:   m[2],
			Table:  m[3],
			Method: m[4],
		}
		// 每段列声明的第一个引号标识符才是列名，后面的引号是 opclass
		// （"session_id" "pg_catalog"."uuid_ops" ASC NULLS LAST）
		for _, seg := range strings.Split(m[5], ",") {
			if col := baselineColumnRe.FindStringSubmatch(seg); col != nil {
				idx.Columns = append(idx.Columns, col[1])
			}
		}
		if tail := m[6]; tail != "" {
			if i := strings.Index(strings.ToUpper(tail), "WHERE"); i >= 0 {
				idx.Predicate = strings.TrimSpace(tail[i+len("WHERE"):])
			}
		}
		out = append(out, idx)
	}
	if len(out) == 0 {
		t.Fatalf("没从 %s 解析出任何索引，正则可能已失效", baselineSQLPath)
	}
	return out
}

// TestIndexSpecsDoNotDivergeFromBaseline 对账：代码声明的每个索引，
// 在建库基线里必须**存在同名的、定义逐字一致的**索引。
//
// 分三种失败形态，对应历史上真实发生过的三种分叉：
//   - 名字分叉：基线叫 idx_document_chunks_keywords_gin，代码去找
//     idx_document_chunks_keywords → 空库首次启动就有两份等价索引；
//   - 定义分叉：同名但列集/谓词不同（user_memories 一度声明两列 + partial，
//     基线建的是三列全量）→ 代码以为建了自己要的，实际跑的是另一个；
//   - 基线未收录：靠启动期 Ensure* 补建，必须登记进 baselineExempt 并写明原因。
func TestIndexSpecsDoNotDivergeFromBaseline(t *testing.T) {
	baseline := parseBaselineIndexes(t)
	byName := make(map[string]baselineIndex, len(baseline))
	for _, b := range baseline {
		byName[b.Name] = b
	}

	specs := []indexSpec{pgVectorIndexSpec, keywordsGINIndexSpec}
	specs = append(specs, contextIndexSpecs...)

	for _, spec := range specs {
		t.Run(spec.Name, func(t *testing.T) {
			// 1) 同名项：定义必须逐字一致
			if b, ok := byName[spec.Name]; ok {
				if b.Table != spec.Table {
					t.Fatalf("同名索引 %s 在基线里属于表 %s，代码却声明在 %s",
						spec.Name, b.Table, spec.Table)
				}
				if !equivalent(b.definition(), spec.definition()) {
					t.Fatalf("同名索引 %s 的定义与基线不一致：基线=%s；代码=%s",
						spec.Name, b.definition().defSig(), spec.definition().defSig())
				}
				return
			}

			// 2) 基线里若有能服务同一需求的**异名**索引 → 名字分叉，空库会同时存在两份
			var serving []string
			for _, b := range baseline {
				if b.Table == spec.Table && covers(b.definition(), spec.definition()) {
					serving = append(serving, b.Name)
				}
			}
			if len(serving) > 0 {
				t.Fatalf("基线里服务同一需求的是 %s，代码声明的却是 %s —— "+
					"名字分叉会让空库首次启动同时存在两份等价索引（keywords GIN 的历史事故）",
					strings.Join(serving, "、"), spec.Name)
			}

			// 3) 基线里没有 → 必须登记豁免
			if _, ok := baselineExempt[spec.Name]; !ok {
				t.Fatalf("%s（%s）在建库基线里没有，空库首次启动只能靠 Ensure* 补建；"+
					"若确属基线未收录，请登记进 baselineExempt 并写明原因",
					spec.Name, spec.describe())
			}
		})
	}
}

// TestBaselineHasNoDuplicateIndexes 基线自身也不该出现两份等价索引。
func TestBaselineHasNoDuplicateIndexes(t *testing.T) {
	baseline := parseBaselineIndexes(t)
	for i := range baseline {
		for j := i + 1; j < len(baseline); j++ {
			if baseline[i].Table != baseline[j].Table {
				continue
			}
			if equivalent(baseline[i].definition(), baseline[j].definition()) {
				t.Errorf("建库基线里 %s 上有两份等价索引：%s 与 %s —— "+
					"每个写操作都要维护两份，应只留一个",
					baseline[i].Table, baseline[i].Name, baseline[j].Name)
			}
		}
	}
}
