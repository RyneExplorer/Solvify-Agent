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
	// 1. 构建 system prompt
	systemPrompt := buildReActSystemPrompt()

	// 2. 构建输入消息（历史 + 当前问题）
	inputMessages := buildInputMessages(req.Query, req.History)

	// 3. 创建带用户上下文的工具（直接实现 eino InvokableTool，无需适配）
	ksTool := e.knowledgeSearchFactory(req.UserID, req.KnowledgeBaseIDs)

	// 4. 确定最大步数（每轮 Think+Act 算一步，默认给足空间）
	maxStep := e.cfg.MaxIterations
	if maxStep <= 0 {
		maxStep = 8
	}

	// 5. 创建 eino ReAct Agent
	agent, err := react.NewAgent(ctx, &react.AgentConfig{
		ToolCallingModel: chatModel,
		ToolsConfig: compose.ToolsNodeConfig{
			Tools: []einoTool.BaseTool{ksTool, e.webSearchTool},
		},
		MaxStep: maxStep,
		MessageModifier: func(_ context.Context, msgs []*schema.Message) []*schema.Message {
			return append([]*schema.Message{schema.SystemMessage(systemPrompt)}, msgs...)
		},
	})
	if err != nil {
		eventCh <- Event{Type: EventError, Title: "Agent 初始化失败", Detail: err.Error(), Status: "error", Done: true}
		return
	}

	// 6. 注册回调处理器，捕获中间事件（思考、工具调用等）
	callbackHandler := newAgentCallbackHandler(eventCh)

	// 7. 流式调用 Agent（通过回调捕获中间事件）
	stream, err := agent.Stream(ctx, inputMessages, einoAgent.WithComposeOptions(compose.WithCallbacks(callbackHandler)))
	if err != nil {
		eventCh <- Event{Type: EventError, Title: "Agent 调用失败", Detail: err.Error(), Status: "error", Done: true}
		return
	}

	// 8. 读取流式消息，转换为 SSE 事件
	e.processStream(stream, ksTool, eventCh)
}

// processStream 读取 eino Agent 的流式输出，转换为 SSE 事件
// LLM 直接在文本中输出 <kb doc="..." chunk_id="..." /> 引用标签，前端解析渲染
func (e *Engine) processStream(stream *schema.StreamReader[*schema.Message], ksTool *tool.KnowledgeSearchTool, eventCh chan<- Event) {
	defer stream.Close()

	emittedThinking := false
	var fullAnswer string

	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			logger.Errorf("Agent 流读取失败: %v", err)
			eventCh <- Event{Type: EventError, Title: "Agent 流读取失败", Detail: err.Error(), Status: "error", Done: true}
			return
		}
		if msg == nil {
			continue
		}

		// eino ReAct Agent 的 Stream() 只输出最终答案（分块）
		if msg.Role == schema.Assistant && msg.Content != "" {
			if !emittedThinking {
				emittedThinking = true
				eventCh <- Event{Type: EventThinking, Title: "正在生成答案", Status: "running"}
			}
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

	// 发送来源和完成事件
	if len(sources) > 0 {
		eventCh <- Event{Type: EventSources, Sources: sources}
	}
	eventCh <- Event{
		Type:    EventDone,
		Content: fullAnswer,
		Sources: sources,
		Done:    true,
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
