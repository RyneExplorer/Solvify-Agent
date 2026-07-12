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
	for _, t := range allTools {
		info, err := t.Info(ctx)
		if err != nil {
			logger.Warnf("[Agent] 获取工具信息失败: %v", err)
			continue
		}
		toolDescMap[info.Name] = info.Desc
		logger.Infof("[Agent]   工具: name=%s, desc=%s", info.Name, truncateStr(info.Desc, 80))
	}

	// 5. 构建 system prompt
	systemPrompt := buildReActSystemPrompt(ctx, userTools)
	logger.Infof("[Agent] SystemPrompt (前200字符): %s", truncateStr(systemPrompt, 200))

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
