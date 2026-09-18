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
// 图结构是静态的（4 节点 + 5 条边），唯一 per-request 的是 Graph Local State 与 ChatModel。
// 改成启动期编译一次之后，多出两个必须被钉住的约束：
//
//  1. genState **不能再闭包捕获具体 state 实例**——否则编译一次 = 所有请求共用一份 state，
//     并发请求互相串数据（旧实现每次都是新闭包，恰好掩盖了这个约束）。
//  2. 请求路径**不能再出现 build/Compile**——否则等于退回旧实现，白白多一份「同一静态错误
//     在每个请求上重复一次」的错误路径。
//
// 下面两条用例分别钉这两个约束：一条用真实并发跑同一份 compiled Runnable 验隔离，
// 一条用 AST 静态扫描验「编译只发生在构造期」。

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
func drainStream(sr *schema.StreamReader[*schema.Message]) {
	defer sr.Close()
	for {
		if _, err := sr.Recv(); err != nil {
			return
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
			rewritten := fmt.Sprintf("rw-%d", i)

			// per-request 的两个变量：state 与 ChatModel，都经 ctx 注入。
			state := &quickGraphState{}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			ctx = withGraphState(ctx, state)
			ctx = withGraphChatModel(ctx, &staticChatModel{reply: "reply-" + kb})

			input := &quickGraphInput{
				OriginalQuery:     query,
				UserID:            "u-1",
				KnowledgeBaseIDs:  []string{kb},
				InputMsgs:         []*schema.Message{schema.UserMessage(query)},
				UserQuestionIndex: 0,
				ModelName:         "cl100k_base",
				RetrievalBudget:   2000,
				// 预置改写结果 → 改写节点短路，不调 LLM，用例只考察 state/ChatModel 隔离。
				PreRewrittenQuery: rewritten,
				PreIntent:         intentQuestion,
			}

			sr, err := runnable.Invoke(ctx, input)
			if err != nil {
				errs[i] = fmt.Errorf("Invoke 失败: %w", err)
				return
			}
			if sr == nil {
				errs[i] = fmt.Errorf("Invoke 返回 nil stream")
				return
			}
			drainStream(sr)

			if len(state.RetrievedDocs) != 1 {
				errs[i] = fmt.Errorf("RetrievedDocs 数量=%d，期望 1（docs=%v）", len(state.RetrievedDocs), state.RetrievedDocs)
				return
			}
			if got, want := state.RetrievedDocs[0].Content, "doc-for-"+kb; got != want {
				errs[i] = fmt.Errorf("state 被别的请求串了：RetrievedDocs[0].Content=%q，期望 %q", got, want)
				return
			}
			if state.Input == nil || state.Input.OriginalQuery != query {
				errs[i] = fmt.Errorf("state.Input 被别的请求串了：%+v", state.Input)
				return
			}
			if state.RewrittenQuery != rewritten {
				errs[i] = fmt.Errorf("state.RewrittenQuery=%q，期望 %q", state.RewrittenQuery, rewritten)
				return
			}
			if state.KeywordQuery == "" || state.VectorQuery == "" {
				errs[i] = fmt.Errorf("双轨 query 未写入 state：vector=%q keyword=%q", state.VectorQuery, state.KeywordQuery)
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
			PreRewrittenQuery: "OSI 七层模型分别是什么",
			PreIntent:         intentQuestion,
		}
	}
	runOnce := func(b *testing.B, runnable einoCompose.Runnable[*quickGraphInput, *schema.StreamReader[*schema.Message]]) {
		state := &quickGraphState{}
		ctx := withGraphState(context.Background(), state)
		ctx = withGraphChatModel(ctx, &staticChatModel{reply: "ok"})
		sr, err := runnable.Invoke(ctx, newInput())
		if err != nil {
			b.Fatalf("Invoke 失败: %v", err)
		}
		drainStream(sr)
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
		// 改动后的路径：Graph 只编译一次，请求期只注入 state/ChatModel 并执行。
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
