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

// runDeepAgent 执行普通深度单 Agent，也供旧深度编排恢复专业任务。
func (e *Engine) runDeepAgent(ctx context.Context, req Request, chatModel model.ToolCallingChatModel, eventCh chan<- Event) {
	// 新建深度单Agent不直接获得MCP；旧checkpoint与专业子任务保留原恢复边界。
	if req.AgentName == "" && req.CheckpointID == "" {
		req.MCPDisabled = true
		req.ToolDomains = []ToolDomain{DomainKnowledge, DomainDocMgmt, DomainClarify}
	}
	profile := req.effectiveProfile()
	sorted := e.sortedInternalTools()

	// ── 工具集：按本模式的域取，优先复用上游预构建结果 ──
	// 不复用的话一次请求会构建两遍工具，冷启动时重复拉起 MCP Server 子进程。
	buckets, reused := e.bucketsFor(ctx, sorted, req)
	ts := selectRequestTools(sorted, buckets, req)
	logToolSetReuse(ctx, profile, ts, reused)
	allTools := e.addArtifactTool(req, ts.Tools)
	toolDescMap := logToolSet(ctx, e, req, allTools)

	// 中间件注入与迭代预算读同一个结果：审批与澄清都会中断执行等用户回应，
	// 恢复时同样占一轮模型调用，必须计入预算下界（见 agentIterations）。
	dangerousNames := e.dangerousToolNames(ctx, allTools)
	clarifyNames := e.clarifyToolNames()
	interactive := len(dangerousNames) > 0 || len(clarifyNames) > 0
	iterations := agentIterations(e.deepSubagentIterations(req), interactive)

	// ── 系统提示词 ──
	baseSystemPrompt := buildSystemPrompt(ctx, profile, ts, maxToolCalls(iterations))
	if req.TaskID != "" {
		baseSystemPrompt = buildDeepTaskPrompt(ctx, req.AgentName, ts, iterations)
	}
	systemPromptFinal := composeSystemPrompt(baseSystemPrompt, req.SystemPrompt)
	logger.Infof("[Agent] SystemPrompt (前400字符): %s", strutil.Truncate(systemPromptFinal, 400))

	inputMessages := buildInputMessages(req.Query, req.History, req.ContextData...)

	// ── 创建 adk.ChatModelAgent ──
	toolsNodeConfig := buildToolsNodeConfig()
	toolsNodeConfig.Tools = allTools
	toolsNodeConfig.ToolCallMiddlewares = append(toolsNodeConfig.ToolCallMiddlewares, compose.ToolMiddleware{Invokable: e.artifactMiddleware(req)})
	// ── 注入中间件：危险工具审批 + 澄清追问 ──
	if len(dangerousNames) > 0 {
		toolsNodeConfig.ToolCallMiddlewares = append(toolsNodeConfig.ToolCallMiddlewares,
			compose.ToolMiddleware{Invokable: buildDangerousToolMiddleware(dangerousNames)})
		logger.Infof("[Agent] 已注入危险工具审批中间件: %v", dangerousNames)
	}
	if len(clarifyNames) > 0 {
		toolsNodeConfig.ToolCallMiddlewares = append(toolsNodeConfig.ToolCallMiddlewares,
			compose.ToolMiddleware{Invokable: buildClarifyMiddleware(clarifyNames)})
		logger.Infof("[Agent] 已注入澄清追问中间件: %v", clarifyNames)
	}

	agentName := deepAgentName
	if req.AgentName != "" {
		agentName = req.AgentName
	}
	guard := newBudgetGuard(req)
	guard.finalCheckElsewhere = true
	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        agentName,
		Description: "深度模式 Agent，能够调用知识库和外部工具进行多步推理",
		Instruction: systemPromptFinal,
		Model:       chatModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: e.withToolExecutionTimeout(toolsNodeConfig),
		},
		MaxIterations:    iterations,
		ModelRetryConfig: e.modelRetryConfig(),
		Handlers:         []adk.ChatModelAgentMiddleware{e.runtimeHandler(req, systemPromptFinal, chatModel), guard, &completionGuard{}},
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

	// ── 创建 Runner ──
	store := e.contextStore(req)
	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           agent,
		EnableStreaming: true,
		CheckPointStore: store,
	})

	// ── 执行：首次 Run 或带 ResumeData 的 Resume ──
	e.runWithRunner(ctx, runner, e.resolveCheckpointID(req), inputMessages, req,
		knowledgeSearchToolOf(allTools), toolDescMap, eventCh)
}
