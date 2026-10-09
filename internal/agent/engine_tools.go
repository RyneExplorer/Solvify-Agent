package agent

import (
	"context"
	"encoding/json"
	"sort"

	einoTool "github.com/cloudwego/eino/components/tool"

	"solvify-agent/pkg/tokenutil"
)

type prebuiltToolsCtxKeyType struct{}

var prebuiltToolsCtxKey = prebuiltToolsCtxKeyType{}

// prebuiltToolsBundle 预构建的工具集。
//
// ⚠️ 必须同时带 Sorted 与 Buckets，不能只带拍平的 Tools：
// 快速模式与深度模式挂的工具集不同（见 QuickToolDomains），只存拍平结果的话
// 快速模式会从 ctx 里捞到全量工具 —— delete_document 和全部 MCP 工具都被挂上，
// 恰好破坏「快速模式碰不到任何需审批/联网工具」这条保证。
type prebuiltToolsBundle struct {
	Sorted      []internalToolRegistryEntry
	Buckets     map[ToolDomain][]einoTool.BaseTool
	TotalTokens int
}

// tools 按 domains 从预构建工具集取用。
func (b prebuiltToolsBundle) tools(domains ...ToolDomain) []einoTool.BaseTool {
	return toolsInDomains(b.Sorted, b.Buckets, domains...)
}

func withPrebuiltTools(ctx context.Context, bundle prebuiltToolsBundle) context.Context {
	return context.WithValue(ctx, prebuiltToolsCtxKey, bundle)
}

func prebuiltToolsFromContext(ctx context.Context) (prebuiltToolsBundle, bool) {
	v := ctx.Value(prebuiltToolsCtxKey)
	if v == nil {
		return prebuiltToolsBundle{}, false
	}
	b, ok := v.(prebuiltToolsBundle)
	return b, ok
}

// sortedInternalTools 按 Order 升序返回内置工具注册表的副本（用于 prompt 展示与构建）
//
// 必须全序：Order 是人工登记的小整数，两个工具登记成同一个值完全可能；
// 同值时按 Name 裁决，否则工具在 prompt 里的先后由排序算法决定。
func (e *Engine) sortedInternalTools() []internalToolRegistryEntry {
	sorted := make([]internalToolRegistryEntry, len(e.internalTools))
	copy(sorted, e.internalTools)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Order != sorted[j].Order {
			return sorted[i].Order < sorted[j].Order
		}
		return sorted[i].Name < sorted[j].Name
	})
	return sorted
}

// buildToolsByDomain 构建 domains 指定的工具并按域分桶。
//
// 只构建一次是硬要求：Build 内部会查 DB / 建检索器，MCP 工具还要握手，
// 构建两遍等于把每请求成本翻倍。分桶后由 toolsInDomains 按域取用。
//
// ⚠️ domains 必须覆盖**本次请求内所有会用到该桶的调用点**，不能只给主 profile 的域：
// 深度编排的主 Agent 走 AllToolDomains，而 web_research 子任务只挂 DomainExternal，
// 两者复用同一个 bundle（见 deep_orchestrator 的 prebuiltToolsFromContext）。
// 漏掉的域在这里不会被构建，下游只是静默少工具 —— Planner 的 external 判据
// 还会因此判定「无联网工具」而不再规划 web_research。
//
// ⚠️ DomainExternal 必须放在 `want` 判断里：CreateAgentTools 会拉起 MCP 子进程并握手，
// 失败时按配置的 timeout 同步阻塞（本地 filesystem MCP 实测白等 30s）。
// 快速模式的域不含 external，不加这个判断就等于每请求白等一次握手。
//
// ⚠️ allowDynamic 为 false 时**一个 MCP 工具都不挂**，且不会触碰 CreateAgentTools
// （用户显式选择「不使用任何 MCP」时走这条路，顺带省掉握手）。
func (e *Engine) buildToolsByDomain(ctx context.Context, sorted []internalToolRegistryEntry, userID string, kbIDs []string, domains []ToolDomain, allowDynamic bool, userToolConfigIDs ...string) map[ToolDomain][]einoTool.BaseTool {
	want := make(map[ToolDomain]bool, len(domains))
	for _, d := range domains {
		want[d] = true
	}
	buckets := make(map[ToolDomain][]einoTool.BaseTool, len(AllToolDomains))
	for _, entry := range sorted {
		if !want[entry.Domain] {
			continue
		}
		buckets[entry.Domain] = append(buckets[entry.Domain], entry.Build(ctx, userID, kbIDs))
	}
	// 动态工具（MCP / HTTP）没有注册表项，统一落 DomainExternal，追加在桶尾。
	if want[DomainExternal] && allowDynamic {
		buckets[DomainExternal] = append(buckets[DomainExternal],
			e.toolFactory.CreateAgentTools(ctx, userID, userToolConfigIDs...)...)
	}
	return buckets
}

// profileToolSet 是一次编排里「实际挂载的工具」与「prompt 里列出的工具」的**同一个产物**。
//
// ⚠️ 两者必须由selectProfileTools 一次算出、一起传下去，不能由调用方各算一份：
// 分开算就多出「过滤条件写错一次」的余地，症状是模型照着 prompt 调用一个
// 没挂的工具、被 UnknownToolsHandler 兜住，而两边看起来都"对"。
type profileToolSet struct {
	// Entries 已按 profile 域过滤、保持全序，供 prompt 列清单
	Entries []internalToolRegistryEntry
	// Tools 是真正挂给 adk 的工具，供 ToolsNodeConfig 与 sources 采集
	Tools []einoTool.BaseTool
}

// selectProfileTools 按 profile 一次算出挂载工具集与 prompt 清单。
// buckets 必须是 buildToolsByDomain 的原样输出（复用预构建结果时直接传bundle.Buckets）。
func selectProfileTools(sorted []internalToolRegistryEntry, buckets map[ToolDomain][]einoTool.BaseTool, profile Profile) profileToolSet {
	return selectToolsByDomains(sorted, buckets, profile.toolDomains())
}

// selectRequestTools 使用任务工具白名单；nil 沿用 profile，空切片禁用所有工具。
func selectRequestTools(sorted []internalToolRegistryEntry, buckets map[ToolDomain][]einoTool.BaseTool, req Request) profileToolSet {
	if req.ToolDomains == nil {
		return selectProfileTools(sorted, buckets, req.effectiveProfile())
	}
	return selectToolsByDomains(sorted, buckets, req.ToolDomains)
}

func selectToolsByDomains(sorted []internalToolRegistryEntry, buckets map[ToolDomain][]einoTool.BaseTool, domains []ToolDomain) profileToolSet {
	return profileToolSet{
		Entries: entriesInDomains(sorted, domains...),
		Tools:   toolsInDomains(sorted, buckets, domains...),
	}
}

// namesOf 返回挂载工具名列表，供日志与测试断言。
func (ts profileToolSet) namesOf() []string {
	out := make([]string, 0, len(ts.Tools))
	for _, t := range ts.Tools {
		if info, err := t.Info(context.Background()); err == nil && info != nil {
			out = append(out, info.Name)
		}
	}
	return out
}

// entriesInDomains 从注册表条目里筛出指定域的子集，保持 sorted 的全序。
//
// prompt 的「可用工具」清单必须走这里：清单里列的工具必须是本模式真挂上的那些，
// 否则模型照着清单调用会撞 UnknownToolsHandler —— prompt 与实际能力不一致。
func entriesInDomains(sorted []internalToolRegistryEntry, domains ...ToolDomain) []internalToolRegistryEntry {
	want := make(map[ToolDomain]bool, len(domains))
	for _, d := range domains {
		want[d] = true
	}
	out := make([]internalToolRegistryEntry, 0, len(sorted))
	for _, entry := range sorted {
		if want[entry.Domain] {
			out = append(out, entry)
		}
	}
	return out
}

// toolsInDomains 从分桶结果里取指定域的工具，跨域拼接后仍按 sorted 的全序排列
// （域内顺序 = sorted 过滤后的子序列；DomainExternal 恒在最后，与旧 buildTools 的拼接顺序一致）。
func toolsInDomains(sorted []internalToolRegistryEntry, buckets map[ToolDomain][]einoTool.BaseTool, domains ...ToolDomain) []einoTool.BaseTool {
	want := make(map[ToolDomain]bool, len(domains))
	for _, d := range domains {
		want[d] = true
	}
	// 每个域在 sorted 里被消费到第几个 —— 桶内下标必须跟着 sorted 走，
	// 否则过滤 sorted 时会拿到别的域的工具。
	cursors := make(map[ToolDomain]int, len(domains))
	var out []einoTool.BaseTool
	for _, entry := range sorted {
		if !want[entry.Domain] {
			continue
		}
		bucket := buckets[entry.Domain]
		i := cursors[entry.Domain]
		if i >= len(bucket) {
			continue
		}
		cursors[entry.Domain] = i + 1
		out = append(out, bucket[i])
	}
	if want[DomainExternal] {
		out = append(out, buckets[DomainExternal]...)
	}
	return out
}

// EstimateToolsTokens 返回「该模式实际会挂上的工具定义的真 BPE token 数」以及预构建好的工具集。
//
// 按 profile 分域统计是必须的：快速模式若按全量统计，会把 20+ 个 MCP 工具的
// description 一并算进预算，白白挤掉历史/检索的上下文额度。
//
// ⚠️ 这里已经是快速模式的短路点：构建范围就是 profile.toolDomains()，不含 external
// ⇒ 快速模式不会触发 MCP 握手。
func (e *Engine) EstimateToolsTokens(ctx context.Context, userID string, kbIDs []string, modelName string, profile Profile, mcpDisabled bool, userToolConfigIDs ...string) (int, context.Context, error) {
	sorted := e.sortedInternalTools()
	domains := profile.toolDomains()
	buckets := e.buildToolsByDomain(ctx, sorted, userID, kbIDs, domains, !mcpDisabled, userToolConfigIDs...)
	tools := toolsInDomains(sorted, buckets, domains...)
	total := 0
	for _, t := range tools {
		info, err := t.Info(ctx)
		if err != nil || info == nil {
			continue
		}
		bs, mErr := json.Marshal(info)
		if mErr != nil {
			total += tokenutil.CountTokens(info.Name+"\n"+info.Desc, modelName)
			continue
		}
		total += tokenutil.CountTokens(string(bs), modelName)
	}
	overhead := int(float64(total) * 0.2)
	if overhead < 200 {
		overhead = 200
	}
	total += overhead
	bundle := prebuiltToolsBundle{Sorted: sorted, Buckets: buckets, TotalTokens: total}
	return total, withPrebuiltTools(ctx, bundle), nil
}
