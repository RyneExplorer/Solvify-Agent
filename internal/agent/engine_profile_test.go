package agent

import (
	"context"
	"testing"

	einoTool "github.com/cloudwego/eino/components/tool"
)

// stubToolFactory 只实现 CreateAgentTools，返回固定几个「用户 MCP 工具」。
type stubToolFactory struct {
	names    []string
	buildErr error
}

func (s stubToolFactory) CreateAgentTools(_ context.Context, _ string, _ ...string) []einoTool.BaseTool {
	out := make([]einoTool.BaseTool, 0, len(s.names))
	for _, n := range s.names {
		out = append(out, fakeTool{n})
	}
	return out
}

func newProfileTestEngine(mcpNames []string) (*Engine, []internalToolRegistryEntry) {
	e := &Engine{toolFactory: stubToolFactory{names: mcpNames}}
	for _, spec := range []struct {
		name   string
		order  int
		danger bool
		dom    ToolDomain
	}{
		{"knowledge_search", 1, false, DomainKnowledge},
		{"grep_chunks", 2, false, DomainKnowledge},
		{"delete_document", 10, true, DomainDocMgmt},
		{"ask_clarify", 20, false, DomainClarify},
	} {
		s := spec
		e.RegisterInternal(s.name, s.order, s.danger, s.dom,
			func(_ context.Context, _ string, _ []string) einoTool.BaseTool {
				return fakeTool{s.name}
			})
	}
	return e, e.sortedInternalTools()
}

// TestQuickProfileExcludesDangerousAndExternalTools 守卫快速模式的核心承诺：
// 从预构建 ctx 复用工具时，不能把危险工具 / 用户 MCP 工具带进来。
//
// 为什么这条能真的变红：B-1a 之前 prebuiltToolsBundle.Tools 是**拍平的全量**，
// 快速模式复用时会捞到全部工具。此处若 bundle.tools()退化成「返回全部桶内容」，
// delete_document 与 mcp_* 立刻出现，断言失败。
func TestQuickProfileExcludesDangerousAndExternalTools(t *testing.T) {
	e, sorted := newProfileTestEngine([]string{"mcp_search", "mcp_write"})
	buckets := e.buildToolsByDomain(context.Background(), sorted, "u1", []string{"kb1"}, AllToolDomains, true)

	quick := prebuiltToolsBundle{Sorted: sorted, Buckets: buckets}.tools(ProfileQuick.toolDomains()...)
	got := toolNames(quick)

	for _, banned := range []string{"delete_document", "mcp_search", "mcp_write"} {
		for _, n := range got {
			if n == banned {
				t.Fatalf("快速模式工具集里出现了 %s（实际 %v）", banned, got)
			}
		}
	}
	for _, want := range []string{"knowledge_search", "grep_chunks", "ask_clarify"} {
		found := false
		for _, n := range got {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("快速模式工具集缺少 %s（实际 %v）", want, got)
		}
	}
}

// TestQuickProfileBundleReuseEqualsFreshBuild 断言「复用预构建」与「现场构建」
// 在快速模式下得到**完全相同**的工具集。
//
// 这是 B-1a 的核心不变式：两条路径只要有一个工具不同，就会出现「有时带 MCP、
// 有时不带」的间歇性行为。这类缺陷极难靠读代码发现，因为两条路径都"看着对"。
func TestQuickProfileBundleReuseEqualsFreshBuild(t *testing.T) {
	e, sorted := newProfileTestEngine([]string{"mcp_a", "mcp_b"})

	// 路径1：现场构建
	fresh := toolsInDomains(sorted, e.buildToolsByDomain(context.Background(), sorted, "u1", []string{"kb1"}, AllToolDomains, true),
		ProfileQuick.toolDomains()...)
	// 路径 2：从预构建 bundle 复用
	reused := prebuiltToolsBundle{
		Sorted:  sorted,
		Buckets: e.buildToolsByDomain(context.Background(), sorted, "u1", []string{"kb1"}, AllToolDomains, true),
	}.tools(ProfileQuick.toolDomains()...)

	assertNames(t, fresh, toolNames(reused))
	assertNames(t, reused, []string{"knowledge_search", "grep_chunks", "ask_clarify"})
}

// TestDeepProfileBundleReuseKeepsAllTools 是快速模式的对照组：
// 深度模式必须仍然拿到全部工具（含危险工具与 MCP）。
//
// 没有它，上一条可能因为「分域取用整体失效、只返回知识域」而照样绿 ——
// 那会让深度模式静默丢掉 delete_document，用户再也没法删文档。
func TestDeepProfileBundleReuseKeepsAllTools(t *testing.T) {
	e, sorted := newProfileTestEngine([]string{"mcp_a"})
	buckets := e.buildToolsByDomain(context.Background(), sorted, "u1", []string{"kb1"}, AllToolDomains, true)

	got := toolNames(prebuiltToolsBundle{Sorted: sorted, Buckets: buckets}.tools(ProfileDeep.toolDomains()...))
	assertNames(t, mustTools(got), []string{
		"knowledge_search", "grep_chunks", "delete_document", "ask_clarify", "mcp_a",
	})
}

// TestProfileToolDomainsUnknownValueFallsBackToAll 断言未知 Profile 落全域。
//
// 与 effectiveProfile 同一口径。退化实现（未知值落空集）会让拼错的 Profile
// 静默变成「没有任何工具」的 Agent。
func TestProfileToolDomainsUnknownValueFallsBackToAll(t *testing.T) {
	if got := (Profile("typo")).toolDomains(); len(got) != len(AllToolDomains) {
		t.Fatalf("未知 Profile 的域数=%d 期望 %d（应落全域）", len(got), len(AllToolDomains))
	}
	if got := ProfileQuick.toolDomains(); len(got) != 2 {
		t.Fatalf("快速模式域数=%d 期望 2（knowledge+clarify）", len(got))
	}
}

// TestBuildToolsByDomainBuildsEachToolExactlyOnce 断言「构建一遍」。
//
// 这是硬要求：Build 内部查 DB / 建检索器，MCP 还要握手子进程。构建两遍 = 每请求成本翻倍。
// 退化实现（每个域各建一遍）会让 knowledge_search 被 Build 两次。
func TestBuildToolsByDomainBuildsEachToolExactlyOnce(t *testing.T) {
	e := &Engine{toolFactory: stubToolFactory{names: []string{"mcp_a"}}}
	built := map[string]int{}
	for _, spec := range []struct {
		name string
		dom  ToolDomain
	}{
		{"knowledge_search", DomainKnowledge},
		{"grep_chunks", DomainKnowledge},
		{"delete_document", DomainDocMgmt},
		{"ask_clarify", DomainClarify},
	} {
		s := spec
		e.RegisterInternal(s.name, 1, false, s.dom,
			func(_ context.Context, _ string, _ []string) einoTool.BaseTool {
				built[s.name]++
				return fakeTool{s.name}
			})
	}
	sorted := e.sortedInternalTools()
	buckets := e.buildToolsByDomain(context.Background(), sorted, "u1", nil, AllToolDomains, true)

	for name, n := range built {
		if n != 1 {
			t.Fatalf("工具 %s 被构建 %d 次，期望恰好 1 次", name, n)
		}
	}
	// 分桶后总量 = 注册数 + MCP 数，一个不多一个不少
	total := 0
	for _, d := range AllToolDomains {
		total += len(buckets[d])
	}
	if total != 4+1 {
		t.Fatalf("分桶工具总数=%d 期望 5（4 内置 + 1 MCP）", total)
	}
}

// TestEstimateToolsTokensCountsOnlyProfileTools 断言 token 统计按 profile 分域。
//
// 场景：用户配了 20 个 MCP 工具，快速模式一个都不挂。若按全量统计，
// 这 20 个工具的 description 会白白吃掉上下文预算，表现为「快速模式历史被截短」。
//
// 判据用「快速模式 token 数 < 全量 token 数」这个不等式，而不是精确数字 ——
// tokenutil 的具体数值会随模型 tokenizer 变，不等式才是稳定契约。
func TestEstimateToolsTokensCountsOnlyProfileTools(t *testing.T) {
	e, _ := newProfileTestEngine(nil)
	//挂一批动态工具撑开差异
	e.toolFactory = stubToolFactory{names: []string{
		"mcp_tool_with_a_fairly_long_description_one",
		"mcp_tool_with_a_fairly_long_description_two",
		"mcp_tool_with_a_fairly_long_description_three",
		"mcp_tool_with_a_fairly_long_description_four",
	}}

	deepTokens, _, err := e.EstimateToolsTokens(context.Background(), "u1", []string{"kb1"}, "gpt-4", ProfileDeep, false)
	if err != nil {
		t.Fatalf("深度模式估算失败: %v", err)
	}
	quickTokens, quickCtx, err := e.EstimateToolsTokens(context.Background(), "u1", []string{"kb1"}, "gpt-4", ProfileQuick, false)
	if err != nil {
		t.Fatalf("快速模式估算失败: %v", err)
	}
	if quickTokens >= deepTokens {
		t.Fatalf("快速模式 token(%d) 应小于全量(%d)：多挂的 MCP 工具不该计入快速模式预算",
			quickTokens, deepTokens)
	}

	// 返回的 ctx 必须能复用出「只含快速域」的工具集
	bundle, ok := prebuiltToolsFromContext(quickCtx)
	if !ok {
		t.Fatal("EstimateToolsTokens 返回的 ctx 里没有预构建工具集")
	}
	got := toolNames(bundle.tools(ProfileQuick.toolDomains()...))
	assertNames(t, mustTools(got), []string{"knowledge_search", "grep_chunks", "ask_clarify"})
}

// mustTools 把 []string 包成 []BaseTool，供 assertNames 复用
func mustTools(names []string) []einoTool.BaseTool {
	out := make([]einoTool.BaseTool, 0, len(names))
	for _, n := range names {
		out = append(out, fakeTool{n})
	}
	return out
}
