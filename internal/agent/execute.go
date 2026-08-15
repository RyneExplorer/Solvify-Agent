package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bytedance/sonic"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/components/model"
	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/observability"
	"solvify-agent/internal/tool"
	"solvify-agent/pkg/logger"
)

// Execute 启动 Agent 执行流程，通过事件通道异步返回推理结果
func (e *Engine) Execute(ctx context.Context, req Request, chatModel model.ToolCallingChatModel) (<-chan Event, error) {
	eventCh := make(chan Event, 100)

	go func() {
		defer close(eventCh)
		e.runAgent(ctx, req, chatModel, eventCh)
	}()

	return eventCh, nil
}

type agentStepTracker struct {
	mu          sync.Mutex
	stepIdx     int
	pendingByID map[string]*agentStepPending
	closed      bool
}

type agentStepPending struct {
	StepIndex       int
	TaskID          string
	ThinkingSummary string
	ToolName        string
	ToolInputMasked string
	StartedAt       time.Time
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
	obsOk := e.obs != nil
	var tracker *agentStepTracker
	taskID := ""
	if obsOk {
		taskID = observability.TraceIDFromContext(ctx)
		if taskID == "" {
			taskID = randomStr16()
		}
		tracker = &agentStepTracker{
			pendingByID: make(map[string]*agentStepPending),
		}
		e.obs.Incr(ctx, "agent_engine_runs_total", nil, 1)
	}

	// ── 构建工具列表：内置 registry + 用户配置 ──
	var allTools []einoTool.BaseTool

	// 内置工具按 Order 排序后逐个 Build
	sorted := make([]internalToolRegistryEntry, len(e.internalTools))
	copy(sorted, e.internalTools)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Order < sorted[j].Order })
	for _, entry := range sorted {
		allTools = append(allTools, entry.Build(ctx, req.UserID, req.KnowledgeBaseIDs))
	}

	// 用户配置的工具
	userTools := e.toolFactory.CreateAgentTools(ctx, req.UserID)
	allTools = append(allTools, userTools...)

	// ── 工具统计 + 日志 ──
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
		logger.Infof("[Agent]   工具: name=%s, desc=%s", info.Name, truncateStr(info.Desc, 80))
	}
	logger.Infof("[Agent] userID=%s, 工具总数=%d (内置=%d + 用户工具=%d)",
		req.UserID, len(allTools), len(e.internalTools), userToolsN)
	if userToolsN == 0 {
		logger.Warnf("[Agent] 未加载到用户配置的工具（如联网搜索），请检查用户工具配置是否已启用")
	}

	// ── 系统提示词 ──
	baseSystemPrompt := buildReActSystemPrompt(ctx, allTools, sorted)
	var ksToolForStream *tool.KnowledgeSearchTool
	for _, t := range allTools {
		if k, ok := t.(*tool.KnowledgeSearchTool); ok {
			ksToolForStream = k
			break
		}
	}
	var systemPromptFinal string
	if req.SystemPrompt != "" {
		enhanced := strings.TrimLeft(req.SystemPrompt, "\n")
		systemPromptFinal = baseSystemPrompt + "\n\n" + enhanced
	} else {
		systemPromptFinal = baseSystemPrompt
	}
	logger.Infof("[Agent] SystemPrompt (前400字符): %s", truncateStr(systemPromptFinal, 400))

	inputMessages := buildInputMessages(req.Query, req.History)

	maxStep := e.cfg.MaxIterations
	if maxStep <= 0 {
		maxStep = 5
	}

	// ── 创建 adk.ChatModelAgent ──
	toolsNodeConfig := compose.ToolsNodeConfig{
		Tools: allTools,

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
					toolName, truncateStr(arguments, 200), err)
				return "{}", nil
			}
			return arguments, nil
		},

		UnknownToolsHandler: func(ctx context.Context, name, input string) (string, error) {
			logger.Warnf("[Agent] UnknownToolsHandler: LLM 调用了不存在的工具 %q，参数=%s", name, truncateStr(input, 200))
			return fmt.Sprintf("⚠️ 工具 %q 不存在，可用工具请查看系统提示。请检查工具名拼写后重试。", name), nil
		},
	}
	// ── 注入中间件：危险工具审批 + 澄清追问 ──
	var middlewares []compose.ToolMiddleware
	if dangerousNames := e.dangerousToolNames(); len(dangerousNames) > 0 {
		middlewares = append(middlewares, compose.ToolMiddleware{Invokable: buildDangerousToolMiddleware(dangerousNames)})
		logger.Infof("[Agent] 已注入危险工具审批中间件: %v", dangerousNames)
	}
	if clarifyNames := e.clarifyToolNames(); len(clarifyNames) > 0 {
		middlewares = append(middlewares, compose.ToolMiddleware{Invokable: buildClarifyMiddleware(clarifyNames)})
		logger.Infof("[Agent] 已注入澄清追问中间件: %v", clarifyNames)
	}
	if len(middlewares) > 0 {
		toolsNodeConfig.ToolCallMiddlewares = middlewares
	}

	agent, err := adk.NewChatModelAgent(ctx, &adk.ChatModelAgentConfig{
		Name:        "SolvifyDeepAgent",
		Description: "深度模式 Agent，能够调用知识库和外部工具进行多步推理",
		Instruction: systemPromptFinal,
		Model:       chatModel,
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: toolsNodeConfig,
		},
		MaxIterations: maxStep,
	})
	if err != nil {
		logger.Errorf("[Agent] ChatModelAgent 初始化失败: %v", err)
		if obsOk {
			e.obs.Incr(ctx, "agent_engine_errors_total", map[string]string{"stage": "init"}, 1)
		}
		eventCh <- Event{
			Type:      EventError,
			Title:     "深度模式启动失败",
			Detail:    "请尝试切换到快速模式，或稍后重试",
			Error:     err.Error(),
			Status:    "error",
			Retryable: true,
			Done:      true,
		}
		return
	}

	// ── 创建 Runner ──
	checkpointID := req.CheckpointID
	if checkpointID == "" {
		checkpointID = fmt.Sprintf("agent-%s-%s-%s-%d", req.SessionID, req.UserID, randomStr8(), time.Now().UnixNano())
	} else {
		logger.Infof("[Agent] 恢复执行：复用 checkpointID=%s", checkpointID)
	}
	store := e.buildCheckpointStore(req.SessionID)
	runner := adk.NewRunner(ctx, adk.RunnerConfig{
		Agent:           agent,
		EnableStreaming: true,
		CheckPointStore: store,
	})

	// ── 执行：首次 Run 或带 ResumeData 的 Resume ──
	e.runWithRunner(ctx, runner, checkpointID, inputMessages, req, ksToolForStream, toolDescMap, eventCh, tracker, taskID)
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

func randomStr16() string { return randomStr(16) }
func randomStr8() string  { return randomStr(8) }

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

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
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
