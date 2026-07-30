package agent

import (
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/cloudwego/eino/components/model"
	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/compose"
	einoAgent "github.com/cloudwego/eino/flow/agent"
	"github.com/cloudwego/eino/flow/agent/react"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
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

func (e *Engine) runAgent(ctx context.Context, req Request, chatModel model.ToolCallingChatModel, eventCh chan<- Event) {
	// 1. 创建带用户上下文的内置工具
	ksTool := e.knowledgeSearchFactory(req.UserID, req.KnowledgeBaseIDs)
	grepTool := e.grepChunksFactory(req.UserID, req.KnowledgeBaseIDs)
	docInfoTool := e.getDocumentInfoFactory(req.UserID)
	listChunksTool := e.listKnowledgeChunksFactory(req.UserID, req.KnowledgeBaseIDs)
	listBasesTool := e.listKnowledgeBasesFactory(req.UserID)

	// 2. 从 DB/Redis 加载用户配置的工具（联网搜索等）
	userTools := e.toolFactory.CreateAgentTools(ctx, req.UserID)

	// 3. 合并工具列表
	allTools := make([]einoTool.BaseTool, 0, 5+len(userTools))
	allTools = append(allTools, ksTool)
	allTools = append(allTools, grepTool)
	allTools = append(allTools, docInfoTool)
	allTools = append(allTools, listChunksTool)
	allTools = append(allTools, listBasesTool)
	allTools = append(allTools, userTools...)

	// 4. 打印最终工具清单 + 构建 toolDescMap
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

	// 5. 构建 system prompt（ReAct 规则 + 摘要/记忆/用户上下文增强）
	baseSystemPrompt := buildReActSystemPrompt(ctx, userTools)
	systemPrompt := buildEnhancedSystemPromptForAgent(baseSystemPrompt, req.Summary, req.Memories, req.UserCtx)
	logger.Infof("[Agent] SystemPrompt (前400字符): %s", truncateStr(systemPrompt, 400))

	// 6. 构建输入消息（历史 + 当前问题）
	inputMessages := buildInputMessages(req.Query, req.History)

	// 7. 确定最大步数
	maxStep := e.cfg.MaxIterations
	if maxStep <= 0 {
		maxStep = 5
	}

	// 8. 创建 eino ReAct Agent
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

	// 9. 注册回调处理器
	callbackHandler := newAgentCallbackHandler(eventCh, req.KnowledgeBaseIDs, toolDescMap)

	// 10. 流式调用 Agent
	stream, err := agent.Stream(ctx, inputMessages, einoAgent.WithComposeOptions(compose.WithCallbacks(callbackHandler)))
	if err != nil {
		logger.Errorf("Agent 调用失败: %v", err)
		errMsg := err.Error()

		// 检测模型不支持工具调用的情况
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

	// 11. 读取流式消息，转换为 SSE 事件
	e.processStream(ctx, stream, ksTool, eventCh)
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

		if msg.Role == schema.Assistant && msg.Content != "" {
			fullAnswer += msg.Content
			eventCh <- Event{Type: EventAnswer, Content: msg.Content}
		}
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

	eventCh <- Event{Type: EventThinking, Title: "正在生成答案", Status: "success"}

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
