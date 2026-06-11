package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/cloudwego/eino/schema"

	"solvify-agent/internal/llm"
	"solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/tool"
	"solvify-agent/pkg/logger"
)

// reActLoop ReAct 推理循环
//
// 流程：Think → Analyze → Act → Observe，最多 maxIter 轮
//   - Think:   LLM 思考，决定下一步
//   - Analyze: final_answer → 结束；无工具 → 直接输出
//   - Act:     执行工具（knowledge_search / web_search）
//   - Observe: 结果写入消息历史，进入下一轮
func (e *Engine) reActLoop(ctx context.Context, req Request, eventCh chan<- Event) {
	maxIter := e.cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 10
	}

	// 动态创建带用户上下文的 knowledge_search 工具
	ksTool := e.knowledgeSearchFactory(req.UserID, req.KnowledgeBaseIDs)

	// 合并工具：registry(web_search, final_answer) + 动态 knowledge_search
	toolMap := e.registry.ToToolMap()
	toolMap[ksTool.Name()] = ksTool

	llmTools := e.registry.ToLLMTools()
	llmTools = append(llmTools, llm.Tool{
		Name:        ksTool.Name(),
		Description: ksTool.Description(),
		Parameters:  ksTool.Parameters(),
	})

	toolInfos := make([]toolInfo, 0, len(toolMap))
	for _, t := range toolMap {
		toolInfos = append(toolInfos, toolInfo{
			name:        t.Name(),
			description: t.Description(),
		})
	}

	// 构建消息列表（无预检索结果，LLM 自行决定何时调用工具）
	history := toHistoryMessages(req.History)
	systemPrompt := buildReActSystemPrompt(toolInfos)
	messages := []*schema.Message{
		schema.SystemMessage(systemPrompt),
		schema.UserMessage(buildUserMessage(req.Query, history)),
	}

	// 收集知识库检索来源（knowledge_search 返回的 sources）
	var collectedSources []response.SourceInfo

	// 主循环
	for iteration := 0; iteration < maxIter; iteration++ {
		if ctx.Err() != nil {
			eventCh <- Event{Type: EventError, Error: "请求已取消", Done: true}
			return
		}

		logger.Infof("Agent 第 %d 轮迭代", iteration+1)

		// ── Think ──
		thinkResult, err := e.think(ctx, req.LLMClient, messages, llmTools)
		if err != nil {
			logger.Errorf("Agent Think 阶段失败: %v", err)
			eventCh <- Event{Type: EventError, Error: "思考过程出错", Done: true}
			return
		}

		if thinkResult.content != "" {
			eventCh <- Event{Type: EventThought, Content: thinkResult.content}
		}

		// ── Analyze ──
		if len(thinkResult.toolCalls) == 0 {
			if len(collectedSources) > 0 {
				eventCh <- Event{Type: EventSources, Sources: collectedSources}
			}
			streamAnswer(eventCh, thinkResult.content)
			eventCh <- Event{Type: EventDone, Done: true}
			return
		}

		for _, tc := range thinkResult.toolCalls {
			if tc.Name == "final_answer" {
				answer := parseFinalAnswer(tc.Arguments)
				if len(collectedSources) > 0 {
					eventCh <- Event{Type: EventSources, Sources: collectedSources}
				}
				streamAnswer(eventCh, answer)
				eventCh <- Event{Type: EventDone, Done: true}
				return
			}
		}

		// ── Act ──
		toolResults := e.act(ctx, toolMap, thinkResult.toolCalls)
		for _, tr := range toolResults {
			eventCh <- Event{
				Type: EventToolResult,
				ToolResult: &ToolResult{
					Name:    tr.name,
					Content: truncate(tr.content, 500),
					Error:   tr.errMsg,
				},
			}

			// 从 knowledge_search 结果中提取来源
			if tr.name == "knowledge_search" && tr.errMsg == "" {
				sources := extractSourcesFromResult(tr.content)
				collectedSources = append(collectedSources, sources...)
			}
		}

		// ── Observe ──
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

	logger.Warnf("Agent 达到最大迭代次数 %d", maxIter)
	eventCh <- Event{Type: EventError, Error: "达到最大思考轮次，请尝试简化问题", Done: true}
}

// extractSourcesFromResult 从 knowledge_search 返回的 JSON 中提取来源信息
func extractSourcesFromResult(content string) []response.SourceInfo {
	var result tool.SearchResult
	if err := json.Unmarshal([]byte(content), &result); err != nil || len(result.Sources) == 0 {
		return nil
	}

	// 按文档分组
	docMap := make(map[string]*response.SourceInfo)
	docOrder := make([]string, 0)
	for _, src := range result.Sources {
		if _, exists := docMap[src.DocumentID]; !exists {
			docMap[src.DocumentID] = &response.SourceInfo{
				DocumentID:      src.DocumentID,
				KnowledgeBaseID: src.KnowledgeBaseID,
				Title:           src.Title,
			}
			docOrder = append(docOrder, src.DocumentID)
		}
		docMap[src.DocumentID].Chunks = append(docMap[src.DocumentID].Chunks, response.ChunkSource{
			Content: src.Content,
			Score:   src.Score,
		})
		if src.Score > docMap[src.DocumentID].Score {
			docMap[src.DocumentID].Score = src.Score
		}
	}

	sources := make([]response.SourceInfo, 0, len(docMap))
	for _, docID := range docOrder {
		sources = append(sources, *docMap[docID])
	}
	return sources
}

// thinkResult 思考结果
type thinkResult struct {
	content   string
	toolCalls []llm.ToolCall
}

// think 调用 LLM 思考
func (e *Engine) think(ctx context.Context, llmClient llm.Client, messages []*schema.Message, tools []llm.Tool) (thinkResult, error) {
	resp, err := llmClient.Generate(ctx, llm.GenerateRequest{
		Messages: messages,
		Tools:    tools,
	})
	if err != nil {
		return thinkResult{}, fmt.Errorf("LLM 调用失败: %w", err)
	}

	result := thinkResult{toolCalls: resp.ToolCalls}
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

// act 并发执行工具调用
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

// streamAnswer 将回答内容分片发送，实现流式输出效果
func streamAnswer(ch chan<- Event, content string) {
	const chunkSize = 20 // 每片字符数
	runes := []rune(content)
	for i := 0; i < len(runes); i += chunkSize {
		end := i + chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		ch <- Event{Type: EventAnswer, Content: string(runes[i:end])}
	}
}
