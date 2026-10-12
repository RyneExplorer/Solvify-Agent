package agent

import (
	"context"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/compose"

	apperrors "solvify-agent/pkg/errors"
	"solvify-agent/pkg/eventch"
	"solvify-agent/pkg/logger"
	"solvify-agent/pkg/strutil"
)

// runQuickAgent 快速模式：单个 ChatModelAgent + 知识检索域工具。
//
// 与 runAgent（深度模式）刻意保持两套独立函数，而不是塞进一个带if 的巨型函数：
// 两者的工具集、迭代上限、Agent 身份都不同，合并后每加一个分支都要重读全函数。
// 共享的部分只有 runWithRunner（事件消费与中断冒泡）。
func (e *Engine) runQuickAgent(ctx context.Context, req Request, chatModel model.ToolCallingChatModel, eventCh chan<- Event) {
	profile := ProfileQuick
	sorted := e.sortedInternalTools()

	buckets, reused := e.bucketsFor(ctx, sorted, req)
	ts := selectProfileTools(sorted, buckets, profile)
	logToolSetReuse(ctx, profile, ts, reused)
	allTools := ts.Tools
	toolDescMap := logToolSet(ctx, e, req, allTools)

	// 澄清中间件与迭代预算读同一个结果：澄清是一次普通工具调用，会占掉一轮模型调用，
	// 恢复后还要继续扣 —— 预算必须容得下「检索 + 澄清 + 再检索 + 最终回答」，见 agentIterations。
	clarifyNames := e.clarifyToolNames()
	iterations := agentIterations(e.quickMaxIterations(), len(clarifyNames) > 0)

	baseSystemPrompt := buildSystemPrompt(ctx, profile, ts, maxToolCalls(iterations))
	systemPromptFinal := composeSystemPrompt(baseSystemPrompt, req.SystemPrompt)
	logger.Infof("[Agent] SystemPrompt (前400字符): %s", strutil.Truncate(systemPromptFinal, 400))

	inputMessages := buildInputMessages(req.Query, req.History)
	toolsNodeConfig := buildToolsNodeConfig()
	toolsNodeConfig.Tools = allTools
	if len(clarifyNames) > 0 {
		toolsNodeConfig.ToolCallMiddlewares = append(toolsNodeConfig.ToolCallMiddlewares,
			compose.ToolMiddleware{Invokable: buildClarifyMiddleware(clarifyNames)})
		logger.Infof("[Agent] 已注入澄清追问中间件: %v", clarifyNames)
	}

	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:          quickAgentName,
		Description:   "快速模式 Agent，基于知识库回答问题，必要时向用户澄清",
		Instruction:   systemPromptFinal,
		Model:         chatModel,
		ToolsConfig:   adk.ToolsConfig{ToolsNodeConfig: toolsNodeConfig},
		MaxIterations: iterations,
		// 每次模型调用前的总预算检查（首次执行 / 每轮迭代 / 中断恢复三条路径共用）。
		Handlers: []adk.ChatModelAgentMiddleware{newBudgetGuard(req, systemPromptFinal)},
	})
	if err != nil {
		logger.Errorf("[Agent] ChatModelAgent 初始化失败: %v", err)
		eventch.Send(ctx, eventCh, Event{
			Type:   EventError,
			Kind:   apperrors.KindAgentInitFailed,
			Error:  err.Error(),
			Status: "error",
			Done:   true,
		})
		return
	}

	store := e.buildCheckpointStore(req.SessionID)
	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           agent,
		EnableStreaming: true,
		CheckPointStore: store,
	})
	e.runWithRunner(ctx, runner, e.resolveCheckpointID(req), inputMessages, req,
		knowledgeSearchToolOf(allTools), toolDescMap, eventCh)
}
