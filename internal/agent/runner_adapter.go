package agent

import (
	"context"
	"io"
	"strings"

	"github.com/cloudwego/eino/adk"
	einoTool "github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/tool"
	"solvify-agent/pkg/logger"
)

// runWithRunner 用 adk.Runner 执行 Agent，产出 AgentEvent 并转换到 eventCh。
// 首次执行调 runner.Run，带 ResumeData 时调 runner.ResumeWithParams。
func (e *Engine) runWithRunner(
	ctx context.Context,
	runner *adk.Runner,
	checkpointID string,
	inputMessages []*schema.Message,
	req Request,
	ksTool *tool.KnowledgeSearchTool,
	toolDescMap map[string]string,
	eventCh chan<- Event,
	tracker *agentStepTracker,
	taskID string,
) {
	var (
		iter *adk.AsyncIterator[*adk.AgentEvent]
		err  error
	)

	// ── 首次执行 vs 恢复执行 ──
	if len(req.ResumeData) > 0 {
		logger.Infof("[Agent] 恢复执行: checkpointID=%s, targets=%v", checkpointID, mapKeys(req.ResumeData))
		iter, err = runner.ResumeWithParams(ctx, checkpointID, &adk.ResumeParams{
			Targets: req.ResumeData,
		})
		if err != nil {
			logger.Errorf("[Agent] ResumeWithParams 失败: %v", err)
			eventCh <- Event{
				Type:      EventError,
				Title:     "恢复执行失败",
				Detail:    "无法从中断点恢复，请重新发起深度模式请求",
				Error:     err.Error(),
				Status:    "error",
				Retryable: false,
				Done:      true,
			}
			return
		}
	} else {
		iter = runner.Run(ctx, inputMessages, adk.WithCheckPointID(checkpointID))
	}

	var fullAnswer strings.Builder
	var interruptSent bool

	for {
		agentEvent, ok := iter.Next()
		if !ok {
			break
		}

		if agentEvent.Err != nil {
			if ctx.Err() != nil {
				logger.Infof("[Agent] Runner 迭代器因 ctx 取消结束")
				break
			}
			logger.Errorf("[Agent] Runner 事件错误: %v", agentEvent.Err)
			if isToolChoiceUnsupportedError(agentEvent.Err.Error()) {
				eventCh <- Event{
					Type:      EventError,
					Title:     "当前模型不支持工具调用",
					Detail:    "该模型不支持工具调用功能，无法使用联网搜索、天气查询等工具。建议切换到支持工具调用的模型（如通义千问、智谱清言、DeepSeek 等），或使用快速模式。",
					Error:     agentEvent.Err.Error(),
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
				Error:     agentEvent.Err.Error(),
				Status:    "error",
				Retryable: true,
				Done:      true,
			}
			return
		}

		// ── Interrupt 处理 ──
			if agentEvent.Action != nil && agentEvent.Action.Interrupted != nil {
				if !interruptSent {
					interruptSent = true
					ii := agentEvent.Action.Interrupted
					interruptCtx := ii.InterruptContexts
					var interruptID string
					var infoStr string
					if len(interruptCtx) > 0 {
						interruptID = interruptCtx[0].ID
						if s, ok := interruptCtx[0].Info.(string); ok {
							infoStr = s
						}
					}
					logger.Infof("[Agent] 执行中断: checkpointID=%s, interruptID=%s, info=%s", checkpointID, interruptID, truncateStr(infoStr, 200))

					infoType, infoData := parseInterruptInfo(infoStr)

					if infoType == "clarify" {
						eventCh <- Event{
							Type:           EventInterrupt,
							Title:          "需要澄清",
							Detail:         getString(infoData, "question"),
							Status:         "clarify",
							Error:          interruptID,
							CheckpointID:   checkpointID,
							InterruptID:    interruptID,
							IsClarify:      true,
							ClarifyQuestion: getString(infoData, "question"),
							ClarifyOptions:  getStringSlice(infoData, "options"),
							ClarifyContext:  getString(infoData, "context"),
							Done:           true,
						}
					} else {
						// danger 或未知类型 → 按审批处理
						message := getString(infoData, "message")
						if message == "" {
							message = formatInterruptInfo(infoStr)
						}
						eventCh <- Event{
							Type:          EventInterrupt,
							Title:         "需要人工确认",
							Detail:        truncateStr(message, 256),
							Status:        "interrupt",
							Error:         interruptID,
							CheckpointID:  checkpointID,
							InterruptID:   interruptID,
							InterruptInfo: infoData,
							Done:          true,
						}
					}
					return
				}
				continue
			}

		if agentEvent.Output == nil || agentEvent.Output.MessageOutput == nil {
			continue
		}

		mv := agentEvent.Output.MessageOutput

		// ── 流式：消费 MessageStream，逐 chunk 处理 ──
		if mv.IsStreaming && mv.MessageStream != nil {
			e.consumeMessageStream(ctx, mv, toolDescMap, &fullAnswer, eventCh)
			continue
		}

		// ── 非流式：直接拿 Message ──
		msg, err := mv.GetMessage()
		if err != nil || msg == nil {
			continue
		}

		e.handleMessage(ctx, msg, mv.Role, mv.ToolName, toolDescMap, &fullAnswer, eventCh)
	}

	// ── 兜底：没拿到 ToolCalls 也没拿到最终答案，但 KB 有结果 ──
	if strings.TrimSpace(fullAnswer.String()) == "" && ksTool != nil && len(ksTool.CollectedSources) > 0 {
		fallback := buildFallbackAnswer(ksTool.CollectedSources)
		fullAnswer.WriteString(fallback)
		eventCh <- Event{Type: EventAnswer, Content: fallback}
	}

	// ── 收集 Sources ──
	var sources []response.SourceInfo
	if ksTool != nil {
		sources = collectSources(ksTool.CollectedSources)
	}

	if strings.TrimSpace(fullAnswer.String()) != "" {
		eventCh <- Event{Type: EventThinking, Title: "正在生成答案", Status: "success"}
	}
	if len(sources) > 0 {
		eventCh <- Event{Type: EventSources, Sources: sources}
	}

	// observability
	if tracker != nil && taskID != "" && e.obs != nil {
		// Runner 已经在内部处理了完整的 step tracker，这里兜底留空即可
	}

	eventCh <- Event{
		Type:    EventDone,
		Content: fullAnswer.String(),
		Sources: sources,
	}
}

func (e *Engine) consumeMessageStream(
	ctx context.Context,
	mv *adk.TypedMessageVariant[*schema.Message],
	toolDescMap map[string]string,
	fullAnswer *strings.Builder,
	eventCh chan<- Event,
) {
	stream := mv.MessageStream
	defer stream.Close()

	var toolCallPending bool
	var toolCallName string

	for {
		msg, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			if ctx.Err() != nil {
				logger.Infof("[Agent] MessageStream 因 ctx 取消结束")
				return
			}
			logger.Errorf("[Agent] MessageStream 读取失败: %v", err)
			return
		}
		if msg == nil {
			continue
		}

		// Role=Tool 的流：发 tool_result
		if mv.Role == schema.Tool && msg.Role == schema.Assistant && toolCallPending {
			fullAnswer.WriteString(msg.Content)
			continue
		}

		// Role=Assistant 流
		if mv.Role == schema.Assistant {
			if len(msg.ToolCalls) > 0 {
				// 流式里 ToolCalls 可能分 chunk 到达，攒齐了再发 EventToolCall
				for _, tc := range msg.ToolCalls {
					if tc.Function.Name != "" {
						toolCallName = tc.Function.Name
						eventCh <- Event{
							Type:   EventToolCall,
							Title:  "调用工具",
							Detail: truncateStr(tc.Function.Arguments, 200),
							Status: "running",
						}
						toolCallPending = true
					}
				}
				if strings.TrimSpace(msg.Content) != "" {
					eventCh <- Event{
						Type:   EventThinking,
						Title:  "深度推理中",
						Detail: truncateStr(msg.Content, 200),
						Status: "running",
					}
				}
				continue
			}

			// 最终答案
			if msg.Content != "" {
				fullAnswer.WriteString(msg.Content)
				eventCh <- Event{Type: EventAnswer, Content: msg.Content}
			}
			continue
		}

		// Role=Tool 完整结果
		if mv.Role == schema.Tool && msg.Content != "" {
			title, detail, _ := formatToolEnd(toolCallName, &einoTool.CallbackOutput{Response: msg.Content}, toolDescMap)
			eventCh <- Event{
				Type:       EventToolResult,
				Title:      title,
				Detail:     detail,
				Status:     "success",
				ToolResult: msg.Content,
			}
			toolCallPending = false
		}
	}
}

func (e *Engine) handleMessage(
	ctx context.Context,
	msg *schema.Message,
	role schema.RoleType,
	toolName string,
	toolDescMap map[string]string,
	fullAnswer *strings.Builder,
	eventCh chan<- Event,
) {
	switch role {
	case schema.Assistant:
		if len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				if tc.Function.Name == "" {
					continue
				}
				title, detail := formatToolStart(tc.Function.Name, extractQueryFromArgs(tc.Function.Arguments), nil, toolDescMap)
				eventCh <- Event{
					Type:   EventToolCall,
					Title:  title,
					Detail: detail,
					Status: "running",
				}
			}
			if strings.TrimSpace(msg.Content) != "" {
				eventCh <- Event{
					Type:   EventThinking,
					Title:  "深度推理中",
					Detail: truncateStr(msg.Content, 200),
					Status: "running",
				}
			}
			return
		}
		if msg.Content != "" {
			fullAnswer.WriteString(msg.Content)
			eventCh <- Event{Type: EventAnswer, Content: msg.Content}
		}

	case schema.Tool:
		if msg.Content != "" {
			title, detail, _ := formatToolEnd(toolName, &einoTool.CallbackOutput{Response: msg.Content}, toolDescMap)
			eventCh <- Event{
				Type:       EventToolResult,
				Title:      title,
				Detail:     detail,
				Status:     "success",
				ToolResult: msg.Content,
			}
		}
	}
}

func formatInterruptInfo(info any) string {
	switch v := info.(type) {
	case string:
		return v
	case map[string]any:
		if msg, ok := v["message"].(string); ok && msg != "" {
			return msg
		}
		if req, ok := v["request"].(string); ok && req != "" {
			return req
		}
	}
	return "执行被中断，等待用户处理"
}

func mapKeys(m map[string]any) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// collectSources 从 KnowledgeSearchTool.CollectedSources 转换成 response.SourceInfo
func collectSources(sources []tool.SourceDocument) []response.SourceInfo {
	if len(sources) == 0 {
		return nil
	}
	type docInfo struct {
		documentID      string
		knowledgeBaseID string
		chunks          []response.ChunkSource
	}
	docMap := make(map[string]*docInfo)
	for _, src := range sources {
		if _, exists := docMap[src.Title]; !exists {
			docMap[src.Title] = &docInfo{
				documentID:      src.DocumentID,
				knowledgeBaseID: src.KnowledgeBaseID,
			}
		}
		docMap[src.Title].chunks = append(docMap[src.Title].chunks, response.ChunkSource{
			ID:      src.ID,
			Content: src.Content,
			Score:   src.Score,
		})
	}
	result := make([]response.SourceInfo, 0, len(docMap))
	for title, info := range docMap {
		result = append(result, response.SourceInfo{
			DocumentID:      info.documentID,
			KnowledgeBaseID: info.knowledgeBaseID,
			Title:           title,
			Chunks:          info.chunks,
		})
	}
	return result
}

// buildFallbackAnswer 当 Agent 没有产出最终答案但 KB 有命中时的兜底总结
func buildFallbackAnswer(sources []tool.SourceDocument) string {
	var sb strings.Builder
	sb.WriteString("## 知识库检索结果总结\n\n")
	sb.WriteString("根据当前检索到的内容，为您整理以下要点：\n\n")
	usedTitles := make(map[string]bool, len(sources))
	const maxTop = 5
	for i, src := range sources {
		if i >= maxTop {
			break
		}
		title := src.Title
		if title == "" {
			title = "未命名文档"
		}
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
			chunkID = "c" + string(rune('0'+i))
		}
		sb.WriteString("- ")
		sb.WriteString(title)
		sb.WriteString(" <kb doc=\"")
		sb.WriteString(title)
		sb.WriteString("\" chunk_id=\"")
		sb.WriteString(chunkID)
		sb.WriteString("\" />\n")
		if content != "" {
			sb.WriteString("  > ")
			sb.WriteString(content)
			sb.WriteString("\n\n")
		}
	}
	sb.WriteString("\n如需进一步分析请补充问题细节，或切换到快速模式获取更直接的回答。")
	return sb.String()
}



func getString(m map[string]any, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getStringSlice(m map[string]any, key string) []string {
	v, ok := m[key]
	if !ok {
		return nil
	}
	switch raw := v.(type) {
	case []string:
		return raw
	case []any:
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
