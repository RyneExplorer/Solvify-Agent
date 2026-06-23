package agent

import (
	"context"
	"encoding/json"
	"io"

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

// Execute 执行 Agent 推理循环
//
// Agent 自主决定工具调用时机：knowledge_search / web_search
// 内部使用 eino ReAct Agent，自动管理 Think → Act → Observe 循环
func (e *Engine) Execute(ctx context.Context, req Request, chatModel model.ToolCallingChatModel) (<-chan Event, error) {
	eventCh := make(chan Event, 100)

	go func() {
		defer close(eventCh)
		e.runAgent(ctx, req, chatModel, eventCh)
	}()

	return eventCh, nil
}

// runAgent 创建 eino ReAct Agent 并处理流式输出
func (e *Engine) runAgent(ctx context.Context, req Request, chatModel model.ToolCallingChatModel, eventCh chan<- Event) {
	// 1. 创建带用户上下文的 knowledge_search 工具
	ksTool := e.knowledgeSearchFactory(req.UserID, req.KnowledgeBaseIDs)

	// 2. 从 DB/Redis 加载用户配置的工具（联网搜索等）
	userTools := e.toolFactory.CreateAgentTools(ctx, req.UserID)

	// 3. 合并工具列表（先加载工具，再构建 prompt——因为 prompt 需要动态列出可用工具）
	allTools := make([]einoTool.BaseTool, 0, 1+len(userTools))
	allTools = append(allTools, ksTool)
	allTools = append(allTools, userTools...)

	// 打印最终工具清单 + 构建 toolDescMap（供 callback 识别工具类别）
	toolDescMap := make(map[string]string, len(allTools))
	logger.Infof("[Agent] userID=%s, 工具总数=%d (knowledge_search + %d 用户工具)", req.UserID, len(allTools), len(userTools))
	for _, t := range allTools {
		info, err := t.Info(ctx)
		if err != nil {
			logger.Warnf("[Agent] 获取工具信息失败: %v", err)
			continue
		}
		toolDescMap[info.Name] = info.Desc
		logger.Infof("[Agent]   工具: name=%s, desc=%s", info.Name, truncateStr(info.Desc, 80))
	}

	// 4. 动态构建 system prompt（根据实际加载的工具列表生成）
	systemPrompt := buildReActSystemPrompt(ctx, userTools)
	logger.Infof("[Agent] SystemPrompt (前200字符): %s", truncateStr(systemPrompt, 200))

	// 5. 构建输入消息（历史 + 当前问题）
	inputMessages := buildInputMessages(req.Query, req.History)

	// 6. 确定最大步数（每轮 Think+Act 算一步，默认给足空间）
	maxStep := e.cfg.MaxIterations
	if maxStep <= 0 {
		maxStep = 8
	}

	// 7. 创建 eino ReAct Agent
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

	// 8. 注册回调处理器，捕获中间事件（思考、工具调用等）
	callbackHandler := newAgentCallbackHandler(eventCh, req.KnowledgeBaseIDs, toolDescMap)

	// 9. 流式调用 Agent（通过回调捕获中间事件）
	stream, err := agent.Stream(ctx, inputMessages, einoAgent.WithComposeOptions(compose.WithCallbacks(callbackHandler)))
	if err != nil {
		logger.Errorf("Agent 调用失败: %v", err)
		eventCh <- Event{
			Type:      EventError,
			Title:     "深度推理失败",
			Detail:    "深度思考模式执行异常，请重试或使用快速模式",
			Error:     err.Error(),
			Status:    "error",
			Retryable: true,
			Done:      true,
		}
		return
	}

	// 10. 读取流式消息，转换为 SSE 事件
	e.processStream(ctx, stream, ksTool, eventCh)
}

// processStream 读取 eino Agent 的流式输出，转换为 SSE 事件
//
// 注意："正在生成答案" (running) 已由 callback.go 在 ChatModel onEnd（无工具调用时）发出，
// 这里只负责流式推送答案内容和最终的"正在生成答案" (success) 标记。
func (e *Engine) processStream(ctx context.Context, stream *schema.StreamReader[*schema.Message], ksTool *tool.KnowledgeSearchTool, eventCh chan<- Event) {
	defer stream.Close()

	var fullAnswer string

	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			// 用户主动中断时，ctx 会被取消，不发送错误事件，直接结束
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

		// eino ReAct Agent 的 Stream() 只输出最终答案（分块）
		if msg.Role == schema.Assistant && msg.Content != "" {
			fullAnswer += msg.Content
			// 直接发送文本（包含 <kb> 标签），前端负责解析
			eventCh <- Event{Type: EventAnswer, Content: msg.Content}
		}
	}

	// 构建 sources 列表
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

	// 标记答案生成完成
	eventCh <- Event{Type: EventThinking, Title: "正在生成答案", Status: "success"}

	// 发送来源信息（由 Service 层统一封装到最终 done 事件中）
	if len(sources) > 0 {
		eventCh <- Event{Type: EventSources, Sources: sources}
	}

	// 发送完成信号（Done=false，不终止 SSE——由 Service 层的 emitDoneAndSave 统一终止）
	// Content 为完整答案，供 Service 层收集后持久化
	eventCh <- Event{
		Type:    EventDone,
		Content: fullAnswer,
		Sources: sources,
	}
}

// buildInputMessages 构建输入消息列表（历史对话 + 当前问题）
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

// extractQueryFromArgs 从工具参数 JSON 中提取 query 字段
func extractQueryFromArgs(args string) string {
	var params struct {
		Query string `json:"query"`
	}
	if err := json.Unmarshal([]byte(args), &params); err == nil {
		return params.Query
	}
	return args
}

// truncateStr 截断字符串到指定长度
func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
