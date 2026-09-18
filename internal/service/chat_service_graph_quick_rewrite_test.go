package service

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	einoModel "github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/internal/rag"
)

// ─── P1-1：快速模式「改写结果」的唯一载体与唯一调用点 ──────────────────────
//
// 背景：旧实现里同一次改写有两个调用点——processMessageGraphQuick 预跑一次（带 2s 硬超时），
// Graph 内的 quickRewriteFn 在 PreRewrittenQuery 为空时还会**再调一次**；两者之间靠 7 个
// 平行字段（PreRewrittenQuery/PreIntent/PreKeywords/PreSkipRetrieve/PreNeedClarify/
// PreClarifyQuestion/PreClarifyOptions）搬运结果。
//
// 这类结构的缺陷是「静默」的：平行字段必须成组、按序、在多个调用点赋值，漏写一个不会编译报错，
// 只表现为意图/关键词莫名丢失；第二个调用点也不会报错，只表现为请求关键路径上白等一轮 LLM。
//
// 现在的结果：
//   - 载体唯一 —— quickGraphInput.PreRewrite *rewriteResult；
//   - 调用点唯一 —— doRewriteWithLLM 只被 processMessageGraphQuick 调用；
//   - 派生规则唯一 —— 「是否跳过检索」只在 deriveSkipRetrieve 里定义一次。
//
// 下面 4 条守卫分别钉形状 / 历史名 / 调用点 / 返回值元数，2 条行为用例钉运行时语义。
// 守卫挡得住「形状回退」，挡不住「字段接错」——所以两者都要有。

// recordingChatModel 记录 LLM 被调用的方式：
//   - Generate 被调 → 说明「改写」在 Graph 内又跑了一遍（本批次要消灭的缺陷）；
//   - Stream 被调 → 生成节点正常工作（生成走 Stream，改写走 Generate，两者可区分）。
type recordingChatModel struct {
	mu          sync.Mutex
	genCalls    int
	streamCalls int
}

func (m *recordingChatModel) Generate(context.Context, []*schema.Message, ...einoModel.Option) (*schema.Message, error) {
	m.mu.Lock()
	m.genCalls++
	m.mu.Unlock()
	return schema.AssistantMessage("generate-should-not-happen", nil), nil
}

func (m *recordingChatModel) Stream(context.Context, []*schema.Message, ...einoModel.Option) (*schema.StreamReader[*schema.Message], error) {
	m.mu.Lock()
	m.streamCalls++
	m.mu.Unlock()
	return schema.StreamReaderFromArray([]*schema.Message{schema.AssistantMessage("ok", nil)}), nil
}

func (m *recordingChatModel) counts() (gen, stream int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.genCalls, m.streamCalls
}

var _ einoModel.BaseChatModel = (*recordingChatModel)(nil)

// TestQuickRewriteNode_ReusesPrecomputedOutcomeWithoutCallingLLM
//
// 行为回归：Rewrite 节点只**消费**外部预跑的 rewriteResult——
//   - state 的改写字段逐一对上结构体（钉住「有字段被漏搬」这一类静默丢语义）；
//   - SkipRetrieve 仍是有载荷的开关（true 不检索、false 正常检索），
//     说明它作为派生字段从预跑结果一路传到了检索节点；
//   - 整个 Graph 执行期间 Generate 调用为 0（钉住「Graph 内又改写了一轮」）。
func TestQuickRewriteNode_ReusesPrecomputedOutcomeWithoutCallingLLM(t *testing.T) {
	ensureTestConfig(t)

	runnable, err := compileQuickGraph(rag.NewEinoRetrieverAdapter(&barrierRetriever{}, 10), nil)
	if err != nil {
		t.Fatalf("compileQuickGraph 失败: %v", err)
	}

	cases := []struct {
		name     string
		pre      *rewriteResult
		wantDocs int
	}{
		{
			name: "skip_retrieve_true",
			pre: &rewriteResult{
				Rewritten:    "指代回填后的完整问题",
				Intent:       intentChitchat,
				Keywords:     []string{"k1", "k2"},
				SkipRetrieve: true,
			},
			wantDocs: 0,
		},
		{
			name: "skip_retrieve_false",
			pre: &rewriteResult{
				Rewritten:    "指代回填后的完整问题",
				Intent:       intentQuestion,
				Keywords:     []string{"k1"},
				SkipRetrieve: false,
			},
			wantDocs: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			state := &quickGraphState{}
			model := &recordingChatModel{}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			ctx = withGraphState(ctx, state)
			ctx = withGraphChatModel(ctx, model)

			input := &quickGraphInput{
				OriginalQuery:     "那个方案呢",
				UserID:            "u-1",
				KnowledgeBaseIDs:  []string{"kb-1"},
				InputMsgs:         []*schema.Message{schema.UserMessage("那个方案呢")},
				UserQuestionIndex: 0,
				ModelName:         "cl100k_base",
				RetrievalBudget:   2000,
				PreRewrite:        tc.pre,
			}

			sr, err := runnable.Invoke(ctx, input)
			if err != nil {
				t.Fatalf("Invoke 失败: %v", err)
			}
			drainStream(sr)

			if state.RewrittenQuery != tc.pre.Rewritten {
				t.Errorf("state.RewrittenQuery=%q，期望 %q", state.RewrittenQuery, tc.pre.Rewritten)
			}
			if state.Intent != tc.pre.Intent {
				t.Errorf("state.Intent=%q，期望 %q（预跑结果的意图被丢了？）", state.Intent, tc.pre.Intent)
			}
			if got, want := strings.Join(state.Keywords, ","), strings.Join(tc.pre.Keywords, ","); got != want {
				t.Errorf("state.Keywords=%q，期望 %q", got, want)
			}
			if state.SkipRetrieve != tc.pre.SkipRetrieve {
				t.Errorf("state.SkipRetrieve=%v，期望 %v", state.SkipRetrieve, tc.pre.SkipRetrieve)
			}
			if len(state.RetrievedDocs) != tc.wantDocs {
				t.Errorf("RetrievedDocs 数量=%d，期望 %d（SkipRetrieve 没有一路传到检索节点？）",
					len(state.RetrievedDocs), tc.wantDocs)
			}

			gen, stream := model.counts()
			if gen != 0 {
				t.Errorf("Graph 内发生了 %d 次改写式 LLM 调用（Generate），期望 0：同一次改写出现了第二个调用点", gen)
			}
			if stream != 1 {
				t.Errorf("生成节点 Stream 调用=%d，期望 1", stream)
			}
		})
	}
}

// TestQuickRewriteNode_RejectsMissingPreRewrite
//
// 契约：Graph 入参必须带上预跑好的 rewriteResult。缺了它就是调用方漏掉「唯一调用点」，
// 必须立刻报错——旧实现在这里会静默再调一次 LLM，正是「同一次改写两个调用点」的表现形式。
func TestQuickRewriteNode_RejectsMissingPreRewrite(t *testing.T) {
	ensureTestConfig(t)

	runnable, err := compileQuickGraph(rag.NewEinoRetrieverAdapter(emptyRetriever{}, 10), nil)
	if err != nil {
		t.Fatalf("compileQuickGraph 失败: %v", err)
	}

	state := &quickGraphState{}
	model := &recordingChatModel{}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = withGraphState(ctx, state)
	ctx = withGraphChatModel(ctx, model)

	input := &quickGraphInput{
		OriginalQuery:     "故意不设 PreRewrite",
		UserID:            "u-1",
		KnowledgeBaseIDs:  []string{"kb-1"},
		InputMsgs:         []*schema.Message{schema.UserMessage("故意不设 PreRewrite")},
		UserQuestionIndex: 0,
		ModelName:         "cl100k_base",
		RetrievalBudget:   2000,
	}

	sr, err := runnable.Invoke(ctx, input)
	if err == nil {
		if sr != nil {
			sr.Close()
		}
		t.Fatal("PreRewrite 为空时 Invoke 成功了，期望报错：改写结果必须由调用方预跑（见 quickRewriteFn）")
	}
	if sr != nil {
		sr.Close()
	}
	if gen, _ := model.counts(); gen != 0 {
		t.Errorf("PreRewrite 为空却触发了 %d 次改写式 LLM 调用，期望 0（应直接报错而不是补一次调用）", gen)
	}
}

// ─── 结构守卫 ───────────────────────────────────────────────────────────────

// graphFileForGuard 供守卫解析的目标文件：改写链路全部集中在这一个文件里。
const graphFileForGuard = "chat_service_graph_quick.go"

// rewriteOwnerFunc 唯一允许调用 doRewriteWithLLM 的函数（请求入口，Graph 外）。
const rewriteOwnerFunc = "processMessageGraphQuick"

// prodGoFileNames 返回包内生产代码（排除 _test.go）的文件名，已排序。
func prodGoFileNames(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("读取包目录失败: %v", err)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		seen[name] = true
	}
	names := sortedKeys(seen)
	if len(names) == 0 {
		t.Fatal("守卫空转：没有读到任何生产 .go 文件")
	}
	sort.Strings(names)
	return names
}

// typeString 把 AST 类型表达式还原成源码形式（*rewriteResult 之类）。
func typeString(fset *token.FileSet, e ast.Expr) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, e); err != nil {
		return "<unprintable>"
	}
	return buf.String()
}

// findStructType 在指定文件里找具名 struct 类型。
func findStructType(t *testing.T, file, typeName string) (*ast.StructType, *token.FileSet) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("解析 %s 失败: %v", file, err)
	}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != typeName {
				continue
			}
			if st, ok := ts.Type.(*ast.StructType); ok {
				return st, fset
			}
		}
	}
	t.Fatalf("守卫空转：%s 里没找到 struct %s", file, typeName)
	return nil, nil
}

// TestRewriteOutcomeIsCarriedByOneField
//
// 形状守卫：quickGraphInput 里「传递改写结果」的字段必须恰好一个，且类型是 *rewriteResult。
// 一旦有人把它拆回多个 Pre* 平行字段，这条用例立刻变红。
func TestRewriteOutcomeIsCarriedByOneField(t *testing.T) {
	st, fset := findStructType(t, graphFileForGuard, "quickGraphInput")

	var preFields []string
	for _, fld := range st.Fields.List {
		for _, n := range fld.Names {
			if strings.HasPrefix(n.Name, "Pre") {
				preFields = append(preFields, n.Name+" "+typeString(fset, fld.Type))
			}
		}
	}

	if len(preFields) != 1 || preFields[0] != "PreRewrite *rewriteResult" {
		t.Errorf("quickGraphInput 里承载改写结果的字段 = %v，期望恰好一个 [PreRewrite *rewriteResult]。\n"+
			"平行字段必须成组、按序、在多个调用点赋值，漏写一个不会编译报错、只会静默丢语义——"+
			"新增改写产出请加进 rewriteResult 结构体，不要再开平行字段。", preFields)
	}
}

// TestNoLegacyParallelRewriteFields
//
// 文本守卫：7 个历史平行字段名不得在任何生产代码里复活（含赋值点）。
// 与形状守卫互补：形状守卫盯字段声明，这条还盯「名字被写回来」。
func TestNoLegacyParallelRewriteFields(t *testing.T) {
	legacy := []string{
		"PreRewrittenQuery",
		"PreIntent",
		"PreKeywords",
		"PreSkipRetrieve",
		"PreNeedClarify",
		"PreClarifyQuestion",
		"PreClarifyOptions",
	}

	for _, file := range prodGoFileNames(t) {
		src, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", file, err)
		}
		for _, name := range legacy {
			if bytes.Contains(src, []byte(name)) {
				t.Errorf("%s 里仍出现历史平行字段 %s——改写结果只能由 quickGraphInput.PreRewrite 一个字段承载", file, name)
			}
		}
	}
}

// TestDoRewriteHasSingleCallSite
//
// 调用点守卫：doRewriteWithLLM 在生产代码里只允许有一个调用点（processMessageGraphQuick）。
// 第二个调用点 = 同一次改写跑两遍，且 Graph 内那次落在请求关键路径上白等 LLM。
func TestDoRewriteHasSingleCallSite(t *testing.T) {
	fset := token.NewFileSet()
	owners := map[string]bool{}

	for _, name := range prodGoFileNames(t) {
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("解析 %s 失败: %v", name, err)
		}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "doRewriteWithLLM" {
					owners[fd.Name.Name] = true
				}
				return true
			})
		}
	}

	got := sortedKeys(owners)
	if len(got) == 0 {
		t.Fatal("守卫空转：没有找到 doRewriteWithLLM 的调用点")
	}
	if fmt.Sprint(got) != fmt.Sprint([]string{rewriteOwnerFunc}) {
		t.Errorf("doRewriteWithLLM 的调用方 = %v，期望只有 [%s]。\n"+
			"第二个调用点意味着同一次改写要跑两遍；需要复用改写结果时，请读 quickGraphInput.PreRewrite。",
			got, rewriteOwnerFunc)
	}
}

// TestDoRewriteReturnsSingleOutcome
//
// 元数守卫：doRewriteWithLLM 只返回一个 *rewriteResult。
// 退回多返回值（旧实现是 7 元组）等于要求调用方成组搬运，漏一个不报错。
func TestDoRewriteReturnsSingleOutcome(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, graphFileForGuard, nil, 0)
	if err != nil {
		t.Fatalf("解析 %s 失败: %v", graphFileForGuard, err)
	}

	var fd *ast.FuncDecl
	for _, decl := range f.Decls {
		if d, ok := decl.(*ast.FuncDecl); ok && d.Name.Name == "doRewriteWithLLM" {
			fd = d
		}
	}
	if fd == nil {
		t.Fatal("守卫空转：没找到 doRewriteWithLLM")
	}

	var got []string
	if fd.Type.Results != nil {
		for _, r := range fd.Type.Results.List {
			got = append(got, typeString(fset, r.Type))
		}
	}
	if len(got) != 1 || got[0] != "*rewriteResult" {
		t.Errorf("doRewriteWithLLM 的返回值 = %v，期望恰好一个 *rewriteResult。\n"+
			"多返回值 + 平行字段要求调用方成组搬运；请把新产出加进 rewriteResult。", got)
	}
}
