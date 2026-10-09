package agent

import (
	"context"
	"strings"
	"testing"
)

// ── B-1b 守卫 ──
//
// 这一批守「快速模式真的是另一个 Agent」，三个真实缺陷形态：
//  ① prompt 里列了本模式没挂的工具 ⇒ 模型照着调，被 UnknownToolsHandler 兜住；
//  ② 快速模式沿用深度模式的「先检索再回答 / 联网搜索」文案 ⇒ 闲聊也去查库，
//     而且告知模型有它根本没有的联网能力；
//  ③ 改 quick_agent_max_iterations 只改配置不改 prompt ⇒ 两处上限对不上。
//
// 全部经selectProfileTools 取工具集 —— 测试也必须走生产路径，
// 否则就会重演「测试自己先过滤好再喂给 prompt」那种近乎恒真的断言。

// quickToolSet 返回快速模式下真实挂载的工具集与 prompt 清单。
func quickToolSet(t *testing.T, mcpNames []string) profileToolSet {
	t.Helper()
	e, sorted := newProfileTestEngine(mcpNames)
	return selectProfileTools(sorted, e.buildToolsByDomain(context.Background(), sorted, "u1", []string{"kb1"}, AllToolDomains, true), ProfileQuick)
}

// TestSelectProfileToolsPromptMatchesMounted 守 ①：prompt 清单 = 实际挂载，双向比对。
//
// 只做单向（清单 ⊆ 挂载）不够 —— 漏挂一个工具同样会引发「模型调不到」。
// 判据是「同一个 selectProfileTools 产物」，所以结构上无法喂进不一致的组合。
func TestSelectProfileToolsPromptMatchesMounted(t *testing.T) {
	ts := quickToolSet(t, []string{"mcp_search"})
	mounted := map[string]bool{}
	for _, n := range ts.namesOf() {
		mounted[n] = true
	}
	listed := map[string]bool{}
	for _, entry := range ts.Entries {
		listed[entry.Name] = true
	}
	for _, entry := range ts.Entries {
		if !mounted[entry.Name] {
			t.Fatalf("prompt 列了 %s，但实际没挂这个工具", entry.Name)
		}
	}
	for name := range mounted {
		if !listed[name] {
			t.Fatalf("工具 %s 实际挂上了，但 prompt 清单里没有 ⇒ 模型不知道自己能用它", name)
		}
	}
}

// TestSelectProfileToolsDeepKeepsEverything 是上一条的对照组：
// 深度模式仍拿到全量工具（含危险工具与MCP）。
func TestSelectProfileToolsDeepKeepsEverything(t *testing.T) {
	e, sorted := newProfileTestEngine([]string{"mcp_a"})
	ts := selectProfileTools(sorted, e.buildToolsByDomain(context.Background(), sorted, "u1", nil, AllToolDomains, true), ProfileDeep)
	got := ts.namesOf()
	for _, want := range []string{"knowledge_search", "grep_chunks", "delete_document", "ask_clarify", "mcp_a"} {
		found := false
		for _, n := range got {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("深度模式缺工具 %s（实际 %v）", want, got)
		}
	}
	if len(ts.Entries) != 4 {
		t.Fatalf("深度模式清单条目=%d 期望 4", len(ts.Entries))
	}
}

// TestQuickPromptHasNoUnmountedTools 守 ①：快速模式 prompt 不得出现危险工具与 MCP。
//
// 退化形态：selectProfileTools 的 Entries 不过滤（回退成 sorted 全量），
// 或 buildSystemPrompt 忽略 profile 一律走深度分支。任一发生都能命中。
func TestQuickPromptHasNoUnmountedTools(t *testing.T) {
	ts := quickToolSet(t, []string{"mcp_search", "mcp_delete_file"})
	p := buildSystemPrompt(context.Background(), ProfileQuick, ts, 1)

	for _, banned := range []string{"delete_document", "mcp_search", "mcp_delete_file"} {
		if strings.Contains(p, banned) {
			t.Fatalf("快速模式 prompt 里出现了未挂载的工具 %s", banned)
		}
	}
	for _, want := range []string{"knowledge_search", "grep_chunks", "ask_clarify"} {
		if !strings.Contains(p, want) {
			t.Fatalf("快速模式 prompt 缺少已挂载工具 %s", want)
		}
	}
}

// TestQuickPromptOmitsDeepOnlyRules 守 ②：快速模式文案不含深度模式专属规则。
//
// 判据挑了**语义上互不重叠**的两条深度规则，每条都能独立证明文案分支真的换了：
// 「先检索再回答」与「联网搜索」。深度模式反过来也必须仍有 ⇒防「判反了」。
func TestQuickPromptOmitsDeepOnlyRules(t *testing.T) {
	// 深度对照组用全量工具集（含 MCP），否则「联网搜索」小节根本不出现
	e, sorted := newProfileTestEngine([]string{"mcp_search"})
	deep := selectProfileTools(sorted, e.buildToolsByDomain(context.Background(), sorted, "u1", nil, AllToolDomains, true), ProfileDeep)
	quick := selectProfileTools(sorted, e.buildToolsByDomain(context.Background(), sorted, "u1", nil, AllToolDomains, true), ProfileQuick)

	qp := buildSystemPrompt(context.Background(), ProfileQuick, quick, 1)
	dp := buildSystemPrompt(context.Background(), ProfileDeep, deep, 3)

	for _, deepOnly := range []string{"先检索再回答", "联网搜索"} {
		if strings.Contains(qp, deepOnly) {
			t.Fatalf("快速模式 prompt 含深度模式专属规则 %q", deepOnly)
		}
		if !strings.Contains(dp, deepOnly) {
			t.Fatalf("深度模式 prompt 丢了规则 %q（说明分支判断反了）", deepOnly)
		}
	}
	for _, common := range []string{"引用规则", "回答要求", "ask_clarify"} {
		if !strings.Contains(qp, common) || !strings.Contains(dp, common) {
			t.Fatalf("两模式都该有的 %q 缺了：quick=%v deep=%v", common,
				strings.Contains(qp, common), strings.Contains(dp, common))
		}
	}
}

// TestQuickPromptAllowsDirectAnswer 守 ②的正向部分：明确授权「不检索直接答」。
//
// 这是丢掉 localIntentRules 之后的兜底 —— 文案里没这句，模型在深度模式
// 养成的「先检索」惯性会让快速模式对「你好」也去查库。
func TestQuickPromptAllowsDirectAnswer(t *testing.T) {
	qp := buildSystemPrompt(context.Background(), ProfileQuick, quickToolSet(t, nil), 1)
	// 判据钉在「不要调用任何工具」这句唯一措辞上。
	// ⚠️ 不能只判「直接回答」：禁止段里也有一句「（直接回答更快，也更准）」，
	// 改掉工作原则那句时禁止段仍在 ⇒ 断言恒绿。
	if !strings.Contains(qp, "不要调用任何工具") {
		t.Fatalf("快速模式 prompt 缺少「不调用工具直接回答」的授权，闲聊也会触发查库：%s", excerptOf(qp))
	}
	// 双向：也不能反过来写成「所有问题都必须检索」
	if strings.Contains(qp, "所有问题都必须先调用") {
		t.Fatal("快速模式 prompt 要求所有问题都检索 ⇒ 闲聊也会查库")
	}
}

// TestQuickPromptCarriesRealtimeAndGeneralKnowledgeRules 钉住快速模式 prompt 的两条口径：
//
//	① 实时类要「明确告知 + 指向深度模式」（快速模式没有联网/天气/新闻工具）；
//	② 没有检索到知识库内容时必须允许用通用知识并显式标注（此前是「禁止靠常识补全」，堵死常识回答）。
//
// 断言的是生产代码真正喂给 LLM 的 prompt 本身，不是在测试里复刻一份。
func TestQuickPromptCarriesRealtimeAndGeneralKnowledgeRules(t *testing.T) {
	qp := buildSystemPrompt(context.Background(), ProfileQuick, quickToolSet(t, nil), 1)
	for _, sub := range []string{
		"## 实时信息处理",
		"深度模式",
		"以下内容基于通用知识，未来自知识库",
	} {
		if !strings.Contains(qp, sub) {
			t.Errorf("快速模式 prompt 缺少 %q", sub)
		}
	}
	if strings.Contains(qp, "禁止靠常识补全") {
		t.Error("快速模式 prompt 仍含「禁止靠常识补全」：这条会堵死通用知识回答")
	}
}

// TestPromptToolCallLimitFollowsIterations 守 ③：文案里的工具上限跟着参数走。
//
// 用不等式/包含断言而不是精确 token 数：参数变了上限文案必须跟着变。
func TestPromptToolCallLimitFollowsIterations(t *testing.T) {
	ts := quickToolSet(t, nil)
	p1 := buildSystemPrompt(context.Background(), ProfileQuick, ts, 1)
	p9 := buildSystemPrompt(context.Background(), ProfileQuick, ts, 9)
	if !strings.Contains(p1, "工具上限 1 次") {
		t.Fatalf("prompt 未反映 maxToolCalls=1：%s", excerptOf(p1))
	}
	if !strings.Contains(p9, "工具上限 9 次") {
		t.Fatalf("prompt 未反映 maxToolCalls=9：%s", excerptOf(p9))
	}
	if p1 == p9 {
		t.Fatal("maxToolCalls 变化但 prompt 完全相同 ⇒ 文案里根本没接这个参数")
	}
}

// TestMaxToolCallsDerivation 断言换算规则：工具轮 = 模型轮 − 1。
//
// 边界必须钉住：MaxIterations=1 时工具轮至少给 1（给 0 的话 prompt 会写
// 「工具上限 0 次」而实际仍可调用，模型直接不敢调工具）。
func TestMaxToolCallsDerivation(t *testing.T) {
	cases := []struct{ in, want int }{{1, 1}, {2, 1}, {3, 2}, {5, 4}, {0, 1}, {-3, 1}}
	for _, c := range cases {
		if got := maxToolCalls(c.in); got != c.want {
			t.Fatalf("maxToolCalls(%d)=%d 期望 %d", c.in, got, c.want)
		}
	}
	// 换算链（配置 → 迭代轮 → 工具上限）的实际取值必须 ≥1
	if got := maxToolCalls(deepMaxIterations(4)); got != 3 {
		t.Fatalf("maxToolCalls(deepMaxIterations(4))=%d 期望 3", got)
	}
}

// TestComposeSystemPromptOverride 断言用户自定义 prompt 的拼接规则。
func TestComposeSystemPromptOverride(t *testing.T) {
	if got := composeSystemPrompt("BASE", ""); got != "BASE" {
		t.Fatalf("空 override 应原样返回，实际 %q", got)
	}
	if got := composeSystemPrompt("BASE", "\n\nUSER"); got != "BASE\n\nUSER" {
		t.Fatalf("前导换行未被 Trim，实际 %q", got)
	}
	// 追加者侧的多余空格**不 trim**：TrimLeft 的 cutset 只有换行字符，
	// 改它就是本次之外的行为变更，且会把用户非空导语的前缩进去掉。
	if got := composeSystemPrompt("BASE", "  尾巴"); got != "BASE\n\n  尾巴" {
		t.Fatalf("拼接结果=%q", got)
	}
}

// TestQuickAgentUsesItsOwnIdentity 断言两模式 Agent 身份名不同。
// 同名的话 Langfuse 上根本分不出哪条 trace 是快速模式。
func TestQuickAgentUsesItsOwnIdentity(t *testing.T) {
	if deepAgentName == quickAgentName {
		t.Fatalf("两个模式同名 %q，平台上无法区分", deepAgentName)
	}
	for _, n := range []string{deepAgentName, quickAgentName} {
		if !strings.HasPrefix(n, "Solvify") || n == "" {
			t.Fatalf("Agent 名 %q 不符合约定", n)
		}
	}
}

// TestQuickMaxIterationsIndependentFromDeep 断言两模式的迭代上限各读各的配置项。
// 退化形态：快速模式读 cfg.MaxIterations ⇒ 改深度配置会连带改快速模式。
func TestQuickMaxIterationsIndependentFromDeep(t *testing.T) {
	base, _ := newProfileTestEngine(nil)
	base.cfg.MaxIterations = 4
	base.cfg.QuickAgentMaxIterations = 2
	if got := base.quickMaxIterations(); got != 2 {
		t.Fatalf("quickMaxIterations=%d 期望 2（须读 QuickAgentMaxIterations）", got)
	}
	if got := deepMaxIterations(base.cfg.MaxIterations); got != 4 {
		t.Fatalf("deepMaxIterations=%d 期望 4", got)
	}
	base.cfg.MaxIterations = 9
	if got := base.quickMaxIterations(); got != 2 {
		t.Fatalf("改深度配置后 quickMaxIterations 变成 %d ⇒ 两个模式共用了一个配置项", got)
	}

	// 零值兜底：直接构造 Engine（不经配置校验）时不能返回 0，
	// 否则 eino 收到 0 会走「无上限」，与「配置缺失」的真实意图相反。
	zero, _ := newProfileTestEngine(nil)
	if got := zero.quickMaxIterations(); got != 2 {
		t.Fatalf("零值兜底=%d 期望 2", got)
	}
	if got := deepMaxIterations(0); got != 5 {
		t.Fatalf("深度零值兜底=%d 期望 5", got)
	}
}

func excerptOf(s string) string {
	if len(s) <= 200 {
		return s
	}
	return s[:200]
}
