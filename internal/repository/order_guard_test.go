package repository

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// 本文件是「排序必须全序」这条规则的**唯一判据**，覆盖两类排序：
//
//	A. SQL 侧：GORM 的 `.Order("...")` 与 SQL 字符串里的 `ORDER BY`
//	B. Go 侧：`sort.Slice` / `sort.Sort` / `slices.SortFunc` 的比较函数
//
// ## 为什么要一个跨包扫描器
//
// 之前这条规则分两处守着：rag 包遍历 `chunkReadSQLs` 注册表、repository 包断言那一条
// `searchByKeywordSQL`。两者都是「**逐个出口登记**」—— 登记了才检查，没登记就没有覆盖。
// 结果：同一个缺陷在仓库里还剩 30 多处没被发现（`.Order("created_at DESC")` 的链式写法
// 连 `grep "\.Order("` 都扫不到，因为点号在上一行）。
//
// 这个扫描器改成**从源码自动发现**：
//   - 不依赖任何注册表 ⇒「漏登记」这个失效模式不存在；
//   - 递归覆盖 `internal/` 与 `pkg/` 下的全部非测试文件；
//   - 新增一条 `.Order("...")`、一段含 `ORDER BY` 的 SQL、或一个 `sort.Slice`，**自动进入检查范围**。
//
// 头一次运行就抓到了两处人工清点漏掉的位置（`pkg/database/index_guard.go` 的索引内省 SQL、
// `internal/tool/providers/mcp_pool.go` 的 LRU 淘汰）—— 这就是「自动发现」相对「人工登记」的价值。
//
// ## 判据
//
// A. 排序子句的**最后一个键**必须是唯一键（`id` 形态），允许末尾带 ASC/DESC —— 方向不影响
//    全序这个性质，把方向写死就是过度耦合。
// B. 比较函数必须**显式处理相等输入**（有分支）；`SliceStable` 天然满足。
//
// ## 为什么这件事值得守
//
// 排序键可并列时，「并列行的先后」不由 SQL 决定，而由 PG 的排序算法决定；
// 而 `LIMIT` 会换算法（7 行输入：3=top-N heapsort、≥4=quicksort，**执行计划文本完全相同**）。
// 实测只把候选池 6→3，gold 就从第 1 名掉到第 2 名 —— 而 gold 从未被切出候选池。
// ⇒ 排序非全序时，A/B 实验会把「排序算法换了」读成「这个配置有效果」。
//
// 分页（`Offset`+`Limit`）下的后果更直接：同一条记录可能出现在两页里，或者一页都进不去。

// orderSite 是一个被扫出来的排序表达式。
type orderSite struct {
	File   string // 相对仓库根，如 internal/repository/foo.go
	Line   int    // ORDER BY / Order() 那一行的真实行号
	Scope  string // 所在声明：SQL 常量名或「函数名」，用于白名单做稳定键
	Kind   string // "gorm" | "sql"
	Clause string // 排序子句（不含 ORDER BY 本身）
	Raw    string // 供诊断用的原文首行
	// Text 是**整段**字面量（Raw 只是首行）。判 JOIN 必须用整段：
	// 用首行会让 `SELECT ...` 之后的 `JOIN` 被截掉 ⇒ 断言恒真（踩过一次）。
	Text string
}

// exemptionKey 用「文件｜所在声明」而不是行号做键：行号会随无关编辑漂移，
// 每次漂移都要求重新登记 = 守卫变成噪声源，人就会开始无视它。
func exemptionKey(file, scope string) string { return file + "|" + scope }

// orderExemptions 是「排序键确实唯一、但唯一键不叫 id」的例外白名单。
//
// ⚠️ 必须为空或极少：加一条 = 承认这里用不了统一判据，理由要写清**为什么这个键一定唯一**。
// 有了这个列表，扫描器就不需要「登记制」—— 发现是自动的，例外是显式的。
var orderExemptions = map[string]string{
	exemptionKey("pkg/database/index_guard.go", "tableIndexesSQL"): "排序键是 `i.relname`（pg_class.relname）：**在同一个 schema 内索引名唯一**，" +
		"而这条 SQL 已用 `n.nspname = 'public'` 限定到单 schema ⇒ 本身已是全序；且该查询无 LIMIT，结果只用于打诊断日志。",
}

// goSortExemptions 是 Go 侧「比较函数没有显式处理相等输入」的例外白名单。
var goSortExemptions = map[string]string{
	exemptionKey("internal/tool/providers/mcp_pool.go", "(*MCPClientPool).sweep"): "连接池 LRU 淘汰：等空闲时淘汰哪一个都不影响正确性，补 tie-breaker 无意义。",
}

// scanScopes 遍历 internal/ 与 pkg/ 下每个非测试 .go 文件的每个顶层声明。
//
// ⚠️ 实测提醒：`internal/tool/providers/` 这类目录是**递归**扫到的 —— 早先按「写死几个目录」
// 做清点时漏掉了它，说明清点范围本身也得自动推导。
func scanScopes(t *testing.T, root string, visit func(rel, scope string, node ast.Node, fset *token.FileSet)) {
	t.Helper()
	for _, top := range []string{"internal", "pkg"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			f, pErr := parser.ParseFile(fset, path, nil, 0)
			if pErr != nil {
				return pErr
			}
			rel, rErr := filepath.Rel(root, path)
			if rErr != nil {
				return rErr
			}
			rel = filepath.ToSlash(rel)
			for _, decl := range f.Decls {
				switch d := decl.(type) {
				case *ast.FuncDecl:
					if d.Body == nil {
						continue
					}
					visit(rel, funcLabel(d), d.Body, fset)
				case *ast.GenDecl:
					for _, spec := range d.Specs {
						vs, ok := spec.(*ast.ValueSpec)
						if !ok {
							continue
						}
						visit(rel, valueSpecLabel(vs), vs, fset)
					}
				}
			}
			return nil
		})
		if err != nil {
			t.Fatalf("遍历 %s 失败: %v", top, err)
		}
	}
}

// funcLabel 给出「哪个函数」的稳定标签，如 `(*MCPClientPool).sweep` / `buildDocsContextBlock`。
func funcLabel(d *ast.FuncDecl) string {
	name := d.Name.Name
	if d.Recv == nil || len(d.Recv.List) == 0 {
		return name
	}
	t := d.Recv.List[0].Type
	if star, ok := t.(*ast.StarExpr); ok {
		t = star.X
	}
	if id, ok := t.(*ast.Ident); ok {
		return "(*" + id.Name + ")." + name
	}
	return "(?)." + name
}

func valueSpecLabel(vs *ast.ValueSpec) string {
	if len(vs.Names) == 0 {
		return "(unnamed)"
	}
	return vs.Names[0].Name
}

// TestAllOrderingsAreTotallyOrdered 扫描全仓的 SQL/GORM 排序表达式，断言都是全序。
func TestAllOrderingsAreTotallyOrdered(t *testing.T) {
	root := moduleRoot(t)
	var sites []orderSite
	scanScopes(t, root, func(rel, scope string, node ast.Node, fset *token.FileSet) {
		sites = append(sites, collectOrderSites(t, fset, rel, scope, node)...)
	})
	// 反空转：防止「扫描器整体失效」时测试静默变绿。不是精度断言，只是下限。
	if len(sites) < 30 {
		t.Fatalf("只扫到 %d 个排序表达式（预期 30+）—— 扫描器或路径出了问题", len(sites))
	}

	for _, s := range sites {
		where := s.File + ":" + strconv.Itoa(s.Line) + " [" + s.Scope + "]"
		if reason, ok := orderExemptions[exemptionKey(s.File, s.Scope)]; ok {
			if strings.TrimSpace(reason) == "" {
				t.Errorf("%s 在白名单里但没写理由", where)
			}
			continue
		}
		if !orderEndsWithUniqueKey(s.Clause) {
			t.Errorf("%s 的排序不是全序（并列行的先后会随 LIMIT / 分页变）:\n"+
				"    排序子句: %s\n"+
				"    原文: %s\n"+
				"    修法: 追加唯一键（单表用 `id`；有 JOIN 必须带表别名，如 `m.id` / `s.id` / `d.id`）\n"+
				"    若该键本身已唯一（如 relname），登记进 orderExemptions 并写明理由",
				where, s.Clause, s.Raw)
		}
		// 有 JOIN 的 SQL 里，裸 `id` 会在运行期报「column reference id is ambiguous」——
		// 静态断言看不出歧义，所以这里退一步：只要 JOIN 与 ORDER BY 落在**同一个** SQL 字面量里，
		// 末键就必须带限定（含 `.`）。
		// ⚠️ 已知盲区：JOIN 写在另一个字面量里、或用 GORM `Joins(...)` 拼的查询，这里看不出来。
		if s.Kind == "sql" && hasJoinKeyword(s.Text) && !strings.Contains(lastKey(s.Clause), ".") {
			t.Errorf("%s 含 JOIN 但排序末键是裸 `id` —— 运行期会报 column reference id is ambiguous:\n"+
				"    排序子句: %s", where, s.Clause)
		}
	}
}

// TestAllGoSortsHandleTies 扫描全仓的 `sort.Slice` 等调用，断言比较函数**显式处理相等输入**。
//
// 判据为什么是「有没有分支」而不是「结果是否可复现」：比较函数全序时任何排序算法同解，
// 所以只要并列有确定规则，输出就与算法无关。反过来，单键比较函数在并列时把决定权交给了
// 排序算法 —— 而 `SliceStable` 靠稳定性天然满足。
//
// ⚠️ 与 SQL 侧不同的一个事实：Go 侧若入参是**切片**，单键比较函数其实也是**确定**的
// （同一份输入 + 同一份算法 ⇒ 同一份输出），只是「同分谁在前」由算法而非规则决定。
// 所以这里守的是「规则显式」，不是「会不会变」—— 别把它当成「不修就有随机 bug」。
func TestAllGoSortsHandleTies(t *testing.T) {
	root := moduleRoot(t)
	found := 0
	sortNames := map[string]bool{"Slice": true, "SliceStable": true, "Sort": true, "SortStable": true}

	scanScopes(t, root, func(rel, scope string, node ast.Node, fset *token.FileSet) {
		ast.Inspect(node, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel == nil || !sortNames[sel.Sel.Name] {
				return true
			}
			found++
			where := rel + ":" + strconv.Itoa(fset.Position(call.Pos()).Line) + " [" + scope + "]"
			if reason, ok := goSortExemptions[exemptionKey(rel, scope)]; ok {
				if strings.TrimSpace(reason) == "" {
					t.Errorf("%s 在白名单里但没写理由", where)
				}
				return true
			}
			if sel.Sel.Name == "SliceStable" || sel.Sel.Name == "SortStable" {
				return true // 稳定排序天然保留相等元素的原顺序
			}
			if len(call.Args) < 2 {
				t.Errorf("%s 的 %s 调用缺少比较函数，扫描器看不到判据", where, sel.Sel.Name)
				return true
			}
			fn, ok := call.Args[len(call.Args)-1].(*ast.FuncLit)
			if !ok {
				t.Errorf("%s 的比较函数不是字面量 —— 扫描器看不到判据。"+
					"守卫必须能看到判据，请改成字面量或登记进 goSortExemptions", where)
				return true
			}
			if !hasTieBranch(fn) {
				t.Errorf("%s 的比较函数没有显式处理相等输入（单键比较）:\n"+
					"    并列时顺序由排序算法决定，不由规则决定。\n"+
					"    修法: 加一层 `if a.k != b.k { return a.k > b.k }` 再用唯一键（ID/id/idx）兜底；\n"+
					"    若确实不需要（如淘汰型语义），登记进 goSortExemptions 并写明理由", where)
			}
			return true
		})
	})
	if found < 6 {
		t.Fatalf("只扫到 %d 个 Go 侧排序调用（预期 6+）—— 扫描器或路径出了问题", found)
	}
}

// hasTieBranch 判断比较函数体里有没有显式分支（即有没有考虑过「两个键相等」）。
func hasTieBranch(fn *ast.FuncLit) bool {
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if _, ok := n.(*ast.IfStmt); ok {
			found = true
			return false
		}
		return true
	})
	return found
}

// orderEndsWithUniqueKey 判断排序子句是否以唯一键收尾。
func orderEndsWithUniqueKey(clause string) bool {
	k := lastKey(clause)
	if k == "" {
		return false
	}
	return uniqueKeyRe.MatchString(k)
}

// lastKey 取出排序子句的最后一个键（去掉 ASC/DESC 与 NULLS FIRST/LAST）。
func lastKey(clause string) string {
	parts := strings.Split(clause, ",")
	if len(parts) == 0 {
		return ""
	}
	k := strings.Join(strings.Fields(strings.TrimSpace(parts[len(parts)-1])), " ")
	for _, suffix := range []string{" NULLS FIRST", " NULLS LAST"} {
		if strings.HasSuffix(strings.ToUpper(k), suffix) {
			k = strings.TrimSpace(k[:len(k)-len(suffix)])
		}
	}
	for _, dir := range []string{" DESC", " ASC"} {
		if strings.HasSuffix(strings.ToUpper(k), dir) {
			k = strings.TrimSpace(k[:len(k)-len(dir)])
			break
		}
	}
	return k
}

// uniqueKeyRe 唯一键的形态：`id` / `dc.id` / `m.id`（可带表别名）。
// 不匹配 `created_at` / `score` / `name` / `version_no` 这类会并列的列 —— 它们要么真会并列，
// 要么「是否唯一」取决于约束而无法静态判定，都要求人工登记。
var uniqueKeyRe = regexp.MustCompile(`(?:^|[.])id$`)

func hasJoinKeyword(sqlText string) bool {
	return strings.Contains(strings.ToUpper(sqlText), "JOIN ")
}

// collectOrderSites 遍历一个声明（函数体 / var·const 初始值），找出所有 SQL/GORM 排序表达式。
func collectOrderSites(t *testing.T, fset *token.FileSet, file, scope string, node ast.Node) []orderSite {
	t.Helper()
	var out []orderSite
	seen := map[string]bool{}
	add := func(line int, kind, clause, raw, text string) {
		k := strconv.Itoa(line) + ":" + kind + ":" + clause
		if seen[k] {
			return
		}
		seen[k] = true
		out = append(out, orderSite{
			File: file, Line: line, Scope: scope, Kind: kind,
			Clause: strings.Join(strings.Fields(clause), " "),
			Raw:    raw, Text: text,
		})
	}

	ast.Inspect(node, func(n ast.Node) bool {
		switch nd := n.(type) {
		case *ast.CallExpr:
			sel, ok := nd.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel == nil || sel.Sel.Name != "Order" {
				return true
			}
			line := fset.Position(nd.Pos()).Line
			if len(nd.Args) != 1 {
				t.Errorf("%s:%d [%s] Order() 参数个数为 %d，扫描器只检查单参数字面量；"+
					"请改成字面量，或把本处登记进 orderExemptions", file, line, scope, len(nd.Args))
				return true
			}
			bl, ok := nd.Args[0].(*ast.BasicLit)
			if !ok || bl.Kind != token.STRING {
				t.Errorf("%s:%d [%s] Order() 的参数不是字符串字面量 —— 扫描器看不到它的排序键。"+
					"守卫必须能看到判据，请改成字面量", file, line, scope)
				return true
			}
			clause, uErr := strconv.Unquote(bl.Value)
			if uErr != nil {
				t.Errorf("%s:%d [%s] Order() 字面量无法解析: %v", file, line, scope, uErr)
				return true
			}
			add(line, "gorm", clause, clause, clause)
		case *ast.BasicLit:
			if nd.Kind != token.STRING {
				return true
			}
			text, uErr := strconv.Unquote(nd.Value)
			upper := strings.ToUpper(text)
			if uErr != nil || !strings.Contains(upper, "ORDER BY") {
				return true
			}
			clause := clauseAfterLastOrderBy(text)
			if clause == "" {
				return true
			}
			// ⚠️ 必须报 ORDER BY 所在行，不是字面量起始行：一段多行 SQL 里
			// 字面量起点常在 15 行之前，报错指错地方等于不可操作。
			start := fset.Position(nd.Pos()).Line
			line := start + strings.Count(text[:strings.LastIndex(upper, "ORDER BY")], "\n")
			add(line, "sql", clause, firstNonEmptyLine(text), text)
		}
		return true
	})

	sort.Slice(out, func(i, j int) bool { return out[i].Line < out[j].Line })
	return out
}

// clauseAfterLastOrderBy 取出最后一个 ORDER BY 到 LIMIT（或字面量结尾）之间的子句。
func clauseAfterLastOrderBy(sqlText string) string {
	upper := strings.ToUpper(sqlText)
	i := strings.LastIndex(upper, "ORDER BY")
	if i < 0 {
		return ""
	}
	rest := sqlText[i+len("ORDER BY"):]
	if j := strings.Index(strings.ToUpper(rest), "LIMIT"); j >= 0 {
		rest = rest[:j]
	}
	return strings.TrimSpace(rest)
}

// firstNonEmptyLine 取第一个非空行，避免报错信息里「原文: 」后面是空的。
func firstNonEmptyLine(s string) string {
	for _, ln := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(ln); t != "" {
			return t
		}
	}
	return ""
}

// moduleRoot 由本测试文件的位置反推仓库根（本文件在 internal/repository/）。
func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller 失败，无法定位仓库根")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}
