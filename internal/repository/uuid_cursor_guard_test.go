package repository

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"
	"testing"
)

// ─── uuid 列的 keyset 游标不能是空串 ──────────────────────────────────────
//
// 回归背景：历史消息向量回填的 FindMessagesMissingEmbedding 首屏用空串当游标，
// 生成 `WHERE ... AND id > ''`。id 是 uuid 列，PostgreSQL 直接报
// `invalid input syntax for type uuid: ""`（SQLSTATE 22P02）——
// 整轮回填一条都跑不了，而且**单测全绿**（测试替身做字符串比较，不认 uuid 类型）。
//
// 为什么单测抓不到、必须有这条守卫：
//   测试替身把 `id > afterID` 翻译成 Go 字符串比较，`"m10" < "m9"` 那类错乱能被测到，
//   但「PostgreSQL 不接受空串当 uuid」这件事只有真库知道。
//   ⇒ 凡是「判据依赖数据库类型语义」的缺陷，单测天生抓不到，只能靠形状守卫把它挡住。
//
// 扫描范围复用 order_guard 的 scanScopes（internal/ + pkg/ 全目录、非测试文件），
// 而不是自建登记表：登记表 = 登记了才查，「忘了登记」这个失效模式天生存在。
//
// ⚠️ 这类守卫读的是**磁盘源码**，`go test -overlay` 对它无效（overlay 只换编译输入，
// 解析器读的是真实文件）⇒ 验证它必须临时改磁盘再逐字节还原。

// uuidKeysetColumns 是以 uuid 为主键、参与 keyset 分页比较的列。
var uuidKeysetColumns = map[string]bool{
	"id":         true,
	"session_id": true,
}

func TestKeysetOnUuidColumnGuardsEmptyCursor(t *testing.T) {
	// 每处「uuid 列的 keyset 比较」都必须**落在某个「游标非空」判空的 If 体里**。
	//
	// ⚠️ 判据不能是「函数里存在判空」—— 那样只要函数别处有一个无关的 `if x != ""`
	// 就算过关。实测踩过：注入把 `Where("id > ?")` 挪回链式调用、判空却留着，
	// 守卫照样全绿 —— 它守的是「函数里有没有判空」，不是「这条比较有没有被判空包住」。
	type finding struct {
		where   string // 文件｜声明，稳定键
		compares int
		guarded  int // 落在判空 If 体里的比较数
	}
	var findings []finding

	scanScopes(t, moduleRoot(t), func(rel, scope string, node ast.Node, _ *token.FileSet) {
		body, ok := node.(*ast.BlockStmt)
		if !ok {
			return
		}
		f := finding{where: rel + "｜" + scope}

		ast.Inspect(body, func(n ast.Node) bool {
			ifs, ok := n.(*ast.IfStmt)
			if !ok || !isCursorNonEmptyGuard(ifs) {
				return true
			}
			ast.Inspect(ifs.Body, func(m ast.Node) bool {
				if lit, ok := m.(*ast.BasicLit); ok && isPlaceholderCompare(lit) {
					f.guarded++
					return false
				}
				return true
			})
			return true
		})
		ast.Inspect(body, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && isPlaceholderCompare(lit) {
				f.compares++
			}
			return true
		})

		if f.compares > 0 {
			findings = append(findings, f)
		}
	})

	if len(findings) == 0 {
		t.Fatal("一条 uuid 列的 keyset 比较都没扫到 —— 守卫本身失效（AST 模式过窄或代码大改）")
	}
	var unguarded []string
	total, guarded := 0, 0
	for _, f := range findings {
		total += f.compares
		guarded += f.guarded
		if f.guarded < f.compares {
			unguarded = append(unguarded, fmt.Sprintf("%s（%d/%d 处未被判空包住）", f.where, f.compares-f.guarded, f.compares))
		}
	}
	if len(unguarded) > 0 {
		t.Errorf("这些位置拿 uuid 列做 keyset 比较（`col > ?`），但比较不在任何「游标非空」判空的 If 体里：\n  %s\n"+
			"  首屏（空游标）会生成 `id > ''`，PostgreSQL 报 SQLSTATE 22P02（invalid input syntax for type uuid）",
			strings.Join(unguarded, "\n  "))
	}
	t.Logf("扫到 %d 处 uuid keyset 比较（分布在 %d 处声明），其中 %d 处落在判空 If 体里", total, len(findings), guarded)
}

// isPlaceholderCompare 判断是不是「与 SQL 占位符 ? 的比较」。
//
// ⚠️ 判据落在**SQL 字符串字面量**上，不是 Go 的比较表达式：
// GORM 写的是 Where("id > ?", afterID) —— 那是一根字符串，AST 里根本没有 `>` 节点。
// 最初按 AST 比较表达式写，结果一条都扫不到（守卫自己空转，还是绿的——
// 这类「模式过窄」的守卫最危险：它看起来在守着，实际什么都没看）。
func isPlaceholderCompare(lit *ast.BasicLit) bool {
	if lit.Kind != token.STRING {
		return false
	}
	sql := strings.Trim(lit.Value, "`\"")
	upper := strings.ToUpper(sql)
	// ⚠️ 不要要求字面量里含 WHERE：GORM 的 Where("id > ?", cur) 传的是**独立参数**，
	// 天然只有条件片段。多行 Raw SQL 才带 WHERE。
	// （初版加了这条 WHERE 判据 ⇒ 一条都扫不到，而测试因为「扫不到就 Fatal」报了红，
	//   属于自检起了作用；但若改成「扫不到就绿」，就是个彻底空转的守卫了。）
	for _, op := range []string{" > ?", " < ?", " >= ?", " <= ?"} {
		for col := range uuidKeysetColumns {
			if strings.Contains(upper, strings.ToUpper(col)+op) {
				return true
			}
		}
	}
	return false
}

func isEmptyStringLit(e ast.Expr) bool {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return false
	}
	return strings.Trim(lit.Value, "`\"") == ""
}

// isCursorNonEmptyGuard 判断这个 If 是不是「游标非空」的守卫：
// 形如 `if afterID != "" { ... }`。两侧写反（`if "" != afterID`）也认。
func isCursorNonEmptyGuard(ifs *ast.IfStmt) bool {
	cond, ok := ifs.Cond.(*ast.BinaryExpr)
	if !ok || cond.Op != token.NEQ {
		return false
	}
	return isEmptyStringLit(cond.Y) || isEmptyStringLit(cond.X)
}

