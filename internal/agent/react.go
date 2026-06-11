package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/cloudwego/eino/schema"

	"solvify-agent/internal/llm"
	"solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/rag"
	"solvify-agent/internal/tool"
	"solvify-agent/pkg/config"
	"solvify-agent/pkg/logger"
)

// Execute 执行 Agent 深度模式完整流程：
// 查询改写 → 知识库检索 → 有结果直接生成 / 无结果进入 ReAct（web_search + final_answer）
func (e *Engine) Execute(ctx context.Context, req Request) (<-chan Event, error) {
	maxIter := e.cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 10
	}

	// ReAct 工具：web_search + final_answer（knowledge_search 已在前置流程完成）
	webSearch := tool.NewWebSearchTool("", "")
	finalAnswer := tool.NewFinalAnswerTool()
	reactTools := []tool.Tool{webSearch, finalAnswer}

	llmTools := make([]llm.Tool, 0, len(reactTools))
	for _, t := range reactTools {
		llmTools = append(llmTools, llm.Tool{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters:  t.Parameters(),
		})
	}

	// 转换历史消息格式
	historyMsg := make([]historyMessage, 0, len(req.History))
	for _, msg := range req.History {
		historyMsg = append(historyMsg, historyMessage{
			role:    msg.Role,
			content: msg.Content,
		})
	}

	eventCh := make(chan Event, 100)

	go func() {
		defer close(eventCh)
		e.executeFlow(ctx, flowContext{
			req:        req,
			reactTools: reactTools,
			llmTools:   llmTools,
			history:    historyMsg,
			maxIter:    maxIter,
			eventCh:    eventCh,
		})
	}()

	return eventCh, nil
}

// flowContext 流程上下文
type flowContext struct {
	req        Request
	reactTools []tool.Tool
	llmTools   []llm.Tool
	history    []historyMessage
	maxIter    int
	eventCh    chan<- Event
}

// executeFlow 执行深度模式完整流程
func (e *Engine) executeFlow(ctx context.Context, fc flowContext) {
	// Step 1: 查询改写
	searchQuery := fc.req.Query
	if len(fc.history) > 0 {
		rewritten, err := e.rewriteQuery(ctx, fc.req.LLMClient, fc.history, fc.req.Query)
		if err != nil {
			logger.Warnf("查询改写失败，使用原始问题, sessionID=%s: %v", fc.req.SessionID, err)
		} else if rewritten != "" {
			searchQuery = rewritten
		}
	}

	// Step 2: 知识库检索
	fc.eventCh <- Event{Type: EventProgress, Content: "正在检索知识库..."}
	retrieveResult, sources, err := e.retrieve(ctx, fc.req.UserID, searchQuery, fc.req.KnowledgeBaseIDs)
	if err != nil {
		logger.Errorf("知识库检索失败, sessionID=%s: %v", fc.req.SessionID, err)
		fc.eventCh <- Event{Type: EventError, Error: "知识库检索失败", Done: true}
		return
	}

	// Step 3: 有结果 → 直接生成
	if retrieveResult.Hit && len(retrieveResult.Documents) > 0 {
		fc.eventCh <- Event{Type: EventSources, Sources: sources}
		e.generateDirectAnswer(ctx, fc.req.LLMClient, fc.req.Query, fc.history, retrieveResult, sources, fc.eventCh)
		return
	}

	// Step 4: 知识库无结果 → 进入 ReAct（LLM 自行调用 web_search）
	fc.eventCh <- Event{Type: EventProgress, Content: "知识库未找到相关内容，进入深度推理..."}
	e.enterReActLoop(ctx, fc, sources)
}

// enterReActLoop 进入 ReAct 循环，LLM 自行决定是否调用 web_search
func (e *Engine) enterReActLoop(ctx context.Context, fc flowContext, sources []response.SourceInfo) {
	toolMap := make(map[string]tool.Tool, len(fc.reactTools))
	for _, t := range fc.reactTools {
		toolMap[t.Name()] = t
	}

	toolInfos := make([]toolInfo, 0, len(fc.reactTools))
	for _, t := range fc.reactTools {
		toolInfos = append(toolInfos, toolInfo{
			name:        t.Name(),
			description: t.Description(),
		})
	}

	// 系统提示词告知 LLM：知识库已搜过，没有结果
	systemPrompt := buildReActSystemPrompt(toolInfos)

	messages := []*schema.Message{
		schema.SystemMessage(systemPrompt),
	}
	userMsg := buildUserMessage(fc.req.Query, fc.history)
	messages = append(messages, schema.UserMessage(userMsg))

	for iteration := 0; iteration < fc.maxIter; iteration++ {
		if ctx.Err() != nil {
			fc.eventCh <- Event{Type: EventError, Error: "请求已取消", Done: true}
			return
		}

		logger.Infof("Agent 第 %d 轮迭代", iteration+1)

		// Think
		thinkResult, err := e.think(ctx, fc.req.LLMClient, messages, fc.llmTools)
		if err != nil {
			logger.Errorf("Agent Think 阶段失败: %v", err)
			fc.eventCh <- Event{Type: EventError, Error: "思考过程出错", Done: true}
			return
		}

		if thinkResult.content != "" {
			fc.eventCh <- Event{Type: EventThought, Content: thinkResult.content}
		}

		// 无工具调用 → 直接输出文字
		if len(thinkResult.toolCalls) == 0 {
			fc.eventCh <- Event{Type: EventAnswer, Content: thinkResult.content}
			fc.eventCh <- Event{Type: EventDone, Done: true}
			return
		}

		// 检查 final_answer
		for _, tc := range thinkResult.toolCalls {
			if tc.Name == "final_answer" {
				answer := parseFinalAnswer(tc.Arguments)
				if len(sources) > 0 {
					fc.eventCh <- Event{Type: EventSources, Sources: sources}
				}
				fc.eventCh <- Event{Type: EventAnswer, Content: answer}
				fc.eventCh <- Event{Type: EventDone, Done: true}
				return
			}
		}

		// Act — 执行工具
		toolResults := e.act(ctx, toolMap, thinkResult.toolCalls)

		for _, tr := range toolResults {
			fc.eventCh <- Event{
				Type: EventToolResult,
				ToolResult: &ToolResult{
					Name:    tr.name,
					Content: tr.content,
					Error:   tr.errMsg,
				},
			}
		}

		// Observe — 写入历史
		einoToolCalls := make([]schema.ToolCall, 0, len(thinkResult.toolCalls))
		for _, tc := range thinkResult.toolCalls {
			einoToolCalls = append(einoToolCalls, schema.ToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: schema.FunctionCall{
					Name:      tc.Name,
					Arguments: tc.Arguments,
				},
			})
		}
		messages = append(messages, &schema.Message{
			Role:      schema.Assistant,
			Content:   thinkResult.content,
			ToolCalls: einoToolCalls,
		})

		for _, tr := range toolResults {
			content := tr.content
			if tr.errMsg != "" {
				content = fmt.Sprintf("工具执行失败: %s", tr.errMsg)
			}
			messages = append(messages, &schema.Message{
				Role:       schema.Tool,
				Content:    content,
				ToolCallID: tr.toolCallID,
			})
		}
	}

	logger.Warnf("Agent 达到最大迭代次数 %d", fc.maxIter)
	fc.eventCh <- Event{Type: EventError, Error: "达到最大思考轮次，请尝试简化问题", Done: true}
}

// rewriteQuery 用 LLM 结合历史对话改写用户问题
func (e *Engine) rewriteQuery(ctx context.Context, llmClient llm.Client, history []historyMessage, question string) (string, error) {
	rewriteCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	messages := buildRewritePrompt(history, question)
	resp, err := llmClient.Generate(rewriteCtx, llm.GenerateRequest{Messages: messages})
	if err != nil {
		return "", err
	}
	if resp.Message == nil {
		return "", nil
	}
	return resp.Message.Content, nil
}

// retrieve 执行知识库检索并转换为引用来源
func (e *Engine) retrieve(ctx context.Context, userID, question string, knowledgeBaseIDs []string) (rag.Result, []response.SourceInfo, error) {
	retrieveResult, err := e.retriever.Retrieve(ctx, rag.Query{
		Question:         question,
		TopK:             config.Get().RAG.TopK,
		KnowledgeBaseIDs: knowledgeBaseIDs,
		UserID:           userID,
	})
	if err != nil {
		return rag.Result{}, nil, err
	}

	docMap := make(map[string]*response.SourceInfo)
	docOrder := make([]string, 0)
	for _, doc := range retrieveResult.Documents {
		if _, exists := docMap[doc.DocumentID]; !exists {
			docMap[doc.DocumentID] = &response.SourceInfo{
				DocumentID:      doc.DocumentID,
				KnowledgeBaseID: doc.KnowledgeBaseID,
				Title:           doc.Title,
			}
			docOrder = append(docOrder, doc.DocumentID)
		}
		docMap[doc.DocumentID].Chunks = append(docMap[doc.DocumentID].Chunks, response.ChunkSource{
			ID:      doc.ID,
			Content: doc.Content,
			Score:   doc.Score,
		})
		if doc.Score > docMap[doc.DocumentID].Score {
			docMap[doc.DocumentID].Score = doc.Score
		}
	}

	sources := make([]response.SourceInfo, 0, len(docMap))
	for _, docID := range docOrder {
		sources = append(sources, *docMap[docID])
	}

	logger.Infof("RAG 检索完成: hit=%v, 命中 %d 篇文档", retrieveResult.Hit, len(sources))
	return retrieveResult, sources, nil
}

// generateDirectAnswer 用检索结果直接生成答案（流式输出）
func (e *Engine) generateDirectAnswer(ctx context.Context, llmClient llm.Client, question string, history []historyMessage, retrieveResult rag.Result, sources []response.SourceInfo, eventCh chan<- Event) {
	messages := buildLLMMessages(history, question, retrieveResult)

	stream, err := llmClient.GenerateStream(ctx, llm.GenerateRequest{Messages: messages})
	if err != nil {
		logger.Errorf("LLM 调用失败: %v", err)
		eventCh <- Event{Type: EventError, Error: "LLM 调用失败", Done: true}
		return
	}

	eventCh <- Event{Type: EventSources, Sources: sources}
	var fullContent string
	for chunk := range stream {
		if chunk.Error != nil {
			if fullContent == "" {
				eventCh <- Event{Type: EventError, Error: chunk.Error.Error(), Done: true}
				return
			}
			break
		}
		if chunk.Done {
			break
		}
		fullContent += chunk.Content
		eventCh <- Event{Type: EventAnswer, Content: chunk.Content}
	}

	if fullContent != "" {
		eventCh <- Event{Type: EventDone, Done: true}
	}
}

// buildLLMMessages 组装 LLM 消息列表（供直接生成使用）
func buildLLMMessages(history []historyMessage, question string, retrieveResult rag.Result) []*schema.Message {
	systemPrompt := `你是一个专业的知识问答助手。请严格遵守以下规则：

## 回答规则
1. **优先使用参考资料**：如果参考资料中包含足够的信息来回答问题，请直接基于参考资料作答，不要额外编造内容。
2. **资料不足时适度扩展**：如果参考资料只覆盖了问题的部分方面，可以结合你的通用知识适度补充，但必须明确标注哪些内容来自参考资料、哪些是补充说明。
3. **无相关资料时如实告知**：如果参考资料中完全没有相关信息，请明确告知用户"当前知识库中未找到相关信息"。
4. **绝不编造或篡改**：绝对不要捏造参考资料中不存在的数据、事实或结论。

## 格式要求
- 使用 Markdown 格式输出回答
- 引用参考资料时使用行内标注，如：（来源：文档标题）`

	messages := []*schema.Message{
		schema.SystemMessage(systemPrompt),
	}

	for _, msg := range history {
		switch msg.role {
		case "user":
			messages = append(messages, schema.UserMessage(msg.content))
		case "assistant":
			messages = append(messages, schema.AssistantMessage(msg.content, nil))
		}
	}

	if retrieveResult.Hit {
		contextText := "## 参考资料\n\n"
		for i, doc := range retrieveResult.Documents {
			contextText += fmt.Sprintf("### [%d] %s\n\n%s\n\n", i+1, doc.Title, doc.Content)
		}
		questionText := fmt.Sprintf("%s\n---\n\n**问题**：%s\n\n请根据以上参考资料回答。", contextText, question)
		messages = append(messages, schema.UserMessage(questionText))
	} else {
		questionText := fmt.Sprintf("**问题**：%s\n\n（当前无匹配的参考资料，请如实告知用户知识库中未找到相关信息。）", question)
		messages = append(messages, schema.UserMessage(questionText))
	}

	return messages
}

// thinkResult 思考结果
type thinkResult struct {
	content   string
	toolCalls []llm.ToolCall
}

// think Phase 1: 调用 LLM 思考
func (e *Engine) think(ctx context.Context, llmClient llm.Client, messages []*schema.Message, tools []llm.Tool) (thinkResult, error) {
	resp, err := llmClient.Generate(ctx, llm.GenerateRequest{
		Messages: messages,
		Tools:    tools,
	})
	if err != nil {
		return thinkResult{}, fmt.Errorf("LLM 调用失败: %w", err)
	}

	result := thinkResult{
		toolCalls: resp.ToolCalls,
	}
	if resp.Message != nil {
		result.content = resp.Message.Content
	}
	return result, nil
}

// actResult 工具执行结果
type actResult struct {
	toolCallID string
	name       string
	content    string
	errMsg     string
}

// act Phase 3: 并发执行工具调用
func (e *Engine) act(ctx context.Context, toolMap map[string]tool.Tool, toolCalls []llm.ToolCall) []actResult {
	results := make([]actResult, len(toolCalls))
	var wg sync.WaitGroup

	for i, tc := range toolCalls {
		wg.Add(1)
		go func(idx int, call llm.ToolCall) {
			defer wg.Done()

			t, ok := toolMap[call.Name]
			if !ok {
				results[idx] = actResult{
					toolCallID: call.ID,
					name:       call.Name,
					errMsg:     fmt.Sprintf("工具 %s 不存在", call.Name),
				}
				return
			}

			logger.Infof("执行工具: %s, 参数: %s", call.Name, truncate(call.Arguments, 100))
			content, err := t.Execute(ctx, call.Arguments)
			if err != nil {
				results[idx] = actResult{
					toolCallID: call.ID,
					name:       call.Name,
					errMsg:     err.Error(),
				}
				return
			}

			results[idx] = actResult{
				toolCallID: call.ID,
				name:       call.Name,
				content:    content,
			}
		}(i, tc)
	}

	wg.Wait()
	return results
}

// parseFinalAnswer 从 final_answer 工具参数中提取答案
func parseFinalAnswer(args string) string {
	var params struct {
		Answer string `json:"answer"`
	}
	if err := json.Unmarshal([]byte(args), &params); err == nil && params.Answer != "" {
		return params.Answer
	}
	return args
}

// truncate 截断字符串
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
