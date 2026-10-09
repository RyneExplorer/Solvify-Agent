package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/tool"
	apperrors "solvify-agent/pkg/errors"
	"solvify-agent/pkg/eventch"
	"solvify-agent/pkg/logger"
	"solvify-agent/pkg/strutil"
)

// Execute 启动 Agent 执行流程，通过事件通道异步返回推理结果
func (e *Engine) Execute(ctx context.Context, req Request, chatModel model.ToolCallingChatModel) (<-chan Event, error) {
	eventCh := make(chan Event, 100)

	go func() {
		defer close(eventCh)
		if req.effectiveProfile() == ProfileQuick {
			e.runQuickAgent(ctx, req, chatModel, eventCh)
			return
		}
		if strings.HasPrefix(req.CheckpointID, "deeppe-v1-") || (req.CheckpointID == "" && e.cfg.DeepPlanExecuteEnabled) {
			e.runPlanExecute(ctx, req, chatModel, eventCh)
		} else if strings.HasPrefix(req.CheckpointID, "deep-") {
			e.runDeepOrchestrator(ctx, req, chatModel, eventCh)
		} else {
			e.runAgent(ctx, req, chatModel, eventCh)
		}
	}()

	return eventCh, nil
}

// isInternalToolName 判断工具名是否为内置工具
// registry 里注册过的就是内置，否则是用户配置的
func (e *Engine) isInternalToolName(name string) bool {
	for _, entry := range e.internalTools {
		if entry.Name == name {
			return true
		}
	}
	return false
}

func (e *Engine) runAgent(ctx context.Context, req Request, chatModel model.ToolCallingChatModel, eventCh chan<- Event) {
	profile := req.effectiveProfile()
	sorted := e.sortedInternalTools()

	// ── 工具集：按本模式的域取，优先复用上游预构建结果 ──
	// 不复用的话一次请求会构建两遍工具，冷启动时重复拉起 MCP Server 子进程。
	buckets, reused := e.bucketsFor(ctx, sorted, req)
	ts := selectRequestTools(sorted, buckets, req)
	logToolSetReuse(ctx, profile, ts, reused)
	allTools := ts.Tools
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

	inputMessages := buildInputMessages(req.Query, req.History)

	// ── 创建 adk.ChatModelAgent ──
	toolsNodeConfig := buildToolsNodeConfig()
	toolsNodeConfig.Tools = allTools
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
	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        agentName,
		Description: "深度模式 Agent，能够调用知识库和外部工具进行多步推理",
		Instruction: systemPromptFinal,
		Model:       chatModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: toolsNodeConfig,
		},
		MaxIterations: iterations,
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
	store := e.buildCheckpointStore(req.SessionID)
	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           agent,
		EnableStreaming: true,
		CheckPointStore: store,
	})

	// ── 执行：首次 Run 或带 ResumeData 的 Resume ──
	e.runWithRunner(ctx, runner, e.resolveCheckpointID(req), inputMessages, req,
		knowledgeSearchToolOf(allTools), toolDescMap, eventCh)
}

// ── 两个模式共用的零件 ──
// 每个函数都是「两个模式都必须做同一件事」才抽出来的；
// 只有一个模式需要的东西一律留在各自的 run 函数里。

const (
	// deepAgentName / quickAgentName 是 Agent 在 eino 里的身份名，会出现在
	// Langfuse 的 trace 上 —— 两个模式同名的话，平台上分不出是哪条链路。
	deepAgentName  = "SolvifyDeepAgent"
	quickAgentName = "SolvifyQuickAgent"
)

// bucketsFor 优先复用上游 EstimateToolsTokens 预构建的分桶结果。
//
// 不复用的话一次请求会构建两遍工具：Build 内部查DB、建检索器，MCP还要握手子进程。
func (e *Engine) bucketsFor(ctx context.Context, sorted []internalToolRegistryEntry, req Request) (map[ToolDomain][]einoTool.BaseTool, bool) {
	if bundle, ok := prebuiltToolsFromContext(ctx); ok && len(bundle.Buckets) > 0 {
		return bundle.Buckets, true
	}
	return e.buildToolsByDomain(ctx, sorted, req.UserID, req.KnowledgeBaseIDs, e.toolDomainsFor(req), !req.MCPDisabled, req.UserToolConfigIDs...), false
}

// toolDomainsFor 取本次执行要挂载的工具域。
// 优先 req.ToolDomains：深度子任务的白名单（如 web_research 只有 external）与主 profile 不同，
// 只按 profile 算会漏掉子任务需要的域 ⇒ 静默少工具。
func (e *Engine) toolDomainsFor(req Request) []ToolDomain {
	if req.ToolDomains != nil {
		return req.ToolDomains
	}
	return req.effectiveProfile().toolDomains()
}

func logToolSetReuse(ctx context.Context, profile Profile, ts profileToolSet, reused bool) {
	if reused {
		logger.Debugf("[Agent] 复用预构建工具集: profile=%s, tools=%d", profile, len(ts.Tools))
		return
	}
	logger.Debugf("[Agent] 现场构建工具集: profile=%s, tools=%d", profile, len(ts.Tools))
}

func deepMaxIterations(cfgVal int) int {
	if cfgVal <= 0 {
		return 5
	}
	return cfgVal
}

func (e *Engine) deepSubagentIterations(req Request) int {
	if req.TaskID != "" && e.cfg.DeepMaxSubagentIterations > 0 {
		return e.cfg.DeepMaxSubagentIterations
	}
	return deepMaxIterations(e.cfg.MaxIterations)
}

// quickMaxIterations 读快速模式迭代上限。构造函数已保证 > 0，这里只兜住
// 直接构造 Engine（测试/工具）的场景。
func (e *Engine) quickMaxIterations() int {
	if e.cfg.QuickAgentMaxIterations <= 0 {
		return 2
	}
	return e.cfg.QuickAgentMaxIterations
}

// maxToolCalls 把 MaxIterations 换算成 prompt 里的「工具调用次数上限」。
// eino 一轮 = 一次模型调用 + 可能的工具调用，所以工具轮比模型轮少一轮。
// 只在 prompt 文案里用，不参与编排。
func maxToolCalls(maxIterations int) int {
	if maxIterations < 2 {
		return 1
	}
	return maxIterations - 1
}

// ── 迭代预算 ──
//
// eino 的 MaxIterations =「一次执行里允许的模型调用次数」。ReAct 每一轮里模型可以调
// 一次工具，调完必须再进一次模型才能出答案，因此：
//
//	MaxIterations = 最终回答轮(1) + 工具轮预算
//
// 交互型工具（ask_clarify 澄清、危险工具审批）与业务工具一样各占一轮模型调用，
// 但它们「暂停等人」—— 而 prompt 明确鼓励「不确定的先用检索工具找线索，真不行再澄清」，
// 于是预算必须容得下「澄清 1 + 业务工具 1 + 最终回答 1」。
//
// 下界必须由「本模式实际挂了什么」推导，不能只读配置：只看配置会让
// 「挂了 ask_clarify 却给装不下的预算」结构上可能发生。实测就撞在这条上 ——
// 快速模式 2 轮下澄清吃掉 1 轮，恢复后模型又用 1 轮检索，回 ChatModel 即
// exceeds max iterations（计数器还存在 checkpoint 里，恢复会继续扣减）。
const (
	finalAnswerRounds     = 1 // 最后一轮必须是最终回答，不能再调工具
	businessToolRounds    = 1 // 业务工具（检索等）至少能用一次
	interactiveToolRounds = 1 // 澄清 / 审批这类交互型工具至少能用一次
)

// agentIterations 推导本模式的迭代预算：取「配置值」与「能力下界」的较大者。
//
// interactive 必须与本次真正注入的中间件读同一个结果（澄清 / 危险工具审批），
// 否则「注入了中断能力却没给足轮次」会再次发生。
// 配置值低于下界时抬升并记 Warn —— 静默抬升会让「配置写 2、实际跑 3」难以解释。
func agentIterations(configured int, interactive bool) int {
	need := finalAnswerRounds + businessToolRounds
	if interactive {
		need += interactiveToolRounds
	}
	if configured >= need {
		return configured
	}
	logger.Warnf("[Agent] 迭代预算不足：配置 %d 轮 < 已挂载能力所需下界 %d 轮（含交互型工具=%v），按 %d 轮执行",
		configured, need, interactive, need)
	return need
}

func (e *Engine) resolveCheckpointID(req Request) string {
	if req.CheckpointID != "" {
		logger.Infof("[Agent] 恢复执行：复用 checkpointID=%s", req.CheckpointID)
		return req.CheckpointID
	}
	return fmt.Sprintf("agent-%s-%s-%s-%d", req.SessionID, req.UserID, randomStr8(), time.Now().UnixNano())
}

func composeSystemPrompt(base, override string) string {
	if override == "" {
		return base
	}
	return base + "\n\n" + strings.TrimLeft(override, "\n")
}

// buildToolsNodeConfig 只含两个模式**完全一致**的兜底 handler；Tools 与中间件由调用方填。
func buildToolsNodeConfig() compose.ToolsNodeConfig {
	return compose.ToolsNodeConfig{
		// 兜底：LLM 传了无效 JSON 时，返回 {} 让 InferTool 给业务函数零值 struct，
		// 业务函数里会检查必填字段并返回 ToolResponse{Success:false}，
		// LLM 看到错误信息后可以自行修正参数重试。
		// 不加这个的话 InferTool 内部 sonic.Unmarshal 失败会直接 return error → 整个 Agent 崩。
		ToolArgumentsHandler: func(ctx context.Context, toolName, arguments string) (string, error) {
			if arguments == "" {
				return "{}", nil
			}
			var tmp map[string]any
			if err := sonic.UnmarshalString(arguments, &tmp); err != nil {
				logger.Warnf("[Agent] ToolArgumentsHandler: %s 参数 JSON 解析失败，已降级为空对象: raw=%q, err=%v",
					toolName, strutil.Truncate(arguments, 200), err)
				return "{}", nil
			}
			return arguments, nil
		},
		UnknownToolsHandler: func(ctx context.Context, name, input string) (string, error) {
			logger.Warnf("[Agent] UnknownToolsHandler: LLM 调用了不存在的工具 %q，参数=%s", name, strutil.Truncate(input, 200))
			return fmt.Sprintf("⚠️ 工具 %q 不存在，可用工具请查看系统提示。请检查工具名拼写后重试。", name), nil
		},
	}
}

func knowledgeSearchToolOf(allTools []einoTool.BaseTool) *tool.KnowledgeSearchTool {
	for _, t := range allTools {
		if k, ok := t.(*tool.KnowledgeSearchTool); ok {
			return k
		}
	}
	return nil
}

// logToolSet 打工具清单日志并返回 名称→描述 映射（供 sources 采集用）。
func logToolSet(ctx context.Context, e *Engine, req Request, allTools []einoTool.BaseTool) map[string]string {
	toolDescMap := make(map[string]string, len(allTools))
	userToolsN := 0
	for _, t := range allTools {
		info, err := t.Info(ctx)
		if err != nil {
			logger.Warnf("[Agent] 获取工具信息失败: %v", err)
			continue
		}
		if !e.isInternalToolName(info.Name) {
			userToolsN++
		}
		toolDescMap[info.Name] = info.Desc
		logger.Infof("[Agent]   工具: name=%s, desc=%s", info.Name, strutil.Truncate(info.Desc, 80))
	}
	logger.Infof("[Agent] userID=%s, 工具总数=%d (内置=%d + 用户工具=%d)",
		req.UserID, len(allTools), len(e.internalTools), userToolsN)
	if userToolsN == 0 {
		logger.Warnf("[Agent] 未加载到用户配置的工具（如联网搜索），请检查用户工具配置是否已启用")
	}
	return toolDescMap
}

func randomStr(n int) string {
	const alpha = "0123456789abcdef"
	buf := make([]byte, n)
	seed := time.Now().UnixNano()
	for i := 0; i < n; i++ {
		seed = seed*1103515245 + 12345
		buf[i] = alpha[int(seed>>16)&15]
	}
	return string(buf)
}

func randomStr8() string { return randomStr(8) }

func buildInputMessages(query string, history []entity.ChatMessage) []*schema.Message {
	msgs := make([]*schema.Message, 0, len(history)+1)

	for _, h := range history {
		switch h.Role {
		case "user":
			msgs = append(msgs, schema.UserMessage(h.Content))
		case "assistant":
			msgs = append(msgs, schema.AssistantMessage(h.Content, nil))
		}
	}

	msgs = append(msgs, schema.UserMessage(query))
	return msgs
}

func extractQueryFromArgs(args string) string {
	var params struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(args), &params); err == nil {
		return params.Query
	}
	return args
}

func isToolChoiceUnsupportedError(errMsg string) bool {
	lower := strings.ToLower(errMsg)
	keywords := []string{
		"tool choice",
		"tool_choice",
		"enable-auto-tool-choice",
		"tool-call-parser",
		"tool_calls",
		"function call",
		"function_call",
		"not_supported",
		"not support",
		"unsupported tool",
	}
	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}
