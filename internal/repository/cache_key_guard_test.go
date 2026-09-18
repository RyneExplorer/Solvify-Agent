package repository

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// ─── P1-5：仓库层 cache key 必须有且只有一个来源 ──────────────────────────────
//
// 缺陷模式：同一个 key 在「读」和「每一个失效点」各手写一遍（改动前 4 个文件共 18 处，
// tool_type 一个文件就写了 7 遍）。改 key 格式时漏掉一处，被漏掉的那条索引会一直返回
// **脏值**直到 TTL 过期，而且不报错 —— 与 P0-5「改名后旧 tool_key 索引没删」同源。
//
// 治法是结构性的：key 形态只允许定义在 cache_keys.go，读写两侧都只能调那些函数。
// 下面这条用例就是那个「结构」的守卫：谁再手写一个 key 字面量，它就红。

const cacheKeysFileName = "cache_keys.go"

// keyLiteralPrefixes 是本包缓存 key 的形态前缀（实体前缀由 app.go 的 cache.New 补）。
// 只列形态，是因为实体前缀不在仓库层。
var keyLiteralPrefixes = []string{"id:", "key:", "user:"}

// collectKeyLiterals 用 AST 取字符串字面量，避免把注释、变量名、SQL 片段误当 key。
func collectKeyLiterals(fset *token.FileSet, f *ast.File) []string {
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		v, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		for _, p := range keyLiteralPrefixes {
			if strings.HasPrefix(v, p) {
				out = append(out, fmt.Sprintf("%s  %q (第 %d 行)", fset.Position(lit.Pos()).Filename,
					v, fset.Position(lit.Pos()).Line))
				break
			}
		}
		return true
	})
	return out
}

func TestCacheKeysHaveSingleSource(t *testing.T) {
	fset := token.NewFileSet()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读取包目录失败: %v", err)
	}

	var (
		offenders  []string
		scanned    int
		defsInKeys int // cache_keys.go 里的 key 字面量个数，用来证明守卫本身有效
	)
	for _, e := range entries {
		name := e.Name()
		// 只看生产代码：测试里手写 key 是**独立口径**（key 格式真变了，测试也该跟着变）。
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("解析 %s 失败: %v", name, err)
		}
		scanned++
		lits := collectKeyLiterals(fset, f)
		if name == cacheKeysFileName {
			defsInKeys += len(lits)
			continue
		}
		offenders = append(offenders, lits...)
	}

	// 自证非空转：既确认扫到了文件，也确认 cache_keys.go 里**确实**有 key 定义
	// （否则说明前缀判定写错了，下面「没有 offender」的结论毫无意义）。
	if scanned == 0 {
		t.Fatal("守卫空转：没有扫描到任何非测试 .go 文件")
	}
	if defsInKeys == 0 {
		t.Fatalf("守卫空转：%s 里没有识别到任何 key 字面量（前缀判定可能已失效）", cacheKeysFileName)
	}
	if len(offenders) != 0 {
		t.Errorf("发现 %d 处手写的 cache key 字面量，请改调 %s 里的构造函数：\n  %s",
			len(offenders), cacheKeysFileName, strings.Join(offenders, "\n  "))
	}
}

// TestCacheKeyShapes 把缓存命名空间显式钉住。
//
// 这些字符串（加上 app.go 里 cache.New 的实体前缀）就是线上 Redis 的真实 key。
// 改动它们 = 改动已部署的命名空间：老 key 会在 TTL 内自然过期，期间多一次 cache miss
// （落库回填），不是正确性问题，但必须是有意为之 —— 所以放在这里做一次显式确认。
func TestCacheKeyShapes(t *testing.T) {
	cases := []struct {
		desc string
		got  string
		want string
	}{
		{"系统模型 / 主键", modelCacheKeyByID("m1"), "id:m1"},
		{"用户模型配置 / (主键,用户)", userModelConfigCacheKeyByID("c1", "u1"), "id:c1:u1"},
		{"工具类型 / 主键", toolTypeCacheKeyByID("t1"), "id:t1"},
		{"工具类型 / tool_key", toolTypeCacheKeyByToolKey("weather"), "key:weather"},
		{"用户工具配置 / 主键", userToolConfigCacheKeyByID("x1"), "id:x1"},
		{"用户工具配置 / 用户", userToolConfigCacheKeyByUser("u1"), "user:u1"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s: key=%q，期望 %q", c.desc, c.got, c.want)
		}
	}
}
