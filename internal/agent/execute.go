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

	ksTool := e.knowledgeSearchFactory(req.UserID, req.KnowledgeBaseIDs)
	grepTool := e.grepChunksFactory(req.UserID, req.KnowledgeBaseIDs)
	docInfoTool := e.getDocumentInfoFactory(req.UserID)
	listChunksTool := e.listKnowledgeChunksFactory(req.UserID, req.KnowledgeBaseIDs)
	listBasesTool := e.listKnowledgeBasesFactory(req.UserID)

	userTools := e.toolFactory.CreateAgentTools(ctx, req.UserID)

	allTools := make([]einoTool.BaseTool, 0, 5+len(userTools))
	allTools = append(allTools, ksTool)
	allTools = append(allTools, grepTool)
	allTools = append(allTools, docInfoTool)
	allTools = append(allTools, listChunksTool)
	allTools = append(allTools, listBasesTool)
	allTools = append(allTools, userTools...)

	toolDescMap := make(map[string]string, len(allTools))
	logger.Infof("[Agent] userID=%s, 工具总数=%d (内置5个 + %d 用户工具)", req.UserID, len(allTools), len(userTools))
	if len(userTools) == 0 {
		logger.Warnf("[Agent] 未加载到用户配置的工具（如联网搜索），请检查用户工具配置是否已启用")
	}
	for _, t := range allTools {
		info, err := t.Info(ctx)
		if err != nil {
			logger.Warnf("[Agent] 获取工具信息失败: %v", err)
			continue
		}
		toolDescMap[info.Name] = info.Desc
		logger.Infof("[Agent]   工具: name=%s, desc=%s", info.Name, truncateStr(info.Desc, 80))
	}

	baseSystemPrompt := buildReActSystemPrompt(ctx, userTools)
	systemPrompt := buildEnhancedSystemPromptForAgent(baseSystemPrompt, req.Summary, req.Memories, req.UserCtx)
	logger.Infof("[Agent] SystemPrompt (前400字符): %s", truncateStr(systemPrompt, 400))

	inputMessages := buildInputMessages(req.Query, req.History)

	maxStep := e.cfg.MaxIterations
	if maxStep <= 0 {
		maxStep = 5
	}

	agent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: chatModel,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools: allTools,
		},
		MaxStep: maxStep,
		MessageModifier: func(_ context.Context, msgs []*schema.Message) []*schema.Message {
			return append([]*schema.Message{schema.SystemMessage(systemPrompt)}, msgs...)
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

	stream, err := agent.Stream(ctx, inputMessages, einoAgent.WithComposeOptions(compose.WithCallbacks(callbackHandler.Handler())))
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

	e.processStream(ctx, stream, ksTool, eventCh)
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

// buildEnhancedSystemPromptForAgent 在 ReAct 系统提示词上注入：时间/用户信息 + 对话摘要 + 用户记忆
// 与快速模式的 buildEnhancedSystemPrompt 逻辑保持一致，确保双模式行为统一
func buildEnhancedSystemPromptForAgent(base string, summary *entity.ChatSummary, memories []entity.UserMemory, userCtx PromptUserContext) string {
	var extras []string

	userInfo := "## 当前信息\n"
	if userCtx.TimeStr != "" {
		userInfo += "- 当前时间：" + userCtx.TimeStr + "\n"
	}
	if userCtx.Timezone != "" {
		userInfo += "- 用户时区：" + userCtx.Timezone + "\n"
	}
	if userCtx.Username != "" {
		userInfo += "- 用户：" + userCtx.Username + "\n"
	}
	if userCtx.Role != "" {
		userInfo += "- 系统角色：" + userCtx.Role + "\n"
	}
	if userCtx.Department != "" {
		userInfo += "- 部门：" + userCtx.Department + "\n"
	}
	if userCtx.Position != "" {
		userInfo += "- 职位：" + userCtx.Position + "\n"
	}
	if userCtx.Expertise != "" {
		userInfo += "- 擅长/关注：" + userCtx.Expertise + "\n"
	}
	if userCtx.Language != "" {
		userInfo += "- 偏好语言：" + userCtx.Language + "\n"
	}
	if userInfo != "## 当前信息\n" {
		extras = append(extras, userInfo)
	}

	if userCtx.AnswerStyle != "" || userCtx.TableFirst || userCtx.CitationStyle != "" {
		var p strings.Builder
		p.WriteString("## 用户回答偏好\n")
		switch userCtx.AnswerStyle {
		case "concise":
			p.WriteString("- 回答风格：简洁凝练，直击要点，3~5 句说完，不过度展开\n")
		case "detailed":
			p.WriteString("- 回答风格：详细展开，先结论再分点论述，必要时给例子和注意事项\n")
		case "step_by_step":
			p.WriteString("- 回答风格：分步讲解，用 1/2/3…编号或小标题组织步骤\n")
		default:
			p.WriteString("- 回答风格：平衡简洁与完整，先结论再展开\n")
		}
		if userCtx.TableFirst {
			p.WriteString("- 结构化呈现：对比、列表、映射等数据优先用 Markdown 表格组织\n")
		}
		switch userCtx.CitationStyle {
		case "none":
			p.WriteString("- 引用格式：正文不标注引用，引用信息仅由消息底部来源区展示\n")
		case "doc_title_only":
			p.WriteString("- 引用格式：正文引用时只提「根据《文档名》」，不要章节\n")
		default:
			p.WriteString("- 引用格式：正文引用时以「根据《文档名》· 章节标题」形式说明来源\n")
		}
		extras = append(extras, p.String())
	}

	if strings.TrimSpace(userCtx.RoleTemplatePrompt) != "" {
		extras = append(extras, "## 角色模板设定\n"+strings.TrimSpace(userCtx.RoleTemplatePrompt))
	}

	if userCtx.Language != "" {
		langHint := "## 回答语言\n"
		switch userCtx.Language {
		case "en-US":
			langHint += "- 请使用英文回答（美式英语）。\n"
		case "ja-JP":
			langHint += "- 请使用日语回答。\n"
		case "ko-KR":
			langHint += "- 请使用韩语回答。\n"
		case "fr-FR":
			langHint += "- 请使用法语回答。\n"
		case "de-DE":
			langHint += "- 请使用德语回答。\n"
		case "es-ES":
			langHint += "- 请使用西班牙语回答。\n"
		default:
			langHint += "- 请使用简体中文回答。\n"
		}
		extras = append(extras, langHint)
	}

	if summary != nil && summary.Summary != "" {
		extras = append(extras, "## 本次对话摘要\n"+summary.Summary)
	}

	if len(memories) > 0 {
		var memoryText strings.Builder
		memoryText.WriteString("## 关于用户的已知信息\n")
		for _, m := range memories {
			memoryText.WriteString("- ")
			memoryText.WriteString(m.Content)
			memoryText.WriteString("\n")
		}
		extras = append(extras, memoryText.String())
	}

	if len(extras) == 0 {
		return base
	}

	return base + "\n\n" + strings.Join(extras, "\n\n")
}
