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
// 流程：Planner → Think → Analyze → Act → Observe，最多 maxIter 轮
//   - Planner:  独立 LLM 调用生成执行计划
//   - Think:    LLM 思考，参考计划和 Memory 决定下一步
//   - Analyze:  final_answer → 结束；无工具 → 直接输出
//   - Act:      执行工具（knowledge_search / web_search）
//   - Observe:  截断摘要写入历史，更新 Memory
func (e *Engine) reActLoop(ctx context.Context, req Request, eventCh chan<- Event) {
	maxIter := e.cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 5
	}

	// ── Planner ──
	eventCh <- Event{Type: EventStatus, Content: "正在分析问题"}
	history := toHistoryMessages(req.History)
	plan := e.plan(ctx, req.LLMClient, req.Query, history)

	// 初始化 Memory
	memory := &Memory{}

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

	// 构建消息列表（注入计划和 Memory）
	systemPrompt := buildReActSystemPrompt(toolInfos, plan, memory)
	messages := []*schema.Message{
		schema.SystemMessage(systemPrompt),
		schema.UserMessage(buildUserMessage(req.Query, history)),
	}

	// 收集知识库检索来源
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

		// ── Analyze ──
		if len(thinkResult.toolCalls) == 0 {
			// 无工具调用，直接输出内容
			if len(collectedSources) > 0 {
				eventCh <- Event{Type: EventSources, Sources: collectedSources}
			}
			eventCh <- Event{Type: EventStatus, Content: "正在生成最终答案"}
			streamAnswer(eventCh, thinkResult.content)
			eventCh <- Event{Type: EventDone, Done: true}
			return
		}

		for _, tc := range thinkResult.toolCalls {
			if tc.Name == "final_answer" {
				answer, confidence := parseFinalAnswerWithConfidence(tc.Arguments)
				logger.Infof("Agent final_answer confidence=%.2f", confidence)
				if len(collectedSources) > 0 {
					eventCh <- Event{Type: EventSources, Sources: collectedSources}
				}
				eventCh <- Event{Type: EventStatus, Content: "正在生成最终答案"}
				streamAnswer(eventCh, answer)
				eventCh <- Event{Type: EventDone, Done: true}
				return
			}
		}

		// ── Act ──
		// 发送 Status 事件（推理摘要）
		for _, tc := range thinkResult.toolCalls {
			if tc.Name == "knowledge_search" {
				query := extractQueryFromArgs(tc.Arguments)
				eventCh <- Event{Type: EventStatus, Content: fmt.Sprintf("正在查询：%s", query)}
			}
		}
		// 发送 tool_call 事件（详细信息）
		eventCh <- Event{
			Type:      EventToolCall,
			ToolCalls: thinkResult.toolCalls,
		}

		toolResults := e.act(ctx, toolMap, thinkResult.toolCalls)
		for _, tr := range toolResults {
			// 发送工具结果摘要
			summary := buildToolResultSummary(tr)
			eventCh <- Event{
				Type: EventToolResult,
				ToolResult: &ToolResult{
					Name:    tr.name,
					Content: summary,
					Error:   tr.errMsg,
				},
			}

			// 更新 Memory
			if tr.name == "knowledge_search" && tr.errMsg == "" {
				query := extractQueryFromToolCalls(thinkResult.toolCalls, tr.toolCallID)
				memory.AddSearch(query)
				memory.AddFinding(truncate(summary, 200))

				// 提取来源
				sources := extractSourcesFromResult(tr.content)
				collectedSources = append(collectedSources, sources...)
			}
		}

		// ── Observe ──
		// 将工具调用结果写入消息历史（截断摘要）
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
			content := truncate(tr.content, 500)
			if tr.errMsg != "" {
				content = fmt.Sprintf("工具执行失败: %s", tr.errMsg)
			}
			messages = append(messages, &schema.Message{
				Role:       schema.Tool,
				Content:    content,
				ToolCallID: tr.toolCallID,
			})
		}

		// 更新 system prompt（注入最新 Memory）
		if memSummary := memory.Summary(); memSummary != "" {
			messages[0] = schema.SystemMessage(buildReActSystemPrompt(toolInfos, plan, memory))
		}
	}

	logger.Warnf("Agent 达到最大迭代次数 %d", maxIter)
	eventCh <- Event{Type: EventError, Error: "达到最大思考轮次，请尝试简化问题", Done: true}
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

// extractQueryFromToolCalls 从工具调用列表中找到指定 toolCallID 的 query
func extractQueryFromToolCalls(toolCalls []llm.ToolCall, toolCallID string) string {
	for _, tc := range toolCalls {
		if tc.ID == toolCallID {
			return extractQueryFromArgs(tc.Arguments)
		}
	}
	return ""
}

// buildToolResultSummary 构建工具结果摘要
func buildToolResultSummary(tr actResult) string {
	if tr.errMsg != "" {
		return fmt.Sprintf("工具执行失败: %s", tr.errMsg)
	}
	if tr.name == "knowledge_search" {
		var result tool.SearchResult
		if err := json.Unmarshal([]byte(tr.content), &result); err == nil {
			return fmt.Sprintf("找到 %d 条相关资料", len(result.Sources))
		}
	}
	return truncate(tr.content, 200)
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

// act 并发执行工具调用（失败自动重试一次）
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
				// 自动重试一次
				logger.Warnf("工具 %s 首次执行失败: %v，重试中...", call.Name, err)
				content, err = t.Execute(ctx, call.Arguments)
				if err != nil {
					logger.Errorf("工具 %s 重试仍然失败: %v", call.Name, err)
					results[idx] = actResult{
						toolCallID: call.ID,
						name:       call.Name,
						errMsg:     err.Error(),
					}
					return
				}
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
