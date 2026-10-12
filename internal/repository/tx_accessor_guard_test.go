package repository

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"gorm.io/gorm"
)

// directDBUseRe 匹配「某接收者.db」形式的取句柄写法。
//
// 命中即意味着该方法绕过事务上下文：外层就算开了事务，它也拿的是仓库自己的连接池，
// 写进去的东西不会被回滚 —— 而且没有任何编译期或运行期提示。这是 P1-7 要堵死的路。
var directDBUseRe = regexp.MustCompile(`\b[a-zA-Z_][a-zA-Z0-9_]*\.db\b`)

// interfaceDeclRe 匹配接口声明；methodSigRe 匹配接口体内的方法签名（含首参数）。
//
// 判据只有一个：**方法的首参数是不是 ctx context.Context**。没有 ctx 的接口，
// 实现层即使想参与事务也拿不到事务句柄（无法从 ctx 里取 tx），天然「事务不感知」。
var (
	interfaceDeclRe = regexp.MustCompile(`(?m)^type\s+(\w+)\s+interface\s*\{`)
	methodSigRe     = regexp.MustCompile(`(?m)^\s*(\w+)\(\s*([^,)]*)`)
	// interfaceLineRe 用于「数一遍到底声明了几个接口」的完整性校验，
	// 因此刻意比 interfaceDeclRe 更宽松（也认 `type ( Foo interface { ... } )` 分组写法）
	interfaceLineRe = regexp.MustCompile(`(?m)^[ \t]*(?:type[ \t]+)?\w+[ \t]+interface[ \t]*\{`)
)

// txUnawareDebt 登记「接口签名没有 ctx、因此无法感知事务」的仓库（键＝接口名）。
//
// 这张表不是免死金牌：TestTxUnawareDebtMatchesInterfaceSignatures 会核对
// 「实际缺 ctx 的接口集合」与它**完全一致** —— 接口补上 ctx 后必须回来销账，
// 否则测试同样红。因此它只是一份「欠账声明」，不是可以随手加的白名单。
var txUnawareDebt = map[string]string{
	"UserRepository":           "FindByID/FindByUsername/Create/Update/... 均无 ctx 参数，改签名会波及全部调用方；不在任何跨仓库多写路径上，单独排期",
	"UserPreferenceRepository": "FindByUserID/Upsert/Update/DeleteByUserID 均无 ctx 参数；不在任何跨仓库多写路径上，单独排期",
}

// txUnawareInterfaces 从 *_interface.go 的签名推导「事务不感知」的接口集合（接口名 → 声明文件）。
// 推导而非硬编码：接口一旦补上 ctx，它会自动从集合里消失。
func txUnawareInterfaces(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob("*_interface.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("扫不到 *_interface.go：%v (files=%d)", err, len(files))
	}
	out := map[string]string{}
	total := 0
	declared := 0
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("读 %s 失败: %v", f, err)
		}
		src := string(b)
		// 完整性：文件里出现多少个接口声明行，就必须解析出多少个 ——
		// 防止有人用 type ( ... ) 分组声明，让新接口从守卫眼皮底下溜过去
		declared += len(interfaceLineRe.FindAllString(src, -1))
		for _, loc := range interfaceDeclRe.FindAllStringSubmatchIndex(src, -1) {
			total++
			name := src[loc[2]:loc[3]]
			body := src[loc[1]:]
			if end := strings.Index(body, "\n}"); end >= 0 {
				body = body[:end]
			}
			for _, m := range methodSigRe.FindAllStringSubmatch(body, -1) {
				if !strings.HasPrefix(strings.TrimSpace(m[2]), "ctx ") {
					out[name] = f
					break
				}
			}
		}
	}
	if total == 0 {
		t.Fatal("一个接口都没解析到（解析规则失效），守卫会静默失守")
	}
	if total != declared {
		t.Fatalf("接口文件里有 %d 个 interface 声明，只解析出 %d 个 —— "+
			"可能用了 type ( ... ) 分组写法，请让解析规则覆盖它，否则新接口会逃过守卫",
			declared, total)
	}
	return out
}

// repoInterfaceNames 返回所有仓库接口名 → 声明文件（以 Repo / Repository 结尾者）
func repoInterfaceNames(t *testing.T) map[string]string {
	t.Helper()
	files, _ := filepath.Glob("*_interface.go")
	out := map[string]string{}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("读 %s 失败: %v", f, err)
		}
		for _, m := range interfaceDeclRe.FindAllStringSubmatch(string(b), -1) {
			if strings.HasSuffix(m[1], "Repo") || strings.HasSuffix(m[1], "Repository") {
				out[m[1]] = f
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("没有解析到任何仓库接口，说明命名约定或解析规则变了，守卫会失效")
	}
	return out
}

// implFileOf 由 <x>_interface.go 推出实现文件名 <x>_repository.go。
// 依赖仓库层「接口与实现同名配对」的约定 —— 约定被破坏时断言会红，而不是静默漏查。
func implFileOf(interfaceFile string) string {
	return strings.Replace(interfaceFile, "_interface.go", "_repository.go", 1)
}

// TestTxUnawareDebtMatchesInterfaceSignatures 欠账表必须与实际签名一致（可增可销，但不能不符）
func TestTxUnawareDebtMatchesInterfaceSignatures(t *testing.T) {
	derived := txUnawareInterfaces(t)
	var missing, stale []string
	for name := range derived {
		if _, ok := txUnawareDebt[name]; !ok {
			missing = append(missing, name)
		}
	}
	for name, reason := range txUnawareDebt {
		if _, ok := derived[name]; !ok {
			stale = append(stale, name)
		}
		if strings.TrimSpace(reason) == "" {
			t.Errorf("%s 的欠账理由为空：登记必须说明为什么允许它不感知事务", name)
		}
	}
	sort.Strings(missing)
	sort.Strings(stale)
	if len(missing) > 0 {
		t.Errorf("以下接口签名缺 ctx（事务不感知）但未登记欠账: %v\n"+
			"→ 要么给接口方法补上 ctx 参数，要么登记欠账并说明理由", missing)
	}
	if len(stale) > 0 {
		t.Errorf("以下接口已不再是「事务不感知」，请从 txUnawareDebt 销账: %v", stale)
	}
}

// TestNoRepositoryBypassesTransactionContext 仓库实现不得直接取连接池。
//
// 这是 P1-7 的结构性守卫：只要有人写出 r.db.Xxx(...)，外层事务就会被静默绕过。
// 唯一豁免是「接口签名没有 ctx」的那两个仓库 —— 它们没得选，且已登记欠账。
func TestNoRepositoryBypassesTransactionContext(t *testing.T) {
	derived := txUnawareInterfaces(t)
	exempt := map[string]string{}
	for name := range txUnawareDebt {
		if f, ok := derived[name]; ok {
			exempt[implFileOf(f)] = name
		}
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读取仓库目录失败: %v", err)
	}
	hits := map[string][]string{}
	scanned := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		// tx.go 是 dbFor 的定义处，注释里正以 r.db 作为反例
		if name == "tx.go" {
			continue
		}
		if _, ok := exempt[name]; ok {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("读 %s 失败: %v", name, err)
		}
		scanned++
		for _, line := range strings.Split(string(b), "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			// 合法的 dbFor(ctx, r.db) 自身也含 "r.db"，先摘掉再找残留的裸取句柄
			residual := strings.ReplaceAll(line, "dbFor(ctx, r.db)", "")
			if directDBUseRe.MatchString(residual) {
				hits[name] = append(hits[name], trimmed)
			}
		}
	}
	if scanned < 10 {
		t.Fatalf("只扫到 %d 个仓库文件，路径或后缀规则可能变了，守卫形同虚设", scanned)
	}
	for _, k := range sortedKeys(hits) {
		t.Errorf("%s 直接取了连接池（会静默绕过外层事务），应改为 dbFor(ctx, r.db):\n    %s",
			k, strings.Join(hits[k], "\n    "))
	}
}

// TestTransactionalRepositoriesUseDBFor 事务感知的仓库必须真的走 dbFor
func TestTransactionalRepositoriesUseDBFor(t *testing.T) {
	unaware := txUnawareInterfaces(t)
	checked := 0
	for iface, ifaceFile := range repoInterfaceNames(t) {
		if _, ok := unaware[iface]; ok {
			continue
		}
		impl := implFileOf(ifaceFile)
		b, err := os.ReadFile(impl)
		if err != nil {
			t.Errorf("接口 %s 声明在 %s，但按约定找不到实现文件 %s —— 守卫的配对约定被破坏了",
				iface, ifaceFile, impl)
			continue
		}
		if !strings.Contains(string(b), "dbFor(ctx, r.db)") {
			t.Errorf("接口 %s 是事务感知的，但实现 %s 里没有 dbFor(ctx, r.db) —— 它无法加入外层事务",
				iface, impl)
		}
		checked++
	}
	if checked == 0 {
		t.Fatal("一个仓库实现都没检查到，守卫形同虚设")
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ─────────────── 事务句柄在 context 里的传递（不依赖数据库） ───────────────
//
// 说明：「InTx 内两次写真的落进同一个事务、失败真的全回滚」这条语义需要真实数据库
// 才能验证，由 test1/p17_tx_probe.go 的真库探针覆盖；这里只测不依赖 DB 的接线部分。

func TestTxHandleRoundTripsThroughContext(t *testing.T) {
	ctx := context.Background()
	if got := txFrom(ctx); got != nil {
		t.Fatalf("空白 ctx 不该带事务，得到 %v", got)
	}
	if got := txFrom(nil); got != nil {
		t.Fatalf("nil ctx 不该 panic、也不该带事务，得到 %v", got)
	}

	// marker 只用作指针，不会被解引用
	marker := new(gorm.DB)
	ctx = withTx(ctx, marker)
	if got := txFrom(ctx); got != marker {
		t.Fatalf("withTx / txFrom 未往返同一个句柄：期望 %p，实际 %p", marker, got)
	}
	// 派生 ctx 仍能取到（回调里常见的 WithTimeout / WithCancel 场景）
	derived, cancel := context.WithCancel(ctx)
	defer cancel()
	if got := txFrom(derived); got != marker {
		t.Fatalf("派生 ctx 丢失了事务句柄：期望 %p，实际 %p", marker, got)
	}
}

func TestTxManagerWithoutDBReturnsErrorInsteadOfSilentlyLosingAtomicity(t *testing.T) {
	m := &gormTxManager{}
	err := m.InTx(context.Background(), func(context.Context) error {
		t.Fatal("数据库连接缺失时不应该执行回调 —— 那会让调用方以为写操作是原子的")
		return nil
	})
	if err == nil {
		t.Fatal("TxManager 没有数据库连接时必须报错，而不是静默降级成非事务执行")
	}
	if !strings.Contains(err.Error(), "TxManager") {
		t.Fatalf("错误信息应指明是 TxManager 装配问题，实际: %v", err)
	}
}

func TestTxManagerWithNilCallbackIsNoop(t *testing.T) {
	m := NewTxManager(nil)
	if err := m.InTx(context.Background(), nil); err != nil {
		t.Fatalf("nil 回调应当直接返回 nil，实际: %v", err)
	}
}
