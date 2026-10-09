package agent

import (
	"context"
	"testing"

	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

// fakeTool 只实现 BaseTool（Info），足够验证工具集拼接，无需真的可执行。
type fakeTool struct{ name string }

func (f fakeTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: f.name, Desc: f.name}, nil
}

func toolNames(tools []einoTool.BaseTool) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		info, err := t.Info(context.Background())
		if err != nil || info == nil {
			continue
		}
		out = append(out, info.Name)
	}
	return out
}

func fakeEntry(name string, order int, domain ToolDomain) internalToolRegistryEntry {
	return internalToolRegistryEntry{
		Name:   name,
		Order:  order,
		Domain: domain,
		Build: func(_ context.Context, _ string, _ []string) einoTool.BaseTool {
			return fakeTool{name: name}
		},
	}
}

func assertNames(t *testing.T, got []einoTool.BaseTool, want []string) {
	t.Helper()
	gotNames := toolNames(got)
	if len(gotNames) != len(want) {
		t.Fatalf("工具数=%d(%v) 期望 %d(%v)", len(gotNames), gotNames, len(want), want)
	}
	for i := range want {
		if gotNames[i] != want[i] {
			t.Fatalf("第 %d 位=%s 期望 %s（实际 %v）", i, gotNames[i], want[i], gotNames)
		}
	}
}

// TestToolsInDomainsFiltersAndKeepsOrder 断言按域筛选后，剩余工具仍保持 sorted 的全序。
//
// 前提：桶由 buildToolsByDomain 按同一个 sorted 构建，因此「桶内顺序 == sorted 的过滤子序列」。
// 本例 sorted 里 DomainKnowledge 与 DomainClarify 交错（order 2 / 1 / 3）——
// 若实现改成「整桶 append」或「按桶 map 的 key 顺序拼接」，
// 输出就不是 [bravo, clar, alpha] 而变成 [bravo, alpha, clar] 或别的排列。
func TestToolsInDomainsFiltersAndKeepsOrder(t *testing.T) {
	sorted := []internalToolRegistryEntry{
		fakeEntry("bravo", 2, DomainKnowledge),
		fakeEntry("clar", 1, DomainClarify),
		fakeEntry("alpha", 3, DomainKnowledge),
	}
	buckets := map[ToolDomain][]einoTool.BaseTool{
		DomainKnowledge: {fakeTool{"bravo"}, fakeTool{"alpha"}},
		DomainClarify:   {fakeTool{"clar"}},
		DomainExternal:  {fakeTool{"mcp1"}},
	}

	got := toolsInDomains(sorted, buckets, DomainKnowledge, DomainClarify)
	assertNames(t, got, []string{"bravo", "clar", "alpha"})
}

// TestToolsInDomainsExcludesUnrequestedDomains 是上一条的对照组：
// 未点名的域一个都不能进来（含 DomainExternal 这类动态工具域）。
//
// 没有它，上一条可能退化成「把 buckets 全返回」而照样全绿 ——
// 那意味着快速模式会拿到 delete_document 和全部 MCP 工具，
// 「快速模式碰不到任何需审批工具」这条保证就废了。
func TestToolsInDomainsExcludesUnrequestedDomains(t *testing.T) {
	sorted := []internalToolRegistryEntry{
		fakeEntry("kb1", 1, DomainKnowledge),
		fakeEntry("del", 10, DomainDocMgmt),
		fakeEntry("clar", 20, DomainClarify),
	}
	buckets := map[ToolDomain][]einoTool.BaseTool{
		DomainKnowledge: {fakeTool{"kb1"}},
		DomainDocMgmt:   {fakeTool{"del"}},
		DomainClarify:   {fakeTool{"clar"}},
		DomainExternal:  {fakeTool{"mcp1"}, fakeTool{"mcp2"}},
	}

	got := toolsInDomains(sorted, buckets, DomainKnowledge, DomainClarify)
	assertNames(t, got, []string{"kb1", "clar"})
}

// TestToolsInDomainsSkipsMissingBucket 断言桶缺项时不 panic、不产出 nil 工具。
//
// 场景：某域的注册项比桶里实际构建出的工具多（构建失败返回 nil、或未来支持按需裁剪）。
// 若实现直接写 out = append(out, bucket[i]) 而不检查 i < len(bucket)，
// 就会把 nil 塞进工具集 —— 后续 t.Info(ctx) panic，错误点离根因十万八千里。
func TestToolsInDomainsSkipsMissingBucket(t *testing.T) {
	sorted := []internalToolRegistryEntry{
		fakeEntry("kb1", 1, DomainKnowledge),
		fakeEntry("kb2", 2, DomainKnowledge),
	}
	buckets := map[ToolDomain][]einoTool.BaseTool{
		DomainKnowledge: {fakeTool{"kb1"}}, // 只有 1 个，sorted 却要求 2 个
	}

	got := toolsInDomains(sorted, buckets, DomainKnowledge)
	assertNames(t, got, []string{"kb1"})
	for i, tl := range got {
		if tl == nil {
			t.Fatalf("第 %d 个工具是 nil", i)
		}
	}
}

// TestToolsInDomainsExternalAppendedLast 断言 DomainExternal 恒拼在最后，且只在被点名时出现。
//
// 位置是硬要求：buildTools（深度模式全量）靠它保持与旧实现完全一致的拼接顺序 ——
// 旧实现就是「内置按 order，然后 append 动态工具」。顺序变了 prompt KV-cache 失效，
// 且工具表顺序变化会影响模型的工具选择稳定性。
func TestToolsInDomainsExternalAppendedLast(t *testing.T) {
	sorted := []internalToolRegistryEntry{
		fakeEntry("kb1", 1, DomainKnowledge),
	}
	buckets := map[ToolDomain][]einoTool.BaseTool{
		DomainKnowledge: {fakeTool{"kb1"}},
		DomainExternal:  {fakeTool{"mcp1"}, fakeTool{"mcp2"}},
	}

	got := toolsInDomains(sorted, buckets, DomainKnowledge, DomainExternal)
	assertNames(t, got, []string{"kb1", "mcp1", "mcp2"})

	got2 := toolsInDomains(sorted, buckets, DomainKnowledge)
	assertNames(t, got2, []string{"kb1"})
}

// TestToolsInDomainsIgnoresDomainArgOrder 是「实现是否真按 sorted 遍历」的唯一判据。
//
// 为什么必须专门测这条：`toolsInDomains` 的输出如果按 domains 参数的顺序整桶拼接，
// 在「参数顺序恰好等于 sorted 域分组顺序」时看不出差别 —— 两种实现同解，断言恒真。
// （AllToolDomains 声明成 knowledge→docMgmt→clarify→external，而真实 sorted 的域分组
// 顺序恰好也是这个，所以只按 AllToolDomains 顺序断言是抓不到退化的。）
//
// 这里故意用**乱序**参数：按参数整桶拼接会得到 [clar, bravo, alpha, mcp1]，
// 正确实现（按 sorted 过滤 + 桶内游标）得到 [bravo, clar, alpha, mcp1]。
func TestToolsInDomainsIgnoresDomainArgOrder(t *testing.T) {
	sorted := []internalToolRegistryEntry{
		fakeEntry("bravo", 2, DomainKnowledge),
		fakeEntry("clar", 1, DomainClarify),
		fakeEntry("alpha", 3, DomainKnowledge),
	}
	buckets := map[ToolDomain][]einoTool.BaseTool{
		DomainKnowledge: {fakeTool{"bravo"}, fakeTool{"alpha"}},
		DomainClarify:   {fakeTool{"clar"}},
		DomainExternal:  {fakeTool{"mcp1"}},
	}

	// 参数顺序故意与 sorted 相反
	got := toolsInDomains(sorted, buckets, DomainClarify, DomainExternal, DomainKnowledge)
	assertNames(t, got, []string{"bravo", "clar", "alpha", "mcp1"})
}

// TestToolsInDomainsAllDomainsMatchesLegacyOrder 断言 buildTools（全量）
// 与重构前「内置按 order + 动态工具追加在尾」的顺序**逐项相同**。
//
// 这是 Phase 0 的核心验收：分桶是纯重构，不得改变任何已有行为。
//
// ⚠️ 这条断言对「按参数顺序整桶拼接」这种退化**不敏感**（参数顺序恰好等于域分组顺序），
// 区分能力由 TestToolsInDomainsIgnoresDomainArgOrder 提供。它守的是「行为没漂移」。
func TestToolsInDomainsAllDomainsMatchesLegacyOrder(t *testing.T) {
	// 注册顺序故意打乱，sorted 必须来自 sortedInternalTools()（真实调用方路径）
	e := &Engine{internalTools: []internalToolRegistryEntry{
		fakeEntry("delete_document", 10, DomainDocMgmt),
		fakeEntry("ask_clarify", 20, DomainClarify),
		fakeEntry("knowledge_search", 1, DomainKnowledge),
		fakeEntry("grep_chunks", 2, DomainKnowledge),
	}}
	sorted := e.sortedInternalTools()

	// 分桶按 sorted 顺序构建（= buildToolsByDomain 的行为）
	buckets := make(map[ToolDomain][]einoTool.BaseTool)
	for _, entry := range sorted {
		buckets[entry.Domain] = append(buckets[entry.Domain], fakeTool{entry.Name})
	}
	buckets[DomainExternal] = append(buckets[DomainExternal], fakeTool{"mcp_a"}, fakeTool{"mcp_b"})

	got := toolsInDomains(sorted, buckets, AllToolDomains...)
	assertNames(t, got, []string{
		"knowledge_search", "grep_chunks", // order 1,2
		"delete_document", // order 10
		"ask_clarify",     // order 20
		"mcp_a", "mcp_b",  // 动态工具恒在尾
	})
}

// TestRegisterInternalRejectsUnknownDomain 断言非法域在注册期就 panic。
//
// 静默接受的后果：工具进了某个没人读的桶，从任何 Agent 的工具集里消失，
// 且没有任何日志 —— 表现为「工具明明注册了却调不到」。
func TestRegisterInternalRejectsUnknownDomain(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("注册未知域时应 panic 但没有")
		}
	}()
	e := &Engine{}
	e.RegisterInternal("bad_tool", 1, false, ToolDomain("no_such_domain"), func(context.Context, string, []string) einoTool.BaseTool {
		return fakeTool{"bad_tool"}
	})
}

// TestRegisterInternalRejectsExternalForInternalTool 断言内置工具不许登记到
// DomainExternal —— 那个桶只装 ToolFactory 动态创建的工具（append 语义，非覆盖）。
func TestRegisterInternalRejectsExternalForInternalTool(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("内置工具登记到 DomainExternal 时应 panic 但没有")
		}
	}()
	e := &Engine{}
	e.RegisterInternal("mcp_like", 1, false, DomainExternal, func(context.Context, string, []string) einoTool.BaseTool {
		return fakeTool{"mcp_like"}
	})
}

// TestRegisterInternalAcceptsKnownDomains 断言 3 个「可注册」域都能注册成功
// （两条 panic 守卫的对照组，防止守卫写错成「一律拒绝」）。
//
// 注意 AllToolDomains 含 DomainExternal，但那个域只放运行时动态工具，
// 不在可注册集合内 —— 这里显式枚举可注册域，不遍历 AllToolDomains。
func TestRegisterInternalAcceptsKnownDomains(t *testing.T) {
	registrable := []ToolDomain{DomainKnowledge, DomainDocMgmt, DomainClarify}
	e := &Engine{}
	for i, d := range registrable {
		e.RegisterInternal(string(d)+"_tool", i, false, d, func(context.Context, string, []string) einoTool.BaseTool {
			return fakeTool{}
		})
	}
	if len(e.internalTools) != len(registrable) {
		t.Fatalf("注册数=%d 期望 %d", len(e.internalTools), len(registrable))
	}
	for i, entry := range e.internalTools {
		if entry.Domain != registrable[i] {
			t.Fatalf("第 %d 个 Domain=%s 期望 %s", i, entry.Domain, registrable[i])
		}
	}
}

// TestEffectiveProfileDefaultsToDeep 断言空 Profile 按深度处理（保持既有行为），
// 且判定只认 effectiveProfile —— 直接读 req.Profile 会拿到空串。
func TestEffectiveProfileDefaultsToDeep(t *testing.T) {
	cases := []struct {
		in   Profile
		want Profile
	}{
		{"", ProfileDeep}, // 空 = 深度（既有调用方不传 Profile）
		{ProfileDeep, ProfileDeep},
		{ProfileQuick, ProfileQuick},
		{Profile("bogus"), ProfileDeep}, // 未知值也落深度，不进 quick 分支
	}
	for _, c := range cases {
		if got := (Request{Profile: c.in}).effectiveProfile(); got != c.want {
			t.Fatalf("Profile=%q → %s 期望 %s", c.in, got, c.want)
		}
	}
}

// TestQuickToolSetHasNoDangerousOrExternal 守卫「快速模式碰不到任何需审批 / 联网工具」。
//
// 这是 §5.2 的核心承诺。若有人把 delete_document 改成 DomainKnowledge，
// 或把 ask_clarify 挪出快速集，本断言立刻红 ——
// 否则后果是快速模式静默获得删文档能力。
func TestQuickToolSetHasNoDangerousOrExternal(t *testing.T) {
	// 与 quick_agent.go 的 QuickToolDomains 同源：快速集 = 知识域 + 澄清域
	quick := []ToolDomain{DomainKnowledge, DomainClarify}

	sorted := []internalToolRegistryEntry{
		fakeEntry("knowledge_search", 1, DomainKnowledge),
		fakeEntry("delete_document", 10, DomainDocMgmt),
		fakeEntry("ask_clarify", 20, DomainClarify),
	}
	buckets := map[ToolDomain][]einoTool.BaseTool{
		DomainKnowledge: {fakeTool{"knowledge_search"}},
		DomainDocMgmt:   {fakeTool{"delete_document"}},
		DomainClarify:   {fakeTool{"ask_clarify"}},
		DomainExternal:  {fakeTool{"mcp_a"}},
	}

	got := toolNames(toolsInDomains(sorted, buckets, quick...))
	for _, n := range got {
		if n == "delete_document" || n == "mcp_a" {
			t.Fatalf("快速工具集里出现了 %s（实际 %v）", n, got)
		}
	}
	if len(got) != 2 {
		t.Fatalf("快速工具集=%v 期望 2 个（知识检索 + 澄清）", got)
	}
}
