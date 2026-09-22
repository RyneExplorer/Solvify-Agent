package repository

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// ─── P0-5 守卫：有缓存的表，只能由它的「拥有者仓库」写 ──────────────────────────
//
// 缺陷长什么样：
//
//	user_tool_configs 由 cachedUserToolConfigRepository 缓存（tool:config:user:<uid>），
//	但 tool_provider_repository.Delete 里有一句
//	    tx.Delete(&entity.UserToolConfig{}, "provider_id = ?", id)
//	—— 它删了这张表，却不知道（也没能力知道）要失效谁的缓存。于是管理员删掉一个供应商后，
//	用户已启用的那个工具还会以「幽灵工具」形式出现在账号里，直到 10 分钟 TTL 过期。
//
// 为什么这是「一类」缺陷而不是两处笔误：
//
//	失效缓存的责任挂在「谁写谁负责」这条纪律上，而写这张表的地方可以随时新增
//	（P1-7 刚教会大家「跨仓库多写要走 InTx」，下一个仓库顺手加一句级联删除时，
//	 没人会想起缓存）。纪律拦不住人，结构才拦得住。
//
// 所以本守卫把规则收到「结构」这一层：
//
//	缓存表的写权属于拥有者仓库（X_repository.go 及其缓存装饰器 X_repository_cached.go）。
//	仓库包里其他任何文件写这张表 → 红灯。
//
// 与 P1-7 的 dbFor 收口是同一个思路：把「必须记得做的事」变成「没有第二条路可走」。
//
// ⚠️ 注册表**不是手写的**，而是从 *_repository_cached.go 派生（见 deriveCachedTableOwners）：
// 以后谁新增一个缓存仓库，它的表自动受保护，不需要有人想起来改这里。
// TestCachedTableOwnersDerivationIsNotVacuous 是配套自检：确保派生本身没退化。

// writeVerbs 是「会改数据」的方法名。宁可多收不可漏收——漏一个就等于守卫失效，
// 多收一个最多是让人来确认一句。
var writeVerbs = map[string]bool{
	"Create":        true,
	"Save":          true,
	"Delete":        true,
	"Update":        true,
	"Updates":       true,
	"UpdateColumn":  true,
	"UpdateColumns": true,
	"Exec":          true, // 裸 SQL 一律当写处理
	"Raw":           true,
}

// cachedTableOwner 描述「一张有缓存的表由谁负责写」。
type cachedTableOwner struct {
	Entity     string // 实体类型名，如 "UserToolConfig"
	OwnerFile  string // 拥有者仓库实现文件，如 "user_tool_config_repository.go"
	CachedFile string // 它的缓存装饰器文件，如 "user_tool_config_repository_cached.go"
}

func (o cachedTableOwner) String() string {
	return o.Entity + " (owner=" + o.OwnerFile + ")"
}

// deriveCachedTableOwners 从 *_repository_cached.go 反推「哪些实体被缓存了、归谁写」。
//
// 派生方式是：缓存装饰器必然有一个 Create(ctx, x *entity.X) 方法（它要建实体），
// 从它的第二个参数就能读出实体类型 X；拥有者文件则是把 "_cached" 去掉。
// 这样注册表随代码自动更新，不会因为有人忘了改守卫而留下缺口。
func deriveCachedTableOwners(t *testing.T) []cachedTableOwner {
	t.Helper()

	cachedFiles, err := filepath.Glob("*_repository_cached.go")
	if err != nil {
		t.Fatalf("列缓存仓库文件失败: %v", err)
	}
	if len(cachedFiles) == 0 {
		t.Fatal("一个 *_repository_cached.go 都没找到——说明工作目录不是 internal/repository，守卫会空转")
	}

	var owners []cachedTableOwner
	for _, cached := range cachedFiles {
		entity := entityTypeFromCreateParam(t, cached)
		if entity == "" {
			t.Errorf("%s: 无法从 Create 方法的参数里读出实体类型；"+
				"缓存仓库的形状变了，请更新 deriveCachedTableOwners", cached)
			continue
		}
		owners = append(owners, cachedTableOwner{
			Entity:     entity,
			OwnerFile:  strings.TrimSuffix(cached, "_cached.go") + ".go",
			CachedFile: cached,
		})
	}
	sort.Slice(owners, func(i, j int) bool { return owners[i].Entity < owners[j].Entity })
	return owners
}

// entityTypeFromCreateParam 在缓存装饰器里找到 Create 方法，取第二个参数的 *entity.X 的 X。
func entityTypeFromCreateParam(t *testing.T, filename string) string {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, nil, 0)
	if err != nil {
		t.Fatalf("解析 %s 失败: %v", filename, err)
	}

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name == nil || fn.Name.Name != "Create" {
			continue
		}
		if fn.Type.Params == nil || len(fn.Type.Params.List) < 2 {
			continue
		}
		// 第二个参数：config *entity.UserToolConfig
		typeExpr := fn.Type.Params.List[1].Type
		star, ok := typeExpr.(*ast.StarExpr)
		if !ok {
			continue
		}
		sel, ok := star.X.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "entity" {
			continue
		}
		return sel.Sel.Name
	}
	return ""
}

// TestCachedTableOwnersDerivationIsNotVacuous 是守卫的守卫。
//
// 如果派生逻辑退化（比如哪天缓存仓库的 Create 签名变了、glob 写错了），
// 下面的写权守卫就会「扫了 0 张表 = 一切正常」，看起来全绿却毫无保护。
// 这条把当前事实钉住：这 4 张表必须被派出来。
func TestCachedTableOwnersDerivationIsNotVacuous(t *testing.T) {
	got := map[string]string{}
	for _, o := range deriveCachedTableOwners(t) {
		got[o.Entity] = o.OwnerFile
	}

	want := map[string]string{
		"Model":           "model_repository.go",
		"ToolType":        "tool_type_repository.go",
		"UserModelConfig": "user_model_config_repository.go",
		"UserToolConfig":  "user_tool_config_repository.go",
	}

	for entity, ownerFile := range want {
		if got[entity] != ownerFile {
			t.Errorf("派生的缓存表归属不对：%s → %q，期望 %q", entity, got[entity], ownerFile)
		}
	}
	for entity := range got {
		if _, ok := want[entity]; !ok {
			t.Errorf("出现了未登记的缓存表 %s。请确认它确实只有拥有者仓库在写，"+
				"然后把它加进 want（这条断言本身就是提醒）。", entity)
		}
	}
}

// TestNoRepositoryWritesCachedTableOutsideItsOwner 是 P0-5 的核心守卫。
//
// 对每一张有缓存的表：仓库包里除拥有者（及缓存装饰器）外的任何文件，
// 只要对其实体发起写操作，就报错——因为那里的写不会失效缓存。
func TestNoRepositoryWritesCachedTableOutsideItsOwner(t *testing.T) {
	owners := deriveCachedTableOwners(t)

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("列仓库包文件失败: %v", err)
	}

	// 实体名 → 谁有权写
	allowed := map[string]map[string]bool{}
	for _, o := range owners {
		allowed[o.Entity] = map[string]bool{o.OwnerFile: true, o.CachedFile: true}
	}

	var violations []string
	scanned := 0

	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		scanned++

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("解析 %s 失败: %v", path, err)
		}

		parents := parentMap(file)

		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			entity := entityNameOfLiteral(lit)
			if entity == "" {
				return true
			}
			// 只关心**有缓存**的表：没有缓存层的表，跨仓库写是合法的
			// （比如 sync_repository 顺手更新 knowledge_bases 的状态字段）。
			allowedFor, isCached := allowed[entity]
			if !isCached {
				return true
			}
			if allowedFor[path] {
				return true
			}

			stmt := enclosingStmt(lit, parents)
			if stmt == nil {
				return true
			}
			verb, isWrite := stmtWriteVerb(stmt)
			if !isWrite {
				return true
			}

			pos := fset.Position(lit.Pos())
			violations = append(violations, formatViolation(path, pos.Line, entity, verb))
			return true
		})
	}

	// 防呆：扫描文件数为 0 时，「没发现问题」毫无意义。
	if scanned < 5 {
		t.Fatalf("只扫到 %d 个非测试文件，工作目录疑似不对，守卫不可信", scanned)
	}

	if len(violations) > 0 {
		sort.Strings(violations)
		t.Errorf("发现 %d 处「跨仓库写缓存表」——这些写不会失效缓存，会留下脏值到 TTL 过期：\n\n%s\n\n"+
			"修法（P0-5）：把这张表的删除/写入收口到它的拥有者仓库，让 service 用 InTx 编排调用；\n"+
			"禁止在这里直接操作该实体。",
			len(violations), strings.Join(violations, "\n"))
	}
}

func formatViolation(path string, line int, entity, verb string) string {
	return "  " + path + ":" + itoa(line) + "  " + verb + "(... &entity." + entity + "{} ...)"
}

// itoa 避免为一行格式化引入 strconv 依赖噪音（本文件已足够长）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf []byte
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		return "-" + string(buf)
	}
	return string(buf)
}

// entityNameOfLiteral 若字面量是 entity.X{...}（或 []entity.X{...} 的元素）则返回 X。
func entityNameOfLiteral(lit *ast.CompositeLit) string {
	sel, ok := lit.Type.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "entity" {
		return ""
	}
	return sel.Sel.Name
}

// parentMap 建「子节点 → 父节点」映射（最近一次 AST 遍历建立，供向上查找用）。
func parentMap(root ast.Node) map[ast.Node]ast.Node {
	parents := map[ast.Node]ast.Node{}
	var stack []ast.Node

	ast.Inspect(root, func(n ast.Node) bool {
		if n == nil {
			// Inspect 在离开节点时会再回调一次 nil
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			return true
		}
		if len(stack) > 0 {
			parents[n] = stack[len(stack)-1]
		}
		stack = append(stack, n)
		return true
	})
	return parents
}

// enclosingStmt 返回包含 n 的最内层语句。用语句（而不是单个调用）做粒度，
// 是因为 gorm 的写操作是链式的：
//
//	db.Model(&entity.X{}).Where(...).Delete(...)
//
// 字面量挂在 Model(...) 上，写动词在链条末端的 Delete(...) 上——只看「字面量的直接
// 父调用」会漏掉这条链。语句粒度能覆盖整条链。
func enclosingStmt(n ast.Node, parents map[ast.Node]ast.Node) ast.Node {
	for cur := n; cur != nil; cur = parents[cur] {
		if _, ok := cur.(ast.Stmt); ok {
			return cur
		}
	}
	return nil
}

// stmtWriteVerb 判断整条语句里是否出现写动词，返回命中的那一个。
func stmtWriteVerb(stmt ast.Node) (string, bool) {
	found := ""
	ast.Inspect(stmt, func(n ast.Node) bool {
		if found != "" {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if writeVerbs[sel.Sel.Name] {
			found = sel.Sel.Name
			return false
		}
		return true
	})
	return found, found != ""
}
