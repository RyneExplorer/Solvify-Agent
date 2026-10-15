package agent

import (
	"context"
	"strings"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/internal/llm"
	"solvify-agent/pkg/config"
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
	declaredCtx int // 用户确认的模型总窗口
	modelName   string
	profile     Profile

	// instruction 是本轮 System Prompt。state 里没有它
	// （adk/chatmodel.go:216-230 的 state 只有 Messages/ToolInfos），
	// 不传进来预算就会漏掉最大的一块固定开销。
	// 模型包装器接收最终消息，instruction 为空；不靠 role 猜测去重。
	instruction         string
	inputLimit          int
	outputLimit         int
	policy              config.ContextPolicy
	finalCheckElsewhere bool
}

const (
	// 单条工具结果消息的 token 上限。知识库检索结果是最可重新检索的一类，
	// 压掉它不心疼。（决策 ⑦「按工具类型分档」留到后续阶段，这里先给全局值。）
	toolResultCap = 1500
	// 截断标记。与 service 层的 truncationMarker 是两回事，各管一段，不要合并。
	toolResultTruncated = "\n\n（工具结果过长，已截断）"
)

// newBudgetGuard 构造当前模式的总预算检查。
// 窗口及输出在调用前解析，不能确定时明确拒绝。
func newBudgetGuard(req Request, instruction string) *budgetGuard {
	return &budgetGuard{
		modelID:     req.ModelID,
		inputLimit:  req.InputLimit,
		outputLimit: req.OutputLimit,
		policy:      config.ContextDefaults(),
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

	policy := g.policy
	if policy.OutputLimit == 0 {
		policy = config.ContextDefaults()
	}
	output := g.outputLimit
	if output == 0 {
		output = policy.OutputLimit
	}
	budget, err := llm.ResolveContextBudget(g.modelID, g.modelName, g.declaredCtx, output, g.profile == ProfileQuick, policy)
	if err != nil {
		if g.finalCheckElsewhere {
			// 调用级供应商选项可能覆盖输出上限，交由最终请求核对。
			return ctx, state, nil
		}
		return ctx, state, err
	}
	budget = budget.WithInputLimit(g.inputLimit)
	// Handler 明确收到不含 Instruction 的状态；模型包装器的 instruction 为空，接收最终消息。
	fixed := llm.ToolsTokens(state.ToolInfos, g.modelName)
	if g.instruction != "" {
		fixed += llm.MessageTokens(schema.SystemMessage(g.instruction), g.modelName)
	}
	before := llm.MessagesTokens(state.Messages, g.modelName) + fixed
	msgs, truncated := truncateToolResults(state.Messages, g.modelName)
	after := llm.MessagesTokens(msgs, g.modelName) + fixed
	removed := 0
	for after > budget.Soft {
		next, ok := dropOldConversation(msgs)
		if !ok {
			break
		}
		msgs = next
		removed++
		after = llm.MessagesTokens(msgs, g.modelName) + fixed
		if after <= budget.Target {
			break
		}
	}
	if removed > 0 {
		logger.Infof("上下文降级：减少旧对话组=%d", removed)
	}
	logger.Infof("上下文预算：模式=%s 窗口=%d 输出=%d 裕量=%d 硬上限=%d 软水位=%d 压缩前=%d 压缩后=%d 工具截断=%d", g.profile, budget.Limit, budget.Output, budget.Margin, budget.Hard, budget.Soft, before, after, truncated)
	if err = budget.Check(after); err != nil && !g.finalCheckElsewhere {
		return ctx, state, err
	}
	next := *state
	next.Messages = msgs
	return ctx, &next, nil
}

// dropOldConversation 只减少最新用户问题前的完整普通对话，不移除工具轨迹。
func dropOldConversation(msgs []*schema.Message) ([]*schema.Message, bool) {
	lastUser := -1
	for i, m := range msgs {
		if m != nil && m.Role == schema.User {
			lastUser = i
		}
	}
	start := -1
	for i := 0; i < lastUser; i++ {
		m := msgs[i]
		if m == nil {
			continue
		}
		if start < 0 {
			if m.Role == schema.User && !strings.HasPrefix(m.Content, runtimeSummaryPrefix) {
				start = i
			}
			continue
		}
		if m.Role == schema.System || m.Role == schema.Tool || len(m.ToolCalls) > 0 {
			return msgs, false
		}
		if m.Role == schema.User {
			start = i
			continue
		}
		if m.Role == schema.Assistant && (i+1 == lastUser || (i+1 < len(msgs) && msgs[i+1] != nil && msgs[i+1].Role == schema.User)) {
			next := append([]*schema.Message(nil), msgs[:start]...)
			next = append(next, msgs[i+1:]...)
			return next, true
		}
	}
	return msgs, false
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

// budgetModel 管理调用副本；生产客户端在序列化边界核对实际供应商选项。
type budgetModel struct {
	inner model.ToolCallingChatModel
	guard *budgetGuard
	tools []*schema.ToolInfo
}

func (m *budgetModel) OutputLimit() int { return m.guard.outputLimit }

func (m *budgetModel) WithTools(tools []*schema.ToolInfo) (model.ToolCallingChatModel, error) {
	inner, err := m.inner.WithTools(tools)
	if err != nil {
		return nil, err
	}
	return &budgetModel{inner: inner, guard: m.guard, tools: append([]*schema.ToolInfo(nil), tools...)}, nil
}

func (m *budgetModel) prepare(ctx context.Context, messages []*schema.Message, opts []model.Option) (context.Context, []*schema.Message, error) {
	options := model.GetCommonOptions(nil, opts...)
	tools := m.tools
	if options.Tools != nil {
		tools = options.Tools
	}
	guard := *m.guard
	if options.MaxTokens != nil {
		guard.outputLimit = *options.MaxTokens
	}
	if options.Model != nil && *options.Model != guard.modelName {
		guard.modelName = *options.Model
		guard.declaredCtx = 0
	}
	if final, ok := m.inner.(interface{ FinalBudgetChecked() bool }); ok {
		guard.finalCheckElsewhere = final.FinalBudgetChecked()
	}
	ctx, state, err := guard.BeforeModelRewriteState(ctx, &adk.ChatModelAgentState{Messages: messages, ToolInfos: tools}, nil)
	return ctx, state.Messages, err
}

func (m *budgetModel) Generate(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.Message, error) {
	ctx, input, err := m.prepare(ctx, messages, opts)
	if err != nil {
		return nil, err
	}
	return m.inner.Generate(ctx, input, opts...)
}

func (m *budgetModel) Stream(ctx context.Context, messages []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	ctx, input, err := m.prepare(ctx, messages, opts)
	if err != nil {
		return nil, err
	}
	return m.inner.Stream(ctx, input, opts...)
}
