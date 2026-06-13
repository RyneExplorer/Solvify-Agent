package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
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
//   - Think:    LLM 非流式思考，参考计划和 Memory 决定下一步
//   - Analyze:  无工具调用 → 流式生成最终答案，结束
//   - Act:      执行工具（knowledge_search / web_search）
//   - Observe:  截断摘要写入历史，更新 Memory
func (e *Engine) reActLoop(ctx context.Context, req Request, eventCh chan<- Event) {
	maxIter := e.cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 5
	}

	// ── Planner ──
	history := toHistoryMessages(req.History)
	plan := e.plan(ctx, req.LLMClient, req.Query, history)
	if plan != nil {
		steps := make([]string, len(plan.Steps))
		copy(steps, plan.Steps)
		eventCh <- Event{Type: EventPlan, Title: plan.Goal, Detail: joinSteps(steps), Status: "running"}
	}

	// 初始化 Memory
	memory := &Memory{}

	// 动态创建带用户上下文的 knowledge_search 工具
	ksTool := e.knowledgeSearchFactory(req.UserID, req.KnowledgeBaseIDs)

	// 合并工具：registry(web_search) + 动态 knowledge_search
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

	// ── 执行计划首步（如有） ──
	// Planner 建议的第一步工具调用，提前执行并将结果注入消息历史
	// 这样 LLM 进入 Think 时已经能看到检索结果，避免重复搜索
	if plan != nil && plan.FirstAction != nil && plan.FirstAction.Query != "" {
		e.executeFirstAction(ctx, toolMap, plan.FirstAction, messages, &collectedSources, memory, eventCh)
	}

	// 主循环
	for iteration := 0; iteration < maxIter; iteration++ {
		if ctx.Err() != nil {
			eventCh <- Event{Type: EventError, Title: "请求已取消", Status: "error", Done: true}
			return
		}

		logger.Infof("Agent 第 %d 轮迭代", iteration+1)

		// ── Think ──
		thinkResult, err := e.think(ctx, req.LLMClient, messages, llmTools)
		if err != nil {
			logger.Errorf("Agent Think 阶段失败: %v", err)
			eventCh <- Event{Type: EventError, Title: "分析过程出错", Detail: "请稍后重试", Status: "error", Done: true}
			return
		}

		// ── Analyze ──
		if len(thinkResult.toolCalls) == 0 {
			// 无工具调用，说明是最终答案
			if len(collectedSources) > 0 {
				eventCh <- Event{Type: EventSources, Sources: collectedSources}
			}
			eventCh <- Event{Type: EventThinking, Title: "正在生成答案", Status: "running"}
			fullAnswer := generateAnswer(ctx, req.LLMClient, messages, eventCh)

			// 解析答案中的精确引用，填充到 sources
			if len(collectedSources) > 0 && fullAnswer != "" {
				quotes := parseQuotesFromAnswer(fullAnswer)
				applyQuotesToSources(collectedSources, quotes)
			}

			eventCh <- Event{Type: EventDone, Sources: collectedSources, Done: true}
			return
		}

		// ── Act ──
		// 发送工具开始事件（通过 ProgressReporter 接口）
		for _, tc := range thinkResult.toolCalls {
			if t, ok := toolMap[tc.Name]; ok {
				if pr, ok := t.(tool.ProgressReporter); ok {
					report := pr.StartReport(tc.Arguments)
					eventCh <- Event{Type: EventToolCall, Title: report.Title, Detail: report.Detail, Status: report.Status}
				}
			}
		}

		toolResults := e.act(ctx, toolMap, thinkResult.toolCalls)
		for _, tr := range toolResults {
			// 发送工具结果事件
			if t, ok := toolMap[tr.name]; ok {
				if pr, ok := t.(tool.ProgressReporter); ok {
					var execErr error
					if tr.errMsg != "" {
						execErr = fmt.Errorf("%s", tr.errMsg)
					}
					report := pr.ResultReport(tr.content, execErr)
					evtType := EventToolResult
					if report.Status == "warning" {
						evtType = EventWarning
					} else if report.Status == "error" {
						evtType = EventError
					}
					eventCh <- Event{Type: evtType, Title: report.Title, Detail: report.Detail, Status: report.Status}
				}
			}

			// 更新 Memory
			if tr.name == "knowledge_search" && tr.errMsg == "" {
				query := extractQueryFromToolCalls(thinkResult.toolCalls, tr.toolCallID)
				summary := buildToolResultSummary(tr)
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
	eventCh <- Event{Type: EventError, Title: "处理超时", Detail: "达到最大思考轮次，请尝试简化问题", Status: "error", Done: true}
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

// executeFirstAction 执行 Planner 建议的首步工具调用，将结果注入消息历史
func (e *Engine) executeFirstAction(ctx context.Context, toolMap map[string]tool.Tool, action *PlanAction, messages []*schema.Message, collectedSources *[]response.SourceInfo, memory *Memory, eventCh chan<- Event) {
	t, ok := toolMap[action.Tool]
	if !ok {
		logger.Warnf("Planner 建议的工具 %s 不存在，跳过首步", action.Tool)
		return
	}

	args := fmt.Sprintf(`{"query":"%s"}`, action.Query)

	// 发送开始事件
	if pr, ok := t.(tool.ProgressReporter); ok {
		report := pr.StartReport(args)
		eventCh <- Event{Type: EventToolCall, Title: report.Title, Detail: report.Detail, Status: report.Status}
	}

	content, err := t.Execute(ctx, args)

	// 发送结果事件
	if pr, ok := t.(tool.ProgressReporter); ok {
		var execErr error
		if err != nil {
			execErr = err
		}
		report := pr.ResultReport(content, execErr)
		evtType := EventToolResult
		if report.Status == "warning" {
			evtType = EventWarning
		} else if report.Status == "error" {
			evtType = EventError
		}
		eventCh <- Event{Type: evtType, Title: report.Title, Detail: report.Detail, Status: report.Status}
	}

	if err != nil {
		logger.Warnf("Planner 首步执行失败: %v", err)
		return
	}

	// 更新 Memory
	summary := buildToolResultSummary(actResult{name: action.Tool, content: content})
	memory.AddSearch(action.Query)
	memory.AddFinding(truncate(summary, 200))

	// 提取来源
	if action.Tool == "knowledge_search" {
		sources := extractSourcesFromResult(content)
		*collectedSources = append(*collectedSources, sources...)
	}

	// 将首步结果注入消息历史，LLM 进入 Think 时可直接参考
	truncated := truncate(content, 500)
	callID := "plan_first_action"
	messages = append(messages, &schema.Message{
		Role:      schema.Assistant,
		Content:   "",
		ToolCalls: []schema.ToolCall{{ID: callID, Type: "function", Function: schema.FunctionCall{Name: action.Tool, Arguments: args}}},
	})
	messages = append(messages, &schema.Message{
		Role:       schema.Tool,
		Content:    truncated,
		ToolCallID: callID,
	})

	logger.Infof("Planner 首步执行完成: tool=%s, query=%s", action.Tool, action.Query)
}

// thinkResult 思考结果
type thinkResult struct {
	content   string
	toolCalls []llm.ToolCall
}

// think 调用 LLM 思考，返回完整响应
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

// generateAnswer 调用 LLM 流式生成最终答案，逐 token 推送，返回完整答案文本
func generateAnswer(ctx context.Context, llmClient llm.Client, messages []*schema.Message, eventCh chan<- Event) string {
	var fullAnswer string

	stream, err := llmClient.GenerateStream(ctx, llm.GenerateRequest{Messages: messages})
	if err != nil {
		logger.Errorf("流式生成答案失败: %v", err)
		eventCh <- Event{Type: EventError, Title: "生成答案失败", Detail: "请稍后重试", Status: "error", Done: true}
		return ""
	}

	for chunk := range stream {
		if chunk.Error != nil {
			if ctx.Err() != nil {
				return fullAnswer
			}
			logger.Errorf("流式生成错误: %v", chunk.Error)
			eventCh <- Event{Type: EventError, Title: "生成答案出错", Detail: chunk.Error.Error(), Status: "error", Done: true}
			return fullAnswer
		}
		if chunk.Done {
			break
		}
		if chunk.Content != "" {
			fullAnswer += chunk.Content
			eventCh <- Event{Type: EventAnswer, Content: chunk.Content}
		}
	}

	return fullAnswer
}

// quoteInfo 解析出的引用信息
type quoteInfo struct {
	Title string // 文档标题
	Quote string // 引用的原文
}

// parseQuotesFromAnswer 从答案文本中解析出所有引用
// 格式：[文档标题]{引用原文}
var quoteRegex = regexp.MustCompile(`\[([^\]]+)\]\{([^}]+)\}`)

func parseQuotesFromAnswer(answer string) []quoteInfo {
	matches := quoteRegex.FindAllStringSubmatch(answer, -1)
	if len(matches) == 0 {
		return nil
	}

	quotes := make([]quoteInfo, 0, len(matches))
	for _, match := range matches {
		if len(match) >= 3 {
			quotes = append(quotes, quoteInfo{
				Title: match[1],
				Quote: match[2],
			})
		}
	}
	return quotes
}

// applyQuotesToSources 将解析出的引用应用到对应的 sources 中
func applyQuotesToSources(sources []response.SourceInfo, quotes []quoteInfo) {
	if len(quotes) == 0 {
		return
	}

	// 构建 title -> quote 列表的映射
	quoteMap := make(map[string][]string)
	for _, q := range quotes {
		quoteMap[q.Title] = append(quoteMap[q.Title], q.Quote)
	}

	// 遍历 sources，将 quote 填充到第一个匹配的 chunk 中
	for i := range sources {
		title := sources[i].Title
		quotesForTitle, exists := quoteMap[title]
		if !exists || len(quotesForTitle) == 0 {
			continue
		}

		// 将 quotes 分配给 chunks（每个 quote 对应一个 chunk）
		for j := 0; j < len(sources[i].Chunks) && j < len(quotesForTitle); j++ {
			sources[i].Chunks[j].Quote = quotesForTitle[j]
		}
	}
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

			args := call.Arguments
			if args == "" {
				args = "{}"
			}
			logger.Infof("执行工具: %s, 参数: %s", call.Name, truncate(args, 100))
			content, err := t.Execute(ctx, args)
			if err != nil {
				// 自动重试一次
				logger.Warnf("工具 %s 首次执行失败: %v，重试中...", call.Name, err)
				content, err = t.Execute(ctx, args)
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
