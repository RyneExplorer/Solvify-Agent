package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/model"
	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	einoAgent "github.com/cloudwego/eino/flow/agent"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/internal/model/dto/response"
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

	var allTools []einoTool.BaseTool
	if pre, ok := prebuiltToolsFromContext(ctx); ok && len(pre.Tools) > 0 {
		// 走深度模式入口预构建分支：工具集 + toolsTokens 已经提前扣好
		allTools = pre.Tools
	} else {
		// 回退分支（如 Execute 被直接调用、预构建失败）：按原逻辑现场构建
		ksTool := e.knowledgeSearchFactory(req.UserID, req.KnowledgeBaseIDs)
		grepTool := e.grepChunksFactory(req.UserID, req.KnowledgeBaseIDs)
		docInfoTool := e.getDocumentInfoFactory(req.UserID)
		listChunksTool := e.listKnowledgeChunksFactory(req.UserID, req.KnowledgeBaseIDs)
		listBasesTool := e.listKnowledgeBasesFactory(req.UserID)
		userTools := e.toolFactory.CreateAgentTools(ctx, req.UserID)

		allTools = make([]einoTool.BaseTool, 0, 5+len(userTools))
		allTools = append(allTools, ksTool)
		allTools = append(allTools, grepTool)
		allTools = append(allTools, docInfoTool)
		allTools = append(allTools, listChunksTool)
		allTools = append(allTools, listBasesTool)
		allTools = append(allTools, userTools...)
	}

	userTools := e.toolFactory.CreateAgentTools(ctx, req.UserID) // 仅用于下面日志中"用户工具数"展示
	_ = userTools

	toolDescMap := make(map[string]string, len(allTools))
	userToolsN := 0
	for _, t := range allTools {
		info, err := t.Info(ctx)
		if err != nil {
			logger.Warnf("[Agent] 获取工具信息失败: %v", err)
			continue
		}
		// heuristics: 内置 5 个工具名按前缀匹配，剩下的记作用户工具；日志只用于展示，不影响执行
		switch info.Name {
		case "knowledge_search", "grep_chunks", "get_document_info", "list_knowledge_chunks", "list_knowledge_bases":
		default:
			userToolsN++
		}
		toolDescMap[info.Name] = info.Desc
		logger.Infof("[Agent]   工具: name=%s, desc=%s", info.Name, truncateStr(info.Desc, 80))
	}
	logger.Infof("[Agent] userID=%s, 工具总数=%d (内置5个 + %d 用户工具)", req.UserID, len(allTools), userToolsN)
	if userToolsN < 0 {
		userToolsN = 0
	}
	if userToolsN == 0 {
		logger.Warnf("[Agent] 未加载到用户配置的工具（如联网搜索），请检查用户工具配置是否已启用")
	}

	baseSystemPrompt := buildReActSystemPrompt(ctx, userTools)
	var ksToolForStream *tool.KnowledgeSearchTool
	for _, t := range allTools {
		if k, ok := t.(*tool.KnowledgeSearchTool); ok {
			ksToolForStream = k
			break
		}
	}
	var systemPromptFinal string
	if req.SystemPrompt != "" {
		// BuildSystem() 以空 baseSystem 构建时，结果会前导 "\n\n"，去掉避免多余空行
		enhanced := strings.TrimLeft(req.SystemPrompt, "\n")
		systemPromptFinal = baseSystemPrompt + "\n\n" + enhanced
	} else {
		// 兜底：req.SystemPrompt 为空时只用 ReAct 规则（正常流程不会走到这里，
		// PromptBuilder.BuildSystem() 总会产出摘要/记忆/用户信息之一）
		systemPromptFinal = baseSystemPrompt
	}
	logger.Infof("[Agent] SystemPrompt (前400字符): %s", truncateStr(systemPromptFinal, 400))
	inputMessages := buildInputMessages(req.Query, req.History)
	// 运行 Agent 流程
	{
		maxStep := e.cfg.MaxIterations
		if maxStep <= 0 {
			maxStep = 5
		}

		ag, err := react.NewAgent(ctx, &react.AgentConfig{
			ToolCallingModel: chatModel,
			ToolsConfig: compose.ToolsNodeConfig{
				Tools: allTools,
			},
			MaxStep: maxStep,
			MessageModifier: func(_ context.Context, msgs []*schema.Message) []*schema.Message {
				return append([]*schema.Message{schema.SystemMessage(systemPromptFinal)}, msgs...)
			},
		})
		if err != nil {
			logger.Errorf("Agent 初始化失败: %v", err)
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

		callbackHandler := newAgentCallbackHandler(eventCh, req.KnowledgeBaseIDs, toolDescMap)
		callbackHandler.taskID = taskID
		callbackHandler.tracker = tracker
		callbackHandler.obs = e.obs

		stream, err := ag.Stream(ctx, inputMessages, einoAgent.WithComposeOptions(compose.WithCallbacks(callbackHandler.Handler())))
		if err != nil {
			logger.Errorf("Agent 调用失败: %v", err)
			if obsOk {
				e.obs.Incr(ctx, "agent_engine_errors_total", map[string]string{"stage": "stream"}, 1)
			}
			errMsg := err.Error()

			if isToolChoiceUnsupportedError(errMsg) {
				eventCh <- Event{
					Type:      EventError,
					Title:     "当前模型不支持工具调用",
					Detail:    "该模型不支持工具调用功能，无法使用联网搜索、天气查询等工具。建议切换到支持工具调用的模型（如通义千问、智谱清言、DeepSeek 等），或使用快速模式。",
					Error:     errMsg,
					Status:    "error",
					Retryable: false,
					Done:      true,
				}
				return
			}

			eventCh <- Event{
				Type:      EventError,
				Title:     "深度推理失败",
				Detail:    "深度思考模式执行异常，请重试或使用快速模式",
				Error:     errMsg,
				Status:    "error",
				Retryable: true,
				Done:      true,
			}
			return
		}

		e.processStream(ctx, stream, ksToolForStream, eventCh)
	}
}

func randomStr16() string {
	const alpha = "0123456789abcdef"
	out := make([]byte, 16)
	seed := time.Now().UnixNano()
	for i := range out {
		seed = seed*1103515245 + 12345
		out[i] = alpha[int(seed>>16)&15]
	}
	return string(out)
}

func (e *Engine) processStream(ctx context.Context, stream *schema.StreamReader[*schema.Message], ksTool *tool.KnowledgeSearchTool, eventCh chan<- Event) {
	defer stream.Close()

	var fullAnswer string

	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			if ctx.Err() != nil {
				logger.Infof("Agent 流被用户中断，已收集 %d 字符", len(fullAnswer))
				break
			}
			logger.Errorf("Agent 流读取失败: %v", err)
			eventCh <- Event{
				Type:      EventError,
				Title:     "推理过程中断",
				Detail:    "深度推理过程中断，请重试",
				Error:     err.Error(),
				Status:    "error",
				Retryable: true,
				Done:      true,
			}
			return
		}
		if msg == nil {
			continue
		}

		if msg.Role != schema.Assistant {
			continue
		}

		if len(msg.ToolCalls) > 0 {
			// ── 中间思考轮次（下一步还要调用工具）──
			// 1) msg.Content 是 reasoning/推理思考，不能作为最终答案给用户看
			// 2) 只发 EventThinking 通知前端进度，不发 EventAnswer，不拼进 fullAnswer
			if strings.TrimSpace(msg.Content) != "" {
				thinking := truncateStr(msg.Content, 200)
				eventCh <- Event{
					Type:   EventThinking,
					Title:  "深度推理中",
					Detail: thinking,
					Status: "running",
				}
			}
			continue
		}

		if msg.Content != "" {
			// ── 最终答案轮次（没有下一步 ToolCalls，真正面向用户的正文）──
			fullAnswer += msg.Content
			eventCh <- Event{Type: EventAnswer, Content: msg.Content}
		}
	}

	// ── 兜底：极端情况（每一轮都有 ToolCalls，MaxStep 到了还没出最终答案）
	//    用知识库已命中的前 N 条来源拼一个总结，绝对不能把中间思考当答案发
	if strings.TrimSpace(fullAnswer) == "" && len(ksTool.CollectedSources) > 0 {
		var sb strings.Builder
		sb.WriteString("## 知识库检索结果总结\n\n")
		sb.WriteString("根据当前检索到的内容，为您整理以下要点：\n\n")
		usedTitles := make(map[string]bool, len(ksTool.CollectedSources))
		const maxTop = 5
		for i, src := range ksTool.CollectedSources {
			if i >= maxTop {
				break
			}
			title := src.Title
			if title == "" {
				title = "未命名文档"
			}
			// 同一个文档只拼一次摘要，重复 chunk 跳过
			if usedTitles[title] {
				continue
			}
			usedTitles[title] = true
			content := strings.TrimSpace(src.Content)
			if len(content) > 160 {
				content = content[:160] + "…"
			}
			chunkID := src.ID
			if chunkID == "" {
				chunkID = fmt.Sprintf("c%d", i)
			}
			sb.WriteString(fmt.Sprintf("- %s <kb doc=%q chunk_id=%q />\n", title, title, chunkID))
			if content != "" {
				sb.WriteString(fmt.Sprintf("  > %s\n\n", content))
			}
		}
		sb.WriteString("\n如需进一步分析请补充问题细节，或切换到快速模式获取更直接的回答。")
		fullAnswer = sb.String()
		eventCh <- Event{Type: EventAnswer, Content: fullAnswer}
	}

	var sources []response.SourceInfo
	type docInfo struct {
		documentID      string
		knowledgeBaseID string
		chunks          []response.ChunkSource
	}
	docMap := make(map[string]*docInfo)
	for _, doc := range ksTool.CollectedSources {
		if _, exists := docMap[doc.Title]; !exists {
			docMap[doc.Title] = &docInfo{
				documentID:      doc.DocumentID,
				knowledgeBaseID: doc.KnowledgeBaseID,
			}
		}
		docMap[doc.Title].chunks = append(docMap[doc.Title].chunks, response.ChunkSource{
			ID:      doc.ID,
			Content: doc.Content,
			Score:   doc.Score,
		})
	}
	for title, info := range docMap {
		sources = append(sources, response.SourceInfo{
			DocumentID:      info.documentID,
			KnowledgeBaseID: info.knowledgeBaseID,
			Title:           title,
			Chunks:          info.chunks,
		})
	}

	if strings.TrimSpace(fullAnswer) != "" {
		eventCh <- Event{Type: EventThinking, Title: "正在生成答案", Status: "success"}
	}

	if len(sources) > 0 {
		eventCh <- Event{Type: EventSources, Sources: sources}
	}

	eventCh <- Event{
		Type:    EventDone,
		Content: fullAnswer,
		Sources: sources,
	}
}

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

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
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
