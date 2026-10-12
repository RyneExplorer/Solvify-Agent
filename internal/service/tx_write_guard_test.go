package service

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// ─────────────────────────────────────────────────────────────────────────────
// 守卫：服务层「一个函数里写两个及以上仓库」时，所有写必须落在同一个事务里
//
// 背景（P1-7）：仓库方法各自持有连接池，service 层把一个业务操作拆成两次跨仓库的
// 写，中间失败就留下永久不一致 —— 例如「先删消息、再删会话」，第二步失败则消息没了、
// 会话还在，用户看得见会话点进去却是空的。这类缺陷此前没有任何表达方式，全靠人记得
// 开事务，而 service 层根本拿不到 *gorm.DB。
//
// 现在有了 TxManager.InTx（回调收到带事务的 ctx，仓库经 dbFor 自动加入该事务），
// 本文件负责让「忘了用」变成红灯而不是静默脏数据。
// ─────────────────────────────────────────────────────────────────────────────

// readVerbPrefixes 只读语义的动词前缀。
//
// ⚠️ 分类刻意 fail-closed：**只有明确是读的才算读，其余一切方法调用都当作可能改状态**。
// 于是像 DeactivateByContent / Finish / ReindexVersion 这类不易归类的写方法不会被漏掉。
// 代价是新增的读方法若不叫 Find/Get/List/... 会被当成写（可能多报）——
// 宁可多报，不可漏报：多报只会要求你改个名或登记例外，漏报就是脏数据。
var readVerbPrefixes = []string{
	"Find", "Get", "List", "Count", "Exists", "Search", "Sum", "Admin", "Query",
}

// multiRepoWriteAllowed 登记「确实需要写多个仓库、但不需要同事务」的例外。
//
// 这张表不是免死金牌：TestMultiRepoWriteRegistryIsComplete 会核对
// 「实际被判定为多仓库写的函数集合」与它完全一致 —— 函数消失了必须销账，
// 且每一条都必须写明为什么这些写不需要同生共死。
//
// 判据：这些写之间**没有「一条成立就必须另一条也成立」的约束**。
// 反过来，只要存在「A 写了 B 就必须写」的关系，就必须进 InTx，不能登记在这里。
var multiRepoWriteAllowed = map[string]string{
	"document_service.go:documentService.runProcessJob": "documentJobRepo.MarkRunning 与 documentRepo.MarkProcessFailed 是**补偿**关系：" +
		"后者只在前者失败之后才执行。若强行放进同一事务，MarkProcessFailed 会被一起回滚，" +
		"恰好丢掉我们最需要的失败记录 —— 这类「失败后补偿」不能同事务。",

	"sync_service.go:syncService.runJob": "syncJobRepo.MarkRunning/Finish 记录的是「这一次同步运行」，" +
		"syncSourceRepo.MarkSyncResult 记录的是「数据源的同步结果」，分属两张表两个语义，" +
		"各自独立提交、失败可分别重试。放进同一事务会让任一次状态写入失败拖垮整轮同步的既有结果。",
}

var (
	ifaceDeclRe = regexp.MustCompile(`(?m)^type\s+(\w+)\s+interface\s*\{`)
	ifaceMethRe = regexp.MustCompile(`(?m)^\s*(\w+)\(`)
	firstParRe  = regexp.MustCompile(`(?m)^\s*(\w+)\(\s*([^,)]*)`)
)

type repoFacts struct {
	interfaces map[string]bool // 所有仓库接口名（以 Repo / Repository 结尾）
	unaware    map[string]bool // 事务不感知的仓库接口（有方法的首参数不是 ctx）
}

func loadRepoFacts(t *testing.T) repoFacts {
	t.Helper()
	dir := filepath.Join("..", "repository")
	files, err := filepath.Glob(filepath.Join(dir, "*_interface.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("扫不到仓库接口文件: %v (files=%d)", err, len(files))
	}
	facts := repoFacts{interfaces: map[string]bool{}, unaware: map[string]bool{}}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("读 %s 失败: %v", f, err)
		}
		src := string(b)
		for _, loc := range ifaceDeclRe.FindAllStringSubmatchIndex(src, -1) {
			name := src[loc[2]:loc[3]]
			if !strings.HasSuffix(name, "Repo") && !strings.HasSuffix(name, "Repository") {
				continue
			}
			facts.interfaces[name] = true
			body := src[loc[1]:]
			if end := strings.Index(body, "\n}"); end >= 0 {
				body = body[:end]
			}
			for _, m := range firstParRe.FindAllStringSubmatch(body, -1) {
				if !strings.HasPrefix(strings.TrimSpace(m[2]), "ctx ") {
					facts.unaware[name] = true
					break
				}
			}
		}
	}
	if len(facts.interfaces) < 10 {
		t.Fatalf("只解析出 %d 个仓库接口 —— 解析规则可能已失效，守卫会静默失守", len(facts.interfaces))
	}
	return facts
}

func isWriteMethod(name string) bool {
	for _, p := range readVerbPrefixes {
		if strings.HasPrefix(name, p) {
			return false
		}
	}
	return true
}

// ───────────────────────────── AST 分析 ─────────────────────────────

type funcAnalysis struct {
	key string
	// 该函数所在文件的仓库字段映射（字段名 → 仓库接口名），用于把字段还原成接口
	repoFields map[string]string
	// 写操作命中的仓库字段 → 方法名集合
	writesInTx    map[string]map[string]bool
	writesOutside map[string]map[string]bool
	// InTx 闭包内用到的所有仓库字段（含只读调用），用于「事务不感知仓库」检查
	fieldsUsedInTx map[string]map[string]bool
	// txCtxMisuse 记录「在 InTx 闭包内、却没把闭包收到的 ctx 传给仓库调用」的位置
	txCtxMisuse []string
	// txCtxChecked 累计已核对过 ctx 参数的「InTx 闭包内仓库调用」数，用于守卫非空转自证
	txCtxChecked int
}

func newFuncAnalysis(key string, repoFields map[string]string) *funcAnalysis {
	return &funcAnalysis{
		key:            key,
		repoFields:     repoFields,
		writesInTx:     map[string]map[string]bool{},
		writesOutside:  map[string]map[string]bool{},
		fieldsUsedInTx: map[string]map[string]bool{},
	}
}

func addField(m map[string]map[string]bool, field, method string) {
	if m[field] == nil {
		m[field] = map[string]bool{}
	}
	m[field][method] = true
}

func fieldNames(m map[string]map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// buildParents 记录 AST 的父子关系（ast.Inspect 会在退出节点时回调 nil，据此维护栈）
func buildParents(f *ast.File) map[ast.Node]ast.Node {
	parents := map[ast.Node]ast.Node{}
	var stack []ast.Node
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return false
		}
		if len(stack) > 0 {
			parents[n] = stack[len(stack)-1]
		}
		stack = append(stack, n)
		return true
	})
	return parents
}

// isInTxFuncLit 判断一个函数字面量是否是 s.txMgr.InTx(ctx, func(ctx) error {...}) 的回调
func isInTxFuncLit(parents map[ast.Node]ast.Node, n ast.Node) bool {
	fl, ok := n.(*ast.FuncLit)
	if !ok {
		return false
	}
	call, ok := parents[fl].(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return sel.Sel.Name == "InTx"
}

func insideInTx(parents map[ast.Node]ast.Node, n ast.Node) bool {
	for cur := parents[n]; cur != nil; cur = parents[cur] {
		if isInTxFuncLit(parents, cur) {
			return true
		}
	}
	return false
}

// enclosingInTxFuncLit 返回包住该节点的**最内层** InTx 回调
func enclosingInTxFuncLit(parents map[ast.Node]ast.Node, n ast.Node) (*ast.FuncLit, bool) {
	for cur := parents[n]; cur != nil; cur = parents[cur] {
		if fl, ok := cur.(*ast.FuncLit); ok && isInTxFuncLit(parents, fl) {
			return fl, true
		}
	}
	return nil, false
}

// inTxCtxParamName 取 InTx 回调的 ctx 参数名。
//
// 第二个返回值为 false 表示「拿不到可用的参数名」（首参未命名，或名字是 `_`）——
// 这种写法结构上无法把事务 ctx 传下去，必须红灯：它跟「忘了传」是同一个缺陷。
func inTxCtxParamName(fl *ast.FuncLit) (string, bool) {
	ft := fl.Type
	if ft == nil || ft.Params == nil || len(ft.Params.List) == 0 {
		return "", false
	}
	p := ft.Params.List[0]
	if len(p.Names) == 0 {
		return "", false
	}
	name := p.Names[0].Name
	if name == "_" || name == "" {
		return "", false
	}
	return name, true
}

// identTokens 收集表达式里出现的全部标识符名。
//
// 刻意按**词法边界**取词，不做子串匹配：否则 `otherCtx` 会因为「包含 ctx」而冒充
// 事务 ctx，守卫就白设了。
func identTokens(e ast.Expr) map[string]bool {
	out := map[string]bool{}
	ast.Inspect(e, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			out[id.Name] = true
		}
		return true
	})
	return out
}

// checkTxCtxArg 判断「InTx 闭包内的一次仓库调用」有没有把闭包收到的事务 ctx 传下去。
//
// 这是本组守卫最容易漏、也最容易被写回来的一条：insideInTx 只判断调用**词法上**在不在
// InTx 闭包里，看不出传的是哪个 ctx。于是下面这种写法会以「写都在 InTx 内」的样子
// 骗过全部检查——
//
//	s.txMgr.InTx(ctx, func(txCtx context.Context) error {
//	    s.messageRepo.DeleteBySessionID(ctx, sessionID) // 外层 ctx：这次写根本没进事务
//	    return s.sessionRepo.Delete(ctx, sessionID)     // 同样没进事务
//	})
//
// ——而它正是 P1-7 要修的那个缺陷（两次写各自提交，中间失败留下永久不一致）。
// 允许传派生 ctx（如 context.WithTimeout(txCtx, ...)），只要标识符出现在参数表达式里。
func checkTxCtxArg(call *ast.CallExpr, fl *ast.FuncLit) (string, bool) {
	param, ok := inTxCtxParamName(fl)
	if !ok {
		return "InTx 回调的首参数未命名或为 `_`，结构上无法把事务 ctx 传给仓库调用", false
	}
	if len(call.Args) == 0 {
		// 该方法不收 ctx（事务不感知的仓库），由「事务不感知仓库不得进事务」那条检查负责
		return "", true
	}
	toks := identTokens(call.Args[0])
	if len(toks) == 0 || (len(toks) == 1 && toks["nil"]) {
		// 首参不是 ctx 形态（字面量 / nil），不在本检查范围
		return "", true
	}
	if !toks[param] {
		return "仓库调用没有传 InTx 回调收到的 ctx（参数名 " + param + "），这次写会绕过事务", false
	}
	return "", true
}

// repoFieldsOf 收集该文件里「服务结构体的仓库字段」：字段名 → 仓库接口名
func repoFieldsOf(f *ast.File, facts repoFacts) map[string]string {
	out := map[string]string{}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, field := range st.Fields.List {
				if len(field.Names) != 1 {
					continue
				}
				sel, ok := field.Type.(*ast.SelectorExpr)
				if !ok {
					continue
				}
				pkg, ok := sel.X.(*ast.Ident)
				if !ok || pkg.Name != "repository" || !facts.interfaces[sel.Sel.Name] {
					continue
				}
				out[field.Names[0].Name] = sel.Sel.Name
			}
		}
	}
	return out
}

// analyzeFunc 收集一个函数体内「跨仓库写」的情况
func analyzeFunc(decl *ast.FuncDecl, parents map[ast.Node]ast.Node, repoFields map[string]string) *funcAnalysis {
	recv := ""
	if decl.Recv != nil && len(decl.Recv.List) > 0 {
		switch t := decl.Recv.List[0].Type.(type) {
		case *ast.StarExpr:
			if id, ok := t.X.(*ast.Ident); ok {
				recv = id.Name + "."
			}
		case *ast.Ident:
			recv = t.Name + "."
		}
	}
	a := newFuncAnalysis(recv+decl.Name.Name, repoFields)

	ast.Inspect(decl, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		// 形如 s.<仓库字段>.<方法>()
		fieldSel, ok := sel.X.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		fieldName := fieldSel.Sel.Name
		if _, isRepoField := repoFields[fieldName]; !isRepoField {
			return true
		}
		method := sel.Sel.Name
		inTx := insideInTx(parents, call)
		if inTx {
			// 词法上在 InTx 里还不够：必须核对它传的是**闭包收到的那个 ctx**。
			if fl, ok := enclosingInTxFuncLit(parents, call); ok {
				a.txCtxChecked++
				if msg, good := checkTxCtxArg(call, fl); !good {
					a.txCtxMisuse = append(a.txCtxMisuse, fieldName+"."+method+"(): "+msg)
				}
			}
		}
		if isWriteMethod(method) {
			if inTx {
				addField(a.writesInTx, fieldName, method)
			} else {
				addField(a.writesOutside, fieldName, method)
			}
		}
		if inTx {
			addField(a.fieldsUsedInTx, fieldName, method)
		}
		return true
	})
	return a
}

// scanServicePackage 解析服务层所有非测试文件，返回「文件名:接收者.函数」→ 分析结果
func scanServicePackage(t *testing.T, facts repoFacts) map[string]*funcAnalysis {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("扫不到服务层文件: %v", err)
	}
	fset := token.NewFileSet()
	out := map[string]*funcAnalysis{}
	repoFieldTotal := 0
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("解析 %s 失败: %v", path, err)
		}
		repoFields := repoFieldsOf(f, facts)
		repoFieldTotal += len(repoFields)
		parents := buildParents(f)
		base := filepath.Base(path)
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			if len(repoFields) == 0 {
				continue
			}
			a := analyzeFunc(fd, parents, repoFields)
			key := base + ":" + a.key
			if _, dup := out[key]; dup {
				t.Fatalf("函数标识 %s 重复：请让键包含更多信息，否则例外登记会指错对象", key)
			}
			out[key] = a
		}
	}
	if repoFieldTotal < 10 {
		t.Fatalf("只识别出 %d 个仓库字段 —— 字段解析失效，守卫会静默失守", repoFieldTotal)
	}
	return out
}

// ───────────────────────────── 断言 ─────────────────────────────

// TestServiceMultiRepositoryWritesShareOneTransaction 核心守卫
func TestServiceMultiRepositoryWritesShareOneTransaction(t *testing.T) {
	facts := loadRepoFacts(t)
	analyses := scanServicePackage(t, facts)

	var violations []string
	for _, key := range sortedAnalysisKeys(analyses) {
		a := analyses[key]
		allWriteFields := map[string]bool{}
		for f := range a.writesInTx {
			allWriteFields[f] = true
		}
		for f := range a.writesOutside {
			allWriteFields[f] = true
		}
		if len(allWriteFields) < 2 {
			continue
		}
		if len(a.writesOutside) == 0 {
			continue // 全部写都在 InTx 内 —— 正是期望的形态
		}
		if _, allowed := multiRepoWriteAllowed[key]; allowed {
			continue
		}
		violations = append(violations, describeViolation(a))
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Errorf("以下函数写入了多个仓库，但存在不在事务内的写：\n\n%s\n\n"+
			"修法：把该函数里的全部写放进 s.txMgr.InTx(ctx, func(ctx context.Context) error { ... })，\n"+
			"并把收到的 ctx 传给每一次仓库调用（不要用外层 ctx）。\n"+
			"若这些写确实不需要同生共死，请在 multiRepoWriteAllowed 登记并写明理由。",
			strings.Join(violations, "\n\n"))
	}

	// 事务不感知的仓库不得出现在事务里 —— 它在事务内也拿不到事务句柄，
	// 写进去的东西不会回滚，会给人一种「已经原子了」的错觉。
	var unawareInTx []string
	for _, key := range sortedAnalysisKeys(analyses) {
		a := analyses[key]
		for _, field := range fieldNames(a.fieldsUsedInTx) {
			iface := a.repoFields[field]
			if !facts.unaware[iface] {
				continue
			}
			ms := make([]string, 0, len(a.fieldsUsedInTx[field]))
			for m := range a.fieldsUsedInTx[field] {
				ms = append(ms, m)
			}
			sort.Strings(ms)
			unawareInTx = append(unawareInTx,
				"  "+key+" 在事务里用了 "+field+"("+iface+") 的 "+strings.Join(ms, "/"))
		}
	}
	if len(unawareInTx) > 0 {
		sort.Strings(unawareInTx)
		t.Errorf("以下位置在事务内使用了「事务不感知」的仓库：\n%s\n\n"+
			"这些仓库的接口方法签名没有 ctx，即使放进 InTx 也会走自己的连接池、\n"+
			"不会被回滚。要么给该接口的方法补上 ctx 参数并改用 dbFor，要么别把它放进事务。",
			strings.Join(unawareInTx, "\n"))
	}
}

// TestInTxCallbackWritesUseTheCallbackCtx 事务闭包里的每一次仓库调用，必须把闭包收到的 ctx 传下去。
//
// 为什么单独一条：TestServiceMultiRepositoryWritesShareOneTransaction 只判断写**词法上**
// 在不在 InTx 闭包里（insideInTx），看不出传的是哪个 ctx。于是下面这种写法一路绿灯：
//
//	s.txMgr.InTx(ctx, func(txCtx context.Context) error {
//	    s.messageRepo.DeleteBySessionID(ctx, sessionID) // 外层 ctx：这次写没进事务
//	    return s.sessionRepo.Delete(ctx, sessionID)     // 同样没进事务
//	})
//
// 编译通过、看起来「写都在 InTx 内」，实际两次写各自提交 —— 中间失败就留下永久不一致，
// 正是 P1-7 要修的那类缺陷。这条检查把「fn 必须用它收到的 ctx」从注释变成红灯。
func TestInTxCallbackWritesUseTheCallbackCtx(t *testing.T) {
	facts := loadRepoFacts(t)
	analyses := scanServicePackage(t, facts)

	checked := 0
	var violations []string
	for _, key := range sortedAnalysisKeys(analyses) {
		a := analyses[key]
		checked += a.txCtxChecked
		for _, v := range a.txCtxMisuse {
			violations = append(violations, "  "+key+" → "+v)
		}
	}

	// 自证非空转：已知生产代码里有 5 处 InTx（共 11 次闭包内仓库调用）。
	// 检查数为 0 说明「InTx 回调」根本没被认出来，守卫会静默全绿。
	if checked < 5 {
		t.Fatalf("只在 InTx 闭包内核对了 %d 次仓库调用的 ctx 参数（期望 ≥5）——"+
			"InTx 回调的识别规则很可能已被代码结构变化打破，守卫会静默失守", checked)
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Errorf("以下仓库调用在 InTx 闭包内、却没有使用闭包收到的事务 ctx：\n%s\n\n"+
			"这些写会走仓库自己的连接池（dbFor 在 ctx 里找不到事务），即「已经进了 InTx，\n"+
			"写却没有加入事务」——外层看起来原子，实际不会回滚。\n"+
			"修法：把 InTx 回调的首参数命名并显式传下去，例如\n"+
			"    s.txMgr.InTx(ctx, func(txCtx context.Context) error { ... s.repo.Write(txCtx, ...) ... })",
			strings.Join(violations, "\n"))
	}
}

func describeViolation(a *funcAnalysis) string {
	var sb strings.Builder
	sb.WriteString("  " + a.key + "\n")
	sb.WriteString("    事务内: " + describeWrites(a.writesInTx) + "\n")
	sb.WriteString("    事务外: " + describeWrites(a.writesOutside))
	return sb.String()
}

func describeWrites(m map[string]map[string]bool) string {
	if len(m) == 0 {
		return "(无)"
	}
	parts := make([]string, 0, len(m))
	for _, field := range fieldNames(m) {
		ms := make([]string, 0, len(m[field]))
		for k := range m[field] {
			ms = append(ms, k)
		}
		sort.Strings(ms)
		parts = append(parts, field+"."+strings.Join(ms, "/"))
	}
	return strings.Join(parts, ", ")
}

func sortedAnalysisKeys(m map[string]*funcAnalysis) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// TestTransactionalSitesAreDetected 防止守卫「恒真」：确认它真的看得见已知的三处事务化站点
func TestTransactionalSitesAreDetected(t *testing.T) {
	facts := loadRepoFacts(t)
	analyses := scanServicePackage(t, facts)

	cases := map[string]bool{ // 键 → 是否期望「多仓库写且全在事务内」
		"chat_service.go:chatService.DeleteSession":           true,
		"admin_session_service.go:adminSessionService.Delete": true,
		"document_service.go:documentService.Upload":          true,
	}
	for key, wantInTx := range cases {
		a, ok := analyses[key]
		if !ok {
			t.Errorf("守卫没找到 %s —— 说明 AST 分析已经跟不上代码结构，它现在可能全是假绿", key)
			continue
		}
		allWriteFields := len(a.writesInTx) + len(a.writesOutside)
		if allWriteFields < 2 {
			t.Errorf("%s 只识别出 %d 个事务内写仓库、%d 个事务外写仓库，"+
				"守卫显然没能解析出它的跨仓库写（期望它在事务内写多个仓库）",
				key, len(a.writesInTx), len(a.writesOutside))
			continue
		}
		if wantInTx && len(a.writesOutside) != 0 {
			t.Errorf("%s 期望「所有写都在事务内」，实际事务外仍有: %s", key, describeWrites(a.writesOutside))
		}
	}

	// 另外确认守卫确实扫到了规模 —— 只解析出个位数函数就是分析失效
	if len(analyses) < 50 {
		t.Fatalf("只分析了 %d 个函数，服务层的函数远不止这些，解析很可能已失效", len(analyses))
	}
}

// TestMultiRepoWriteRegistryIsComplete 例外登记表必须与实际情况一致（可增可销，但不能不符）
func TestMultiRepoWriteRegistryIsComplete(t *testing.T) {
	facts := loadRepoFacts(t)
	analyses := scanServicePackage(t, facts)

	actual := map[string]bool{}
	for key, a := range analyses {
		if len(a.writesOutside) > 0 && len(a.writesInTx)+len(a.writesOutside) >= 2 {
			actual[key] = true
		}
	}
	var stale []string
	for key, reason := range multiRepoWriteAllowed {
		if !actual[key] {
			stale = append(stale, key)
		}
		if len([]rune(strings.TrimSpace(reason))) < 20 {
			t.Errorf("%s 的例外理由过于简略（%q）：必须说清为什么这些写不需要同生共死",
				key, reason)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("以下登记项已不符合实际情况，请销账（或修好代码后移除）: %v", stale)
	}
}
