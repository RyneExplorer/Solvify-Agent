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
)

// budgetGuard 在**每次模型调用前**做一次总预算检查，超水位就压缩工具结果。
//
// 为什么必须挂在这里而不是装配器：首次执行、ReAct 的每一轮迭代、中断恢复后的继续，
// 三者在 eino 内部是同一个循环（adk/react.go 的 ChatModel 节点），只有这个点能同时
// 覆盖它们；装配器只在首次执行的入口跑一次，恢复那一轮根本不过它。
//
// BeforeModelRewriteState返回的投影会写回图状态并随checkpoint保存；原文引用仍保留。
// 恢复后由runtimeContextMiddleware按本轮预算重新展开，投影不会永久丢掉原文。
//
// 快速模式通过 ChatModelAgent middleware 持久化压缩结果；深度模式通过
// budgetModel 在所有模型调用前压缩副本，兼容不暴露 Handlers 的原生阶段。
type budgetGuard struct {
	adk.BaseChatModelAgentMiddleware

	modelID     string
	declaredCtx int // 用户确认的模型总窗口
	modelName   string
	profile     Profile

	inputLimit          int
	outputLimit         int
	policy              config.ContextPolicy
	finalCheckElsewhere bool
}

// newBudgetGuard 构造当前模式的总预算检查。
// 窗口及输出在调用前解析，不能确定时明确拒绝。
func newBudgetGuard(req Request) *budgetGuard {
	return &budgetGuard{
		modelID:     req.ModelID,
		inputLimit:  req.InputLimit,
		outputLimit: req.OutputLimit,
		policy:      config.ContextDefaults(),
		declaredCtx: req.MaxContextLength,
		modelName:   req.ModelName,
		profile:     req.effectiveProfile(),
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
	fixed := llm.ToolsTokens(state.ToolInfos, g.modelName)
	before := llm.MessagesTokens(state.Messages, g.modelName) + fixed
	msgs, truncated := fitStoredToolResults(state.Messages, g.modelName, budget, fixed)
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
	for _, kind := range []string{"recall", "history_fragment", "memory"} {
		for index := len(msgs) - 1; after > budget.Hard && index >= 0; index-- {
			message := msgs[index]
			if message == nil || message.Extra["solvify_context_material"] != kind || protectedToolResult(message.Content) {
				continue
			}
			next := append([]*schema.Message(nil), msgs[:index]...)
			msgs = append(next, msgs[index+1:]...)
			after = llm.MessagesTokens(msgs, g.modelName) + fixed
			logger.Infof("上下文降级：减少可选数据 类型=%s 重新计数=%d", kind, after)
		}
	}
	logger.Infof("上下文预算：模式=%s 窗口=%d 输出=%d 裕量=%d 硬上限=%d 软水位=%d 压缩前=%d 压缩后=%d 工具投影=%d", g.profile, budget.Limit, budget.Output, budget.Margin, budget.Hard, budget.Soft, before, after, truncated)
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
	for start := 0; start+1 < lastUser; start++ {
		user, answer := msgs[start], msgs[start+1]
		if user == nil || answer == nil || user.Role != schema.User || answer.Role != schema.Assistant ||
			user.Extra["solvify_context_material"] != nil ||
			strings.HasPrefix(user.Content, runtimeSummaryPrefix) || protectedToolResult(user.Content) ||
			len(answer.ToolCalls) > 0 || protectedToolResult(answer.Content) {
			continue
		}
		end := start + 2
		if end <= lastUser && msgs[end] != nil && msgs[end].Role == schema.User {
			next := append([]*schema.Message(nil), msgs[:start]...)
			next = append(next, msgs[end:]...)
			return next, true
		}
	}
	return msgs, false
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
