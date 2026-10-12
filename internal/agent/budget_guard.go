package agent

import (
	"context"
	"encoding/json"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/internal/llm"
	"solvify-agent/pkg/logger"
	"solvify-agent/pkg/tokenutil"
)

// budgetGuard 在**每次模型调用前**做一次总预算检查，超水位就压缩工具结果。
//
// 为什么必须挂在这里而不是装配器：首次执行、ReAct 的每一轮迭代、中断恢复后的继续，
// 三者在 eino 内部是同一个循环（adk/react.go 的 ChatModel 节点），只有这个点能同时
// 覆盖它们；装配器只在首次执行的入口跑一次，恢复那一轮根本不过它。
//
// 为什么压缩是持久的：BeforeModelRewriteState 返回的 state 会被写回图状态
// （adk/wrappers.go:1238-1244「Persist state ... after BeforeModelRewriteState」），
// 所以结果会随 checkpoint 落库，恢复时不会全长回来。
//
// 快速模式通过 ChatModelAgent middleware 持久化压缩结果；深度模式通过
// budgetModel 在所有模型调用前压缩副本，兼容不暴露 Handlers 的原生阶段。
type budgetGuard struct {
	adk.BaseChatModelAgentMiddleware

	modelID     string
	declaredCtx int // 模型声明窗口；有效值再过 GetEffectiveMaxContextLength 收敛
	modelName   string
	profile     Profile

	// instruction 是本轮 System Prompt。state 里没有它
	// （adk/chatmodel.go:216-230 的 state 只有 Messages/ToolInfos），
	// 不传进来预算就会漏掉最大的一块固定开销。
	// 若某版本 eino 把它也放进了 state.Messages，则由首条消息的 role 兜底去重。
	instruction string
}

const (
	// 估算的安全裕量。tokenutil.CountTokens 拿不到 BPE 时会退化成字符加权估算
	// （pkg/tokenutil/tokenutil.go:69-78），未知模型一律按 cl100k_base 算（:12），
	// 都不是供应商分词器；角色标记与 tool_calls/tool_call_id 的序列化开销也进不了
	// 文本计数。这些全由裕量兜住，方向是「宁可多压」—— 估大不致命，估小才会超窗。
	marginRatio = 0.08
	marginFloor = 256

	// 输出预留：留给模型生成的空间。
	reserveRatio = 0.15
	reserveFloor = 1024

	// 单条工具结果消息的 token 上限。知识库检索结果是最可重新检索的一类，
	// 压掉它不心疼。（决策 ⑦「按工具类型分档」留到后续阶段，这里先给全局值。）
	toolResultCap = 1500
	// 截断标记。与 service 层的 truncationMarker 是两回事，各管一段，不要合并。
	toolResultTruncated = "\n\n（工具结果过长，已截断）"
)

// watermarkRatio 压缩触发水位：装配后总占用占有效窗口的比例上限。
// 快速模式工具结果少而小（top_k=3，单 chunk 截 150 字符），主要风险来自历史，
// 留更大余量比多塞历史划算。
func watermarkRatio(p Profile) float64 {
	if p == ProfileQuick {
		return 0.60
	}
	return 0.75
}

// newBudgetGuard 构造当前模式的总预算检查。
// 「窗口未知」不在这里判 —— 判定推迟到钩子里，那里会打 Warn 并放行，
// 这样「没接上」是响亮的而不是静默跳过。
func newBudgetGuard(req Request, instruction string) *budgetGuard {
	return &budgetGuard{
		modelID:     req.ModelID,
		declaredCtx: req.MaxContextLength,
		modelName:   req.ModelName,
		profile:     req.effectiveProfile(),
		instruction: instruction,
	}
}

// BeforeModelRewriteState 实现 adk.ChatModelAgentMiddleware。
func (g *budgetGuard) BeforeModelRewriteState(
	ctx context.Context, state *adk.ChatModelAgentState, _ *adk.ModelContext,
) (context.Context, *adk.ChatModelAgentState, error) {
	if g == nil || state == nil || len(state.Messages) == 0 {
		return ctx, state, nil
	}

	limit := llm.GetEffectiveMaxContextLength(g.modelID, g.declaredCtx)
	if limit <= 0 {
		// 拿不到窗口就不猜：宁可放行，也不按拍脑袋的数字砍上下文。
		logger.Warnf("[BudgetGuard] 模型 %s 无有效上下文窗口，跳过预算检查", g.modelID)
		return ctx, state, nil
	}

	toolTokens := toolInfosTokens(state.ToolInfos, g.modelName)
	instrTokens := 0
	if !startswithSystem(state.Messages) {
		instrTokens = tokenutil.CountTokens(g.instruction, g.modelName)
	}

	fixed := instrTokens + toolTokens
	used := messagesTokens(state.Messages, g.modelName)
	margin := int(float64(used+fixed) * marginRatio)
	if margin < marginFloor {
		margin = marginFloor
	}
	reserve := int(float64(limit) * reserveRatio)
	if reserve < reserveFloor {
		reserve = reserveFloor
	}

	target := limit - fixed - reserve - margin
	if w := int(float64(limit) * watermarkRatio(g.profile)); target > w {
		target = w
	}

	// 单条工具结果的截断是**无条件**的：它是把「运行中追加」钉住的那条不变量，
	// 与本次聚合额度够不够无关。曾把它挂在 target > 0 之下，于是窗口极小
	// （固定开销吃满）时反而一条都不截 —— 天花板形同虚设。
	msgs, truncated := truncateToolResults(state.Messages, g.modelName)
	after := messagesTokens(msgs, g.modelName)

	logger.Infof("[BudgetGuard] %s 预算: limit=%d instr=%d tools=%d reserve=%d margin=%d target=%d used=%d after=%d 截断=%d msgs=%d",
		g.profile, limit, instrTokens, toolTokens, reserve, margin, target, used, after, truncated, len(msgs))

	switch {
	case target <= 0:
		logger.Errorf("[BudgetGuard] 固定开销已吃满窗口，只剩「截断工具结果」一条手段: limit=%d fixed=%d reserve=%d margin=%d",
			limit, fixed, reserve, margin)
	case after > target:
		// 「能安全丢的都丢了还压不下水位」通常意味着必留内容（用户提问 / 助手回答）
		// 本身就超预算。这里不硬砍必留内容（它们的原文没有替代来源），只响亮记录，
		// 把取舍交给上游装配器 —— 悄悄砍掉才是这个项目栽过跟头的形态。
		logger.Errorf("[BudgetGuard] 压缩后仍超水位（必留内容本身过大）: %s limit=%d target=%d after=%d truncated=%d",
			g.profile, limit, target, after, truncated)
	}

	if truncated > 0 {
		state.Messages = msgs
	}
	return ctx, state, nil
}

// startswithSystem 判断 System Prompt 是否已经躺在 state.Messages 里。
// 放进去过的版本不能再加一次指令 token，否则预算被系统性低估。
func startswithSystem(msgs []*schema.Message) bool {
	for _, m := range msgs {
		if m == nil {
			continue
		}
		return m.Role == schema.System
	}
	return false
}

// messagesTokens 计整段消息的 token。
func messagesTokens(msgs []*schema.Message, modelName string) int {
	total := 0
	for _, m := range msgs {
		total += messageTokens(m, modelName)
	}
	return total
}

// messageTokens 计一条消息：正文 + 工具调用名/参数/id + 工具结果配对字段。
// 后几项不参与纯文本计数，漏掉会系统性低估 ReAct 轨迹的开销。
// constantPerMessage 是角色标记等每条消息的固定开销（OpenAI 对话格式的经验值）。
func messageTokens(m *schema.Message, modelName string) int {
	if m == nil {
		return 0
	}
	const constantPerMessage = 4
	total := constantPerMessage + tokenutil.CountTokens(m.Content, modelName)
	for i := range m.ToolCalls {
		total += tokenutil.CountTokens(m.ToolCalls[i].ID, modelName)
		total += tokenutil.CountTokens(m.ToolCalls[i].Function.Name, modelName)
		total += tokenutil.CountTokens(m.ToolCalls[i].Function.Arguments, modelName)
	}
	total += tokenutil.CountTokens(m.ToolCallID, modelName)
	total += tokenutil.CountTokens(m.ToolName, modelName)
	return total
}

// toolInfosTokens 计工具定义（JSON Schema）的 token。
// 口径与 engine_tools.go:209-215 的 EstimateToolsTokens 保持一致：
// 直接 marshal 整个 ToolInfo。两处必须同源，否则同一批工具会算出两个数。
func toolInfosTokens(infos []*schema.ToolInfo, modelName string) int {
	total := 0
	for _, info := range infos {
		if info == nil {
			continue
		}
		bs, err := json.Marshal(info)
		if err != nil {
			total += tokenutil.CountTokens(info.Name+"\n"+info.Desc, modelName)
			continue
		}
		total += tokenutil.CountTokens(string(bs), modelName)
	}
	return total
}

// truncateToolResults 只截断工具结果消息的正文。
//
// 刻意**不**做「内容相同的工具结果去重」：工具结果要和发起它的
// assistant.tool_calls 按 ToolCallID 一一配对，丢掉一条工具结果就会留下一个
// 无人应答的 tool_call，模型直接报错。截断正文则不影响配对。
func truncateToolResults(msgs []*schema.Message, modelName string) ([]*schema.Message, int) {
	// 上限算的是**整条消息内容**，所以要先给标记留位置 ——
	// 否则实际落盘的是 cap + 标记长度，上限被悄悄撑大。
	body := toolResultCap - tokenutil.CountTokens(toolResultTruncated, modelName)
	if body < toolResultCap/2 {
		body = toolResultCap / 2
	}

	out := make([]*schema.Message, 0, len(msgs))
	truncated := 0
	for _, m := range msgs {
		if m == nil {
			continue
		}
		if m.Role == schema.Tool {
			if t := tokenutil.CountTokens(m.Content, modelName); t > toolResultCap {
				if cut, actual := tokenutil.TruncateByTokens(m.Content, modelName, body); actual > 0 {
					cp := *m
					cp.Content = cut + toolResultTruncated
					out = append(out, &cp)
					truncated++
					continue
				}
			}
		}
		out = append(out, m)
	}
	return out, truncated
}
