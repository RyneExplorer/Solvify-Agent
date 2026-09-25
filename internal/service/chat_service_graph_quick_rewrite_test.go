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
// 现在的结果（改写已搬进 Graph 内）：
//   - 载体唯一 —— quickGraphPayload.Rewrite *rewriteResult；
//   - 调用点唯一 —— doRewriteWithLLM 只被 Graph 的改写节点 quickRewriteFn 调用（图外不再预跑）；
//   - 派生规则唯一 —— 「是否跳过检索」只在 deriveSkipRetrieve 里定义一次；
//   - 传递路径唯一 —— 请求级数据只走 quickGraphInput → quickGraphPayload → quickGraphOutput，
//     不再有「边」与「Graph Local State」两条通道。
//
// 下面 4 条守卫分别钉形状 / 历史名 / 调用点 / 返回值元数，3 条行为用例钉运行时语义
// （成本模型 / LLM 失败回退 / 澄清短路）。
// 守卫挡得住「形状回退」，挡不住「字段接错」——所以两者都要有。

// recordingChatModel 记录 LLM 被调用的方式与收到的 prompt：
//   - Generate 被调 → 说明「改写」在 Graph 内又跑了一遍（本批次要消灭的缺陷）；
//   - Stream 被调 → 生成节点正常工作（生成走 Stream，改写走 Generate，两者可区分）；
//   - streamMsgs → 拼装节点最终发给模型的消息，用来断言改写结果真的落进了 prompt；
//   - genReply → 指定 Generate 的返回内容，用来喂改写节点各种 LLM 产出（澄清 / 改写 / 垃圾）。
type recordingChatModel struct {
	mu          sync.Mutex
	genReply    string
	genCalls    int
	streamCalls int
	streamMsgs  []*schema.Message
}

func (m *recordingChatModel) Generate(context.Context, []*schema.Message, ...einoModel.Option) (*schema.Message, error) {
	m.mu.Lock()
	m.genCalls++
	reply := m.genReply
	m.mu.Unlock()
	if reply == "" {
		// 默认回一段「明显不该出现」的文本：哪个用例没设 genReply 却调了 Generate，
		// 会立刻在断言里暴露，而不是静默通过。
		reply = "generate-should-not-happen"
	}
	return schema.AssistantMessage(reply, nil), nil
}

func (m *recordingChatModel) Stream(_ context.Context, msgs []*schema.Message, _ ...einoModel.Option) (*schema.StreamReader[*schema.Message], error) {
	m.mu.Lock()
	m.streamCalls++
	m.streamMsgs = msgs
	m.mu.Unlock()
	return schema.StreamReaderFromArray([]*schema.Message{schema.AssistantMessage("ok", nil)}), nil
}

func (m *recordingChatModel) counts() (gen, stream int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.genCalls, m.streamCalls
}

// prompt 返回 Stream 时收到的消息；生成节点没被调过则返回 nil。
func (m *recordingChatModel) prompt() []*schema.Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.streamMsgs
}

var _ einoModel.BaseChatModel = (*recordingChatModel)(nil)

// TestQuickRewriteNode_CallsLLMOnlyForAnaphora
//
// 成本模型回归：改写节点自己决定「要不要调 LLM」，判据是「问题里有没有真指代」——
//   - 本地意图命中（问候）→ 0 次 LLM，且 SkipRetrieve 传到检索节点后一条都不查；
//   - 无疑义词 → 0 次 LLM（LLM 没有不可替代的产出），但仍照常检索；
//   - 含指代 → 恰好 1 次 LLM，且改写结果真的替换进了 prompt（不是只被写进某个字段）。
//
// 旧实现有两个调用点（图外预跑一次 + 图内 PreRewrittenQuery 为空时又调一次），
// 判据也散在两处；现在只由图内的改写节点判定并调用。
func TestQuickRewriteNode_CallsLLMOnlyForAnaphora(t *testing.T) {
	ensureTestConfig(t)

	runnable, err := compileQuickGraph(rag.NewEinoRetrieverAdapter(&barrierRetriever{}, 10))
	if err != nil {
		t.Fatalf("compileQuickGraph 失败: %v", err)
	}

	const llmRewritten = "OSI 七层模型具体分为哪几层"

	cases := []struct {
		name       string
		query      string
		genReply   string
		wantGen    int
		wantDocs   int
		wantPrompt string
	}{
		{
			// 本地意图规则命中（0ms）：问候语既不该调 LLM，也不需要检索
			name:       "本地意图命中_问候",
			query:      "你好",
			wantGen:    0,
			wantDocs:   0,
			wantPrompt: "你好",
		},
		{
			// 无疑义词：LLM 没有不可替代的产出（线上实测 78% 只是确认默认意图），不调
			name:       "无疑义词_不调LLM",
			query:      "OSI 七层模型分别是什么",
			wantGen:    0,
			wantDocs:   1,
			wantPrompt: "OSI 七层模型分别是什么",
		},
		{
			// 含指代：消解指代是 LLM 唯一不可替代的能力 → 必须调，且改写结果要进 prompt
			name:       "含指代_调LLM并采用改写结果",
			query:      "那个方案呢",
			genReply:   `{"rewritten":"` + llmRewritten + `","intent":"question","keywords":["osi"],"need_clarify":false}`,
			wantGen:    1,
			wantDocs:   1,
			wantPrompt: llmRewritten,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			model := &recordingChatModel{genReply: tc.genReply}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			input := &quickGraphInput{
				OriginalQuery:     tc.query,
				UserID:            "u-1",
				KnowledgeBaseIDs:  []string{"kb-1"},
				InputMsgs:         []*schema.Message{schema.UserMessage(tc.query)},
				UserQuestionIndex: 0,
				ModelName:         "cl100k_base",
				RetrievalBudget:   2000,
				ChatModel:         model,
			}

			out, err := runnable.Invoke(ctx, input)
			if err != nil {
				t.Fatalf("Invoke 失败: %v", err)
			}
			if out == nil || out.Stream == nil {
				t.Fatal("Invoke 返回 nil 出参/流")
			}
			drainStream(out.Stream)

			// 成本模型：只有「含指代」才允许真的调一次 LLM 去改写
			if gen, _ := model.counts(); gen != tc.wantGen {
				t.Errorf("改写式 LLM 调用（Generate）=%d 次，期望 %d —— 改写节点只在含指代时才该调 LLM", gen, tc.wantGen)
			}
			// SkipRetrieve 有没有一路传到检索节点
			if len(out.Docs) != tc.wantDocs {
				t.Errorf("出参 docs 数量=%d，期望 %d（SkipRetrieve 没有从改写结果传到检索节点？）",
					len(out.Docs), tc.wantDocs)
			}
			// 改写结果有没有真的进 prompt（不是只被写进某个字段）
			lastUser := findLastMessageByRole(model.prompt(), "user")
			if lastUser == nil {
				t.Fatal("生成节点没有收到任何 user 消息")
			}
			if lastUser.Content != tc.wantPrompt {
				t.Errorf("prompt 里最后一条 user 消息=%q，期望 %q", lastUser.Content, tc.wantPrompt)
			}
			// 正常路径必须给出回答流，而不是澄清
			if out.Clarify != nil {
				t.Errorf("本用例不该走澄清分支，却拿到 Clarify=%+v", out.Clarify)
			}
		})
	}
}

// TestQuickRewriteNode_FallsBackToOriginalQueryOnLLMFailure
//
// 容错回归：含指代时确实调了 1 次 LLM，但模型返回的不是 JSON（不听话）——
// 改写节点必须回退到原问题继续跑（不报错、也不额外补调一次），后面检索与生成照常。
// 这是「改写失败不能拖垮整条链路」的运行时证据，也是 doRewriteWithLLM 永不返回 nil 的依据。
func TestQuickRewriteNode_FallsBackToOriginalQueryOnLLMFailure(t *testing.T) {
	ensureTestConfig(t)

	runnable, err := compileQuickGraph(rag.NewEinoRetrieverAdapter(&barrierRetriever{}, 10))
	if err != nil {
		t.Fatalf("compileQuickGraph 失败: %v", err)
	}

	model := &recordingChatModel{genReply: "这不是 JSON，模型今天不想好好说话"}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	input := &quickGraphInput{
		OriginalQuery:     "那个方案呢",
		UserID:            "u-1",
		KnowledgeBaseIDs:  []string{"kb-1"},
		InputMsgs:         []*schema.Message{schema.UserMessage("那个方案呢")},
		UserQuestionIndex: 0,
		ModelName:         "cl100k_base",
		RetrievalBudget:   2000,
		ChatModel:         model,
	}

	out, err := runnable.Invoke(ctx, input)
	if err != nil {
		t.Fatalf("LLM 返回垃圾时应 fallback 继续跑，而不是让整条链路失败: %v", err)
	}
	if out == nil || out.Stream == nil {
		t.Fatal("Invoke 返回 nil 出参/流")
	}
	drainStream(out.Stream)

	if gen, _ := model.counts(); gen != 1 {
		t.Errorf("Generate 调用=%d 次，期望 1（含指代应尝试一次改写）", gen)
	}
	// fallback 落到原问题 + question 意图 → 仍然检索
	if len(out.Docs) != 1 {
		t.Errorf("出参 docs 数量=%d，期望 1（fallback 后仍应检索）", len(out.Docs))
	}
	lastUser := findLastMessageByRole(model.prompt(), "user")
	if lastUser == nil || lastUser.Content != "那个方案呢" {
		t.Errorf("prompt 里最后一条 user 消息=%v，期望回退到原问题", lastUser)
	}
}

// forbidRetriever 一旦被调用就让用例失败，用于断言「这条分支不该走到检索」。
type forbidRetriever struct{ t *testing.T }

func (f *forbidRetriever) Retrieve(context.Context, rag.Query) (rag.Result, error) {
	f.t.Error("检索节点被调用了，但本轮应该在改写之后就分叉到 clarify_end")
	return rag.Result{}, nil
}

// TestQuickClarifyBranch_ShortCircuitsRetrieveAndGenerate
//
// 行为回归：改写判定 need_clarify=true 时，本轮只该走到 clarify_end——
//   - 出参给的是 Clarify（追问文本 + 选项 + 意图），不是回答流；
//   - 检索节点一次都没被调（不能白花一次知识库查询）；
//   - 生成节点一次都没被调（不能白花一整轮 LLM）。
//
// 这三条正是「把澄清判定放进图里、作为一个真分支」换来的东西：
// 旧实现靠图外提前 return 达到同样效果，但那条捷径与图内的分叉点是两份定义。
func TestQuickClarifyBranch_ShortCircuitsRetrieveAndGenerate(t *testing.T) {
	ensureTestConfig(t)

	const clarifyQ = "你是要导出哪些数据？是单个知识库还是全部知识库？"
	forbid := &forbidRetriever{t: t}
	runnable, err := compileQuickGraph(rag.NewEinoRetrieverAdapter(forbid, 10))
	if err != nil {
		t.Fatalf("compileQuickGraph 失败: %v", err)
	}

	model := &recordingChatModel{genReply: `{"rewritten":"","intent":"question","keywords":[],"need_clarify":true,` +
		`"clarify_question":"` + clarifyQ + `","clarify_options":["单个知识库","全部知识库"]}`}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	input := &quickGraphInput{
		// 含指代表达 → 改写节点才会真的调 LLM，从而拿到 need_clarify=true
		OriginalQuery:     "那个方案呢",
		UserID:            "u-1",
		KnowledgeBaseIDs:  []string{"kb-1"},
		InputMsgs:         []*schema.Message{schema.UserMessage("那个方案呢")},
		UserQuestionIndex: 0,
		ModelName:         "cl100k_base",
		RetrievalBudget:   2000,
		ChatModel:         model,
	}

	out, err := runnable.Invoke(ctx, input)
	if err != nil {
		t.Fatalf("Invoke 失败: %v", err)
	}
	if out == nil {
		t.Fatal("Invoke 返回 nil 出参")
	}

	if out.Clarify == nil {
		t.Fatalf("期望走澄清分支，实际 Clarify=nil（出参=%+v）", out)
	}
	if out.Clarify.Question != clarifyQ {
		t.Errorf("追问文本=%q，期望 %q", out.Clarify.Question, clarifyQ)
	}
	if len(out.Clarify.Options) != 2 {
		t.Errorf("追问选项=%v，期望 2 个", out.Clarify.Options)
	}
	// 不变式：澄清与回答流互斥
	if out.Stream != nil {
		out.Stream.Close()
		t.Error("澄清路径不该同时给出回答流（quickGraphOutput 的不变式被破坏）")
	}
	if len(out.Docs) != 0 {
		t.Errorf("澄清路径不该有检索结果，实际 %d 条", len(out.Docs))
	}

	gen, stream := model.counts()
	if gen != 1 {
		t.Errorf("Generate 调用=%d 次，期望 1（改写那一次）", gen)
	}
	if stream != 0 {
		t.Errorf("生成节点 Stream 调用=%d 次，期望 0 —— 澄清分支不该走到生成", stream)
	}
}

// ─── 结构守卫 ───────────────────────────────────────────────────────────────

// graphFileForGuard 供守卫解析的目标文件：改写链路全部集中在这一个文件里。
const graphFileForGuard = "chat_service_graph_quick.go"

// rewriteOwnerFunc 唯一允许调用 doRewriteWithLLM 的函数（Graph 的改写节点）。
const rewriteOwnerFunc = "quickRewriteFn"

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
	// ① 生产侧：Graph 入参不得再携带改写结果（改写已搬进图内，图外不再预跑）
	in, fset := findStructType(t, graphFileForGuard, "quickGraphInput")
	var staleCarriers []string
	for _, fld := range in.Fields.List {
		for _, n := range fld.Names {
			if strings.HasPrefix(n.Name, "Pre") {
				staleCarriers = append(staleCarriers, n.Name+" "+typeString(fset, fld.Type))
			}
		}
	}
	if len(staleCarriers) != 0 {
		t.Errorf("quickGraphInput 里仍有 Pre* 字段 %v。\n"+
			"改写已经搬进 Graph（quickRewriteFn 自己调 doRewriteWithLLM），图外不再预跑 —— "+
			"改写结果只能由 quickGraphPayload.Rewrite 一个字段承载。", staleCarriers)
	}

	// ② 传递侧：载荷里恰好一个字段承载改写结果，且类型是 *rewriteResult
	pl, pfset := findStructType(t, graphFileForGuard, "quickGraphPayload")
	var carriers []string
	for _, fld := range pl.Fields.List {
		if typeString(pfset, fld.Type) != "*rewriteResult" {
			continue
		}
		for _, n := range fld.Names {
			carriers = append(carriers, n.Name)
		}
	}
	if len(carriers) != 1 || carriers[0] != "Rewrite" {
		t.Errorf("quickGraphPayload 里承载改写结果的字段 = %v，期望恰好一个 [Rewrite]。\n"+
			"平行字段必须成组、按序、在多个调用点赋值，漏写一个不会编译报错、只会静默丢语义——"+
			"新增改写产出请加进 rewriteResult 结构体，不要再开平行字段。", carriers)
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

// findLastMessageByRole 找 msgs 中指定 role 的最后一条消息。
//
// 原先住在生产文件 chat_service_graph_quick.go 里，唯一用途是给「自研 span 树」的
// prompt 属性取值；那条链路随可观测模块一起删除后，只剩本文件用它断言
// 「改写结果确实进了 prompt」（而不只是被写进了某个字段）。
// 于是把它挪到测试里，避免生产代码里留一个只有测试用的 helper。
func findLastMessageByRole(msgs []*schema.Message, role string) *schema.Message {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i] != nil && string(msgs[i].Role) == role {
			return msgs[i]
		}
	}
	return nil
}
