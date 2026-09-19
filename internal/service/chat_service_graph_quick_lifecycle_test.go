package service

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	einoModel "github.com/cloudwego/eino/components/model"
	einoCompose "github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/internal/rag"
	"solvify-agent/pkg/config"
)

// ─── P1-2：快速模式 Graph「编译一次、请求期复用」的回归与结构守卫 ─────────────
//
// 背景：旧实现 `processMessageGraphQuick` 每个请求都 `buildQuickGraph` + `g.Compile(...)`。
// 图结构是静态的（5 节点 + 5 条边 + 1 条分支），请求级变量（ChatModel）装在入参
// quickGraphInput 里沿边传递。改成启动期编译一次之后，多出三个必须被钉住的约束：
//
//  1. 请求之间**不能有共享的可变对象**——编译产物被 N 个请求并发复用，任何跨请求共享的可变
//     载体都会互相串数据。历史坑：Graph Local State 曾由 ctx 注入，一旦 stateGenerator 闭包
//     捕获了某个具体的 state 实例，编译一次 = 所有请求共用一份 state。
//     现在请求级数据只存在于「本次 Invoke 的入参」及其派生的载荷里，结构上没有共享物可串。
//  2. 请求路径**不能再出现 build/Compile**——否则等于退回旧实现，白白多一份「同一静态错误
//     在每个请求上重复一次」的错误路径。
//  3. 请求级数据**不能再走 ctx / Graph Local State**——那是同一份数据的第二个来源。
//
// 下面三条用例分别钉这三个约束：一条用真实并发跑同一份 compiled Runnable 验隔离，
// 两条用 AST 静态扫描验「编译只发生在构造期」与「不得退回 ctx 通道」。

// barrierRetriever 让 N 个并发请求都「卡」在检索节点上，等全部到齐再一起放行。
// 这样并发重叠是**确定的**，而不是靠 racing 概率去撞——串数据的用例不会变成偶发绿灯。
type barrierRetriever struct {
	arrive  *sync.WaitGroup
	release <-chan struct{}
}

func (b *barrierRetriever) Retrieve(ctx context.Context, q rag.Query) (rag.Result, error) {
	if b.arrive != nil {
		b.arrive.Done()
	}
	if b.release != nil {
		select {
		case <-b.release:
		case <-ctx.Done():
			return rag.Result{}, ctx.Err()
		}
	}
	kb := ""
	if len(q.KnowledgeBaseIDs) > 0 {
		kb = q.KnowledgeBaseIDs[0]
	}
	// 返回一条「能反查是哪个请求」的文档：内容由该请求自己的 KnowledgeBaseIDs 派生。
	return rag.Result{Documents: []rag.Document{{
		ID:              "chunk-" + kb,
		Content:         "doc-for-" + kb,
		KnowledgeBaseID: kb,
		DocumentID:      "doc-" + kb,
		Score:           0.9,
	}}}, nil
}

// staticChatModel 一个只回固定文本的 ChatModel，实现 eino 的 BaseChatModel。
type staticChatModel struct{ reply string }

func (m *staticChatModel) Generate(context.Context, []*schema.Message, ...einoModel.Option) (*schema.Message, error) {
	return schema.AssistantMessage(m.reply, nil), nil
}

func (m *staticChatModel) Stream(context.Context, []*schema.Message, ...einoModel.Option) (*schema.StreamReader[*schema.Message], error) {
	return schema.StreamReaderFromArray([]*schema.Message{schema.AssistantMessage(m.reply, nil)}), nil
}

var _ einoModel.BaseChatModel = (*staticChatModel)(nil)

// drainStream 消费掉 Graph 输出的流，保证链路跑到底（不消费会让上游节点阻塞）。
func drainStream(sr *schema.StreamReader[*schema.Message]) string {
	defer sr.Close()
	var sb strings.Builder
	for {
		msg, err := sr.Recv()
		if err != nil {
			return sb.String()
		}
		if msg != nil {
			sb.WriteString(msg.Content)
		}
	}
}

// TestQuickGraph_OneCompiledRunnableServesConcurrentRequestsWithIsolatedState
//
// 一份 compiled Runnable 同时服务 N 个请求，每个请求带自己的 state 与 ChatModel，
// 断言各自读回的 RetrievedDocs / Input / RewrittenQuery 都只属于自己。
//
// 失败模式（本用例存在的理由）：若 genState 捕获了某个共享 state 实例，
// 8 个请求会互相覆盖，断言必红——且 barrierRetriever 保证它们确实同时在场。
func TestQuickGraph_OneCompiledRunnableServesConcurrentRequestsWithIsolatedState(t *testing.T) {
	ensureTestConfig(t)

	const n = 8

	arrive := &sync.WaitGroup{}
	arrive.Add(n)
	release := make(chan struct{})
	var once sync.Once
	releaseAll := func() { once.Do(func() { close(release) }) }
	go func() {
		arrive.Wait()
		releaseAll()
	}()
	// 兜底：万一有请求没走到检索节点，15s 后放行，用例以断言失败收场而不是挂死。
	guard := time.AfterFunc(15*time.Second, releaseAll)
	defer guard.Stop()

	retriever := &barrierRetriever{arrive: arrive, release: release}
	runnable, err := compileQuickGraph(rag.NewEinoRetrieverAdapter(retriever, 10), nil)
	if err != nil {
		t.Fatalf("compileQuickGraph 失败: %v", err)
	}
	if runnable == nil {
		t.Fatal("compileQuickGraph 返回了 nil Runnable")
	}

	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			kb := fmt.Sprintf("kb-%d", i)
			query := fmt.Sprintf("q-%d", i)
			wantReply := "reply-" + kb

			// 唯一的请求级变量就是入参本身：ChatModel 与改写结果都挂在它上面，不再经 ctx 注入。
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			input := &quickGraphInput{
				OriginalQuery:     query,
				UserID:            "u-1",
				KnowledgeBaseIDs:  []string{kb},
				InputMsgs:         []*schema.Message{schema.UserMessage(query)},
				UserQuestionIndex: 0,
				ModelName:         "cl100k_base",
				RetrievalBudget:   2000,
				ChatModel:         &staticChatModel{reply: wantReply},
				// query 里既没有本地意图也没有指代 → 改写节点不会调 LLM，用例只考察请求级数据的隔离。
			}

			out, err := runnable.Invoke(ctx, input)
			if err != nil {
				errs[i] = fmt.Errorf("Invoke 失败: %w", err)
				return
			}
			if out == nil || out.Stream == nil {
				errs[i] = fmt.Errorf("Invoke 返回 nil 出参/流")
				return
			}
			got := drainStream(out.Stream)

			// 模型侧隔离：流式内容必须是自己那个 ChatModel 的回复。
			if got != wantReply {
				errs[i] = fmt.Errorf("流式内容=%q，期望 %q（ChatModel 被别的请求串了？）", got, wantReply)
				return
			}
			// 检索侧隔离：docs 内容由自己的 KnowledgeBaseIDs 派生。
			if len(out.Docs) != 1 {
				errs[i] = fmt.Errorf("出参 docs 数量=%d，期望 1（docs=%v）", len(out.Docs), out.Docs)
				return
			}
			if gotDoc, want := out.Docs[0].Content, "doc-for-"+kb; gotDoc != want {
				errs[i] = fmt.Errorf("docs 被别的请求串了：Content=%q，期望 %q", gotDoc, want)
			}
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("请求 %d: %v", i, err)
		}
	}
}

// TestQuickGraph_CompileHappensOnlyAtStartup
//
// 结构守卫：在生产代码里，
//   - `compileQuickGraph` 只允许被 `NewChatService`（构造期）调用；
//   - `buildQuickGraph` 只允许被 `compileQuickGraph` 调用；
//   - 全包不出现 second 处 `.Compile(...)`。
//
// 任何「把编译搬回请求路径」的改动都会让这条用例变红——注释挡不住人的时候，用扫描挡。
func TestQuickGraph_CompileHappensOnlyAtStartup(t *testing.T) {
	fset := token.NewFileSet()

	callers := map[string]map[string]bool{} // 被调函数名 → 外层函数名集合
	var compileOutsideOwner []string
	parsedFiles := 0

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读取包目录失败: %v", err)
	}
	record := func(callee, owner string) {
		if callers[callee] == nil {
			callers[callee] = map[string]bool{}
		}
		callers[callee][owner] = true
	}

	for _, e := range entries {
		name := e.Name()
		// 只看生产代码：请求路径的约束与测试无关（测试本来就要直接编译 Graph）。
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("解析 %s 失败: %v", name, err)
		}
		parsedFiles++
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			owner := fd.Name.Name
			ast.Inspect(fd.Body, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok {
					return true
				}
				switch fun := call.Fun.(type) {
				case *ast.Ident:
					record(fun.Name, owner)
				case *ast.SelectorExpr:
					record(fun.Sel.Name, owner)
					// 只认 Compile 本身，不误伤 regexp.MustCompile。
					if fun.Sel.Name == "Compile" && owner != graphCompileOwnerFunc {
						compileOutsideOwner = append(compileOutsideOwner,
							fmt.Sprintf("%s:%d (在 %s 内)", name, fset.Position(call.Pos()).Line, owner))
					}
				}
				return true
			})
		}
	}

	// 自证非空转：没解析到文件 / 没找到调用点，说明守卫本身失效了。
	if parsedFiles == 0 {
		t.Fatal("守卫空转：没有解析到任何生产 .go 文件")
	}
	gotCompile := sortedKeys(callers["compileQuickGraph"])
	if len(gotCompile) == 0 {
		t.Fatal("守卫空转：没有找到 compileQuickGraph 的调用点")
	}
	gotBuild := sortedKeys(callers["buildQuickGraph"])
	if len(gotBuild) == 0 {
		t.Fatal("守卫空转：没有找到 buildQuickGraph 的调用点")
	}

	if fmt.Sprint(gotCompile) != fmt.Sprint([]string{graphCtorCallerFunc}) {
		t.Errorf("compileQuickGraph 的调用方 = %v，期望只有 [%s]（构造期）。\n"+
			"若确需新增启动期调用点，请一并更新本守卫并说明理由；"+
			"若出现在 service 方法里，就是把编译搬回了请求路径。", gotCompile, graphCtorCallerFunc)
	}
	if fmt.Sprint(gotBuild) != fmt.Sprint([]string{graphCompileOwnerFunc}) {
		t.Errorf("buildQuickGraph 的调用方 = %v，期望只有 [%s]", gotBuild, graphCompileOwnerFunc)
	}
	if len(compileOutsideOwner) != 0 {
		t.Errorf("发现 %s 之外还有 Compile 调用：%v（Graph 编译只允许发生在启动期）",
			graphCompileOwnerFunc, compileOutsideOwner)
	}
}

// TestQuickGraph_NoContextDataChannel
//
// 结构守卫：快速模式 Graph 不得再把请求级数据塞进 context / Graph Local State。
//
// 背景：旧实现同时开了两条数据通道——一条走图的边，一条走 ctx 里的 Graph Local State；
// 同一份数据（改写结果、检索 docs）在两条路上各存一份，不一致时不会报错，只会静默读到旧值。
// 更糟的是 stateGenerator 取不到 state 时会造一个空 state 继续跑（eino 的 stateGenerator
// 签名没有 error 出口），于是「注入漏了」表现为「下游拿到空数据」，而不是报错。
//
// 现在请求级数据只在 quickGraphInput → quickGraphPayload → quickGraphOutput 这条链上流动。
// 把下面任何一个名字写回生产代码，这条用例都会变红。
func TestQuickGraph_NoContextDataChannel(t *testing.T) {
	banned := []string{
		"ProcessState",
		"WithGenLocalState",
		"quickGraphState",
		"withGraphState",
		"graphStateFromContext",
		"withGraphChatModel",
		"graphChatModelFromContext",
	}

	fset := token.NewFileSet()
	files := prodGoFileNames(t)
	foundCarrier := false

	for _, name := range files {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", name, err)
		}
		if strings.Contains(string(src), "quickGraphPayload") {
			foundCarrier = true
		}

		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("解析 %s 失败: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			id, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			for _, b := range banned {
				if id.Name == b {
					t.Errorf("%s 里出现 %s：请求级数据必须走 quickGraphInput/quickGraphPayload，"+
						"不要再经 context 或 Graph Local State 传递（两条通道 = 同一份数据的第二个来源）", name, b)
				}
			}
			return true
		})
	}

	// 自证非空转：新载体必须真的存在，否则上面的扫描只是在比对一堆不存在的名字。
	if !foundCarrier {
		t.Fatal("守卫空转：生产代码里没找到 quickGraphPayload，说明扫描的文件集不对")
	}
}

const (
	// graphCompileOwnerFunc 唯一允许调用 buildQuickGraph / 执行 Compile 的函数。
	graphCompileOwnerFunc = "compileQuickGraph"
	// graphCtorCallerFunc 唯一允许调用 compileQuickGraph 的函数（构造期）。
	graphCtorCallerFunc = "NewChatService"
)

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// ─── 基准：量化「编译挪到启动期」省掉的 per-request 成本 ────────────────────
//
//	go test ./internal/service -run '^$' -bench PerRequestCost -benchmem
//
// 两个子基准是同一件事的 A/B：compile_and_invoke 是改动前的每请求成本，
// invoke_only 是改动后的每请求成本，差值就是被省下的部分。

func ensureBenchConfig(b *testing.B) {
	b.Helper()
	if _, err := config.Load(""); err != nil {
		b.Fatalf("初始化测试配置失败: %v", err)
	}
}

func BenchmarkPerRequestCost(b *testing.B) {
	ensureBenchConfig(b)

	newInput := func() *quickGraphInput {
		return &quickGraphInput{
			OriginalQuery:     "OSI 七层模型分别是什么",
			UserID:            "u-1",
			KnowledgeBaseIDs:  []string{"11111111-1111-1111-1111-111111111111"},
			InputMsgs:         []*schema.Message{schema.UserMessage("OSI 七层模型分别是什么")},
			UserQuestionIndex: 0,
			ModelName:         "cl100k_base",
			RetrievalBudget:   2000,
			ChatModel:         &staticChatModel{reply: "ok"},
		}
	}
	runOnce := func(b *testing.B, runnable einoCompose.Runnable[*quickGraphInput, *quickGraphOutput]) {
		out, err := runnable.Invoke(context.Background(), newInput())
		if err != nil {
			b.Fatalf("Invoke 失败: %v", err)
		}
		drainStream(out.Stream)
	}

	b.Run("compile_and_invoke", func(b *testing.B) {
		// 改动前的路径：每个请求都 build + Compile，再执行。
		adapter := rag.NewEinoRetrieverAdapter(emptyRetriever{}, 10)
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			runnable, err := compileQuickGraph(adapter, nil)
			if err != nil {
				b.Fatalf("compileQuickGraph 失败: %v", err)
			}
			runOnce(b, runnable)
		}
	})

	b.Run("invoke_only", func(b *testing.B) {
		// 改动后的路径：Graph 只编译一次，请求期只装请求级变量（入参）并执行。
		adapter := rag.NewEinoRetrieverAdapter(emptyRetriever{}, 10)
		runnable, err := compileQuickGraph(adapter, nil)
		if err != nil {
			b.Fatalf("compileQuickGraph 失败: %v", err)
		}
		b.ResetTimer()
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			runOnce(b, runnable)
		}
	})
}
