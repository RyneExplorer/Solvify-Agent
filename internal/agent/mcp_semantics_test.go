package agent

import (
	"context"
	"testing"

	einoTool "github.com/cloudwego/eino/components/tool"
)

// countingToolFactory 记录 CreateAgentTools 被调用的次数与收到的白名单。//
// 这条计数是本文件全部断言的基础：MCP 工具的真实代价不在返回值的数量上，
// 而在 CreateAgentTools 内部会**握手 MCP 子进程**（失败时按配置 timeout 同步阻塞）。
// 所以「有没有挂 MCP」必须看**调用没调用**，不能只看最终桶里有没有工具。
type countingToolFactory struct {
	calls  int
	lastID []string // 最近一次收到的 userConfigIDs
	names  []string // 每次调用都返回这些工具
}

func (c *countingToolFactory) CreateAgentTools(_ context.Context, _ string, ids ...string) []einoTool.BaseTool {
	c.calls++
	c.lastID = ids
	out := make([]einoTool.BaseTool, 0, len(c.names))
	for _, n := range c.names {
		out = append(out, fakeTool{n})
	}
	return out
}

func newCountingEngine(mcpNames ...string) (*Engine, []internalToolRegistryEntry) {
	f := &countingToolFactory{names: mcpNames}
	e := &Engine{toolFactory: f}
	for _, spec := range []struct {
		name string
		dom  ToolDomain
	}{
		{"knowledge_search", DomainKnowledge},
		{"delete_document", DomainDocMgmt},
		{"ask_clarify", DomainClarify},
	} {
		s := spec
		e.RegisterInternal(s.name, 1, false, s.dom,
			func(_ context.Context, _ string, _ []string) einoTool.BaseTool {
				return fakeTool{s.name}
			})
	}
	return e, e.sortedInternalTools()
}

// TestQuickProfileNeverBuildsMCPTools 守「快速模式不触发 MCP 握手」。
//
// 这是本次修复的核心承诺：快速模式的域不含 external，因此 buildToolsByDomain
// 在 `want[DomainExternal]` 处就该短路，**一次都不该调用** CreateAgentTools。
//
// 为什么这条能真的变红（不是恒真）：把 buildToolsByDomain 里的
// `if want[DomainExternal] && allowDynamic` 退化回 `if want[DomainExternal]` 时，
// 计数从 0 变成 1，断言立刻失败 —— 用 -overlay 注入该变异即可验证。
func TestQuickProfileNeverBuildsMCPTools(t *testing.T) {
	e, sorted := newCountingEngine("mcp_a", "mcp_b")
	f := e.toolFactory.(*countingToolFactory)

	buckets := e.buildToolsByDomain(context.Background(), sorted, "u1", nil, QuickToolDomains, true)

	if f.calls != 0 {
		t.Fatalf("快速模式调用了 %d 次 CreateAgentTools（应 0 次，否则每请求白等一次 MCP 握手）", f.calls)
	}
	if got := len(buckets[DomainExternal]); got != 0 {
		t.Fatalf("快速模式 external 桶有 %d 个工具（应 0）", got)
	}
}

// TestQuickProfileEstimateTokensSkipsMCP 是上一条经生产入口的佐证。
//
// 上一条直接调 buildToolsByDomain，可能绕过「谁传了什么 domains」；
// 这条走 EstimateToolsTokens（真实调用点），确保 profile→domains 的换算也没把 external 带进来。
func TestQuickProfileEstimateTokensSkipsMCP(t *testing.T) {
	e, _ := newCountingEngine("mcp_a")
	f := e.toolFactory.(*countingToolFactory)

	if _, _, err := e.EstimateToolsTokens(context.Background(), "u1", nil, "gpt-4", ProfileQuick, false); err != nil {
		t.Fatalf("估算失败: %v", err)
	}
	if f.calls != 0 {
		t.Fatalf("EstimateToolsTokens(Quick) 调用了 %d 次 CreateAgentTools（应 0 次）", f.calls)
	}

	if _, _, err := e.EstimateToolsTokens(context.Background(), "u1", nil, "gpt-4", ProfileDeep, false); err != nil {
		t.Fatalf("估算失败: %v", err)
	}
	if f.calls != 1 {
		t.Fatalf("EstimateToolsTokens(Deep) 调用了 %d 次 CreateAgentTools（应 1 次）", f.calls)
	}
}

// TestMCPDisabledMountsNoMCPTools 守「显式不使用任何 MCP」。
//
// 退化形态：allowDynamic 判断被删掉 ⇒ 用户明明一个都没选，MCP 工具仍被挂上，
// 于是模型可能去调一个用户刻意排除的服务器。
func TestMCPDisabledMountsNoMCPTools(t *testing.T) {
	e, sorted := newCountingEngine("mcp_a", "mcp_b")
	f := e.toolFactory.(*countingToolFactory)

	// allowDynamic=false = 用户显式选择「不使用任何 MCP」
	buckets := e.buildToolsByDomain(context.Background(), sorted, "u1", nil, AllToolDomains, false)

	if f.calls != 0 {
		t.Fatalf("禁用 MCP 时调用了 %d 次 CreateAgentTools（应 0 次，连握手都不该发生）", f.calls)
	}
	if got := len(buckets[DomainExternal]); got != 0 {
		t.Fatalf("禁用 MCP 时 external 桶有 %d 个工具（应 0）", got)
	}
	// 对照组：内置工具必须照旧挂上 —— 禁用 MCP 不能连知识检索一起禁掉
	if got := len(buckets[DomainKnowledge]); got != 1 {
		t.Fatalf("禁用 MCP 后 knowledge 桶=%d（应 1，内置工具不受影响）", got)
	}
	if got := len(buckets[DomainDocMgmt]); got != 1 {
		t.Fatalf("禁用 MCP 后 doc_mgmt 桶=%d（应 1）", got)
	}
}

// TestMCPEnabledPassesWhitelistThrough 守「启用时白名单原样透传」。
//
// 与上一条构成双向对照：只有「禁用→0 次调用」而没有「启用→1 次调用」，
// 断言可能因为「永远不调用」而恒真（那样深度模式的 MCP 会静默全丢）。
func TestMCPEnabledPassesWhitelistThrough(t *testing.T) {
	e, sorted := newCountingEngine("mcp_a", "mcp_b")
	f := e.toolFactory.(*countingToolFactory)

	buckets := e.buildToolsByDomain(context.Background(), sorted, "u1", nil, AllToolDomains, true, "cfg-1", "cfg-2")

	if f.calls != 1 {
		t.Fatalf("启用 MCP 时调用了 %d 次 CreateAgentTools（应恰好 1 次）", f.calls)
	}
	if len(f.lastID) != 2 || f.lastID[0] != "cfg-1" || f.lastID[1] != "cfg-2" {
		t.Fatalf("白名单未原样透传，实际 %v", f.lastID)
	}
	if got := len(buckets[DomainExternal]); got != 2 {
		t.Fatalf("external 桶=%d（应 2）", got)
	}
}

// TestBuildToolsByDomainOnlyBuildsRequestedDomains 守「分域构建」本身。
//
// 上一条只看 CreateAgentTools 的调用次数，因此**看不见**域过滤循环的退化：
// 若把 `if !want[entry.Domain] { continue }` 去掉，内置工具会全量进桶，
// 而 want[DomainExternal] 仍由 domains 算出（快速模式依旧 false）⇒ 调用计数不变，
// 上一条不会红。这条补上那个盲区。
//
// 实测（-overlay 注入「域过滤恒不过滤」）：不加这条时全部用例仍绿。
func TestBuildToolsByDomainOnlyBuildsRequestedDomains(t *testing.T) {
	e, sorted := newCountingEngine("mcp_a")

	// 只请求知识域：其余域必须为空桶
	buckets := e.buildToolsByDomain(context.Background(), sorted, "u1", nil, []ToolDomain{DomainKnowledge}, true)

	if got := len(buckets[DomainKnowledge]); got != 1 {
		t.Fatalf("knowledge 桶=%d（应 1）", got)
	}
	for _, d := range []ToolDomain{DomainDocMgmt, DomainClarify, DomainExternal} {
		if got := len(buckets[d]); got != 0 {
			t.Fatalf("未请求的域 %s 桶=%d（应 0）—— 域过滤失效，工具会越权挂载", d, got)
		}
	}
}

// TestBuildToolsByDomainStillBuildsInternalToolsWhenMCPDisabled 守「禁用 MCP ≠ 禁用内置工具」。
//
// allowDynamic=false 只该砍掉动态工具；若把短路条件写成「直接 return 空桶」，
// 用户一旦取消勾选 MCP，知识检索与文档管理会**一起消失**且没有任何报错。
func TestBuildToolsByDomainStillBuildsInternalToolsWhenMCPDisabled(t *testing.T) {
	e, sorted := newCountingEngine("mcp_a")

	buckets := e.buildToolsByDomain(context.Background(), sorted, "u1", nil, AllToolDomains, false)

	if got := len(buckets[DomainKnowledge]); got != 1 {
		t.Fatalf("禁用 MCP 后 knowledge 桶=%d（应 1）", got)
	}
	if got := len(buckets[DomainDocMgmt]); got != 1 {
		t.Fatalf("禁用 MCP 后 doc_mgmt 桶=%d（应 1）", got)
	}
	if got := len(buckets[DomainClarify]); got != 1 {
		t.Fatalf("禁用 MCP 后 clarify 桶=%d（应 1）", got)
	}
}

// TestDeepOrchestratorPrebuildKeepsExternal 守「深度编排预构建必须含 external」。
//
// Planner 靠 len(buckets[DomainExternal]) > 0 决定能否规划 web_research。
// 若预构建漏了 external，深度模式会**静默失去联网研究能力**且没有任何报错。
func TestDeepOrchestratorPrebuildKeepsExternal(t *testing.T) {
	e, sorted := newCountingEngine("mcp_web")
	f := e.toolFactory.(*countingToolFactory)

	buckets := e.buildToolsByDomain(context.Background(), sorted, "u1", nil, AllToolDomains, true)

	if f.calls != 1 {
		t.Fatalf("深度编排预构建调用了 %d 次 CreateAgentTools（应 1 次）", f.calls)
	}
	if got := len(buckets[DomainExternal]); got == 0 {
		t.Fatal("深度编排预构建的 external 桶为空 ⇒ web_research 会被静默禁用")
	}
}
