package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/google/uuid"
	"gorm.io/datatypes"

	"solvify-agent/internal/agent"
	"solvify-agent/internal/llm"
	requestdto "solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/observability"
	"solvify-agent/pkg/eventch"
	"solvify-agent/pkg/logger"
)

// ─── 深度思考模式 ───────────────────────────────────────────

// processDeepMode 深度思考模式处理流程
// 使用 eino ReAct Agent，自动管理 Think → Act → Observe 循环
func (s *chatService) processDeepMode(ctx context.Context, userID, sessionID, userMsgID string, req requestdto.SendMessageRequest, eventCh chan<- dto.StreamEvent) {
	var span *observability.Span
	if s.obs != nil {
		ctx, span = s.obs.StartSpan(ctx, "chat.deep", observability.ComponentAgentEngine, observability.Attrs{
			"session_id":  sessionID,
			"user_id":     userID,
			"model_id":    req.ModelID,
			"search_mode": "deep",
		})
	}
	defer func() {
		status := observability.SpanStatusOK
		var errVal error
		if r := recover(); r != nil {
			status = observability.SpanStatusError
			errVal = fmt.Errorf("panic: %v", r)
			obsMarkError(ctx, s.obs, errVal)
			eventch.Send(ctx, eventCh, dto.StreamEvent{Type: "error", Detail: "处理过程中发生未预期错误", Done: true})
		}
		obsEndSpan(ctx, s.obs, span, status, errVal, nil)
	}()
	obsIncr(ctx, s.obs, "chat_deep_requests_total", map[string]string{"model_id": req.ModelID}, 1)

	assistantMsgID := uuid.New().String()
	obsAddRootAttrs(ctx, s.obs, observability.Attrs{"assistant_message_id": assistantMsgID})

	// P0-④ 关键修复：在 initContext 之前，先把"将发送给模型的工具定义"预构建并按真 BPE 算总 token，
	// 之后把 toolsTokens 传给 initContext，calculateContextBudgets 会先从 maxCtx 扣除，
	// 避免工具定义悄悄吃掉历史/检索预算，导致最终请求超上下文长度。
	sendProgressEvent(ctx, eventCh, "正在加载上下文...")
	t0 := time.Now()
	preToolsTokens := 0
	deepCtx := ctx
	if s.agentEngine != nil {
		var tErr error
		// 先用"仅创建客户端拿到 modelName"解析出 ModelName，再交给 PrebuildTools 估算工具 token。
		client, cErr := s.resolveClient(ctx, userID, req.ModelID, req.ModelType)
		if cErr == nil {
			modelName := client.ModelName()
			obsAddRootAttrs(ctx, s.obs, observability.Attrs{"model_name": modelName})
			preToolsTokens, deepCtx, tErr = s.agentEngine.EstimateToolsTokens(ctx, userID, req.KnowledgeBaseIDs, modelName, req.MCPUserConfigIDs...)
			if tErr != nil {
				logger.Warnf("预估算工具定义 token 失败，按 0 处理: %v", tErr)
				preToolsTokens = 0
				deepCtx = ctx
			}
		}
	}

	client, enhancedCtx, err := s.initContext(ctx, userID, sessionID, req.ModelID, req.ModelType, req.Content, preToolsTokens)
	if err != nil {
		obsIncr(ctx, s.obs, "chat_deep_errors_total", map[string]string{"stage": "init_ctx"}, 1)
		obsMarkError(ctx, s.obs, err)
		sendErrorEvent(ctx, eventCh, err, err.Error())
		return
	}
	history := excludeByMessageID(enhancedCtx.History, userMsgID)
	chatModel := client.ChatModel()
	modelName := client.ModelName()
	obsAddRootAttrs(ctx, s.obs, observability.Attrs{
		"model_name":       modelName,
		"tools_tokens":     fmt.Sprintf("%d", preToolsTokens),
		"history_budget":   fmt.Sprintf("%d", enhancedCtx.HistoryBudget),
		"retrieval_budget": fmt.Sprintf("%d", enhancedCtx.RetrievalBudget),
	})
	obsObserve(ctx, s.obs, "chat_deep_init_ctx_seconds", map[string]string{"model_id": req.ModelID}, time.Since(t0).Seconds())

	eventch.Send(ctx, eventCh, dto.StreamEvent{Type: "start", MessageID: assistantMsgID})

	sendProgressEvent(ctx, eventCh, "正在深度推理...")
	agentPB := NewPromptBuilder(PromptModeDeep, "", enhancedCtx.Summary, enhancedCtx.Memories, enhancedCtx.UserCtx).
		WithProfile(enhancedCtx.Profile).
		WithPreference(enhancedCtx.Preference)
	agentReq := agentPB.BuildAgentRequestFields(userID, req.Content, req.ModelID, req.ModelType, req.KnowledgeBaseIDs, req.MCPUserConfigIDs, history)
	agentReq.SessionID = sessionID

	// ── 恢复流程：session 有待恢复 checkpoint 且用户带了审批结果 ──
	session2, _ := s.sessionRepo.FindByID(ctx, sessionID)
	if session2 != nil && session2.HasPendingCheckpoint() {
		pc, _ := session2.GetPendingCheckpoint()
		if pc != nil {
			// 超时判断：clarify 走 10min（ClarifyDefaultTimeout），danger/其它走 24h（CheckpointDefaultTimeout）。
			// 超时说明用户长时间未处理，checkpoint 字节可能已被后台 Ticker 清理，继续恢复无意义，直接放弃并清除。
			timeout := entity.CheckpointDefaultTimeout
			if pc.IsClarify {
				timeout = entity.ClarifyDefaultTimeout
			}
			if pc.IsExpired(timeout) {
				logger.Warnf("[ChatService] pending checkpoint 已超时（%v），放弃恢复并清除: checkpointID=%s", timeout, pc.CheckpointID)
				_ = s.sessionRepo.ClearPendingCheckpoint(ctx, sessionID)
				_ = s.sessionRepo.ClearPendingClarify(ctx, sessionID)
			} else {
				logger.Infof("[ChatService] 检测到 pending checkpoint: checkpointID=%s, interruptID=%s", pc.CheckpointID, pc.InterruptID)
				if req.Content != "" {
					agentReq.CheckpointID = pc.CheckpointID
					if pc.IsClarify {
						// clarify：恢复数据为用户回答（字符串），与官方 FollowUpTool 的 UserAnswer 语义一致
						agentReq.ResumeData = map[string]any{
							pc.InterruptID: req.Content,
						}
					} else {
						// danger：恢复数据为结构化 ApprovalResult，对齐官方 approval_wrapper.go 的 *ApprovalResult 数据契约
						agentReq.ResumeData = map[string]any{
							pc.InterruptID: parseApprovalResult(req.Content),
						}
					}
					logger.Infof("[ChatService] 设置恢复参数: checkpointID=%s, resumeKeys=%v", pc.CheckpointID, []string{pc.InterruptID})
				} else {
					logger.Warnf("[ChatService] 有 pending checkpoint 但用户未提供审批内容，走首次执行")
					_ = s.sessionRepo.ClearPendingCheckpoint(ctx, sessionID)
					_ = s.sessionRepo.ClearPendingClarify(ctx, sessionID)
				}
			}
		}
	}
	t1 := time.Now()
	agentEventCh, err := s.agentEngine.Execute(deepCtx, agentReq, chatModel)
	if err != nil {
		obsIncr(ctx, s.obs, "chat_deep_errors_total", map[string]string{"stage": "agent_execute"}, 1)
		obsMarkError(ctx, s.obs, err)
		logger.Errorf("Agent 执行失败, sessionID=%s: %v", sessionID, err)
		llm.ReduceContextBudgetOnError(req.ModelID, err)
		sendErrorEvent(ctx, eventCh, err, "Agent 执行失败")
		return
	}

	var fullContent string
	var agentSources []dto.SourceInfo
	var reasoningSteps []dto.ReasoningStep
	toolCallsN := 0
	toolErrorsN := 0
	toolEventSeen := false
	agentErrorSeen := false

	for agentEvent := range agentEventCh {
		if agentEvent.Type == agent.EventDone {
			if agentEvent.Content != "" {
				fullContent = agentEvent.Content
			}
			if len(agentEvent.Sources) > 0 {
				agentSources = agentEvent.Sources
			}
			continue
		}

		// ── interrupt 事件：存 checkpoint 到 session，返回中断事件给前端 ──
		if agentEvent.Type == agent.EventInterrupt {
			logger.Infof("[ChatService] Agent 中断: checkpointID=%s, interruptID=%s, isClarify=%v", agentEvent.CheckpointID, agentEvent.InterruptID, agentEvent.IsClarify)
			if agentEvent.CheckpointID != "" {
				pcData := &entity.PendingCheckpointData{
					CheckpointID: agentEvent.CheckpointID,
					InterruptID:  agentEvent.InterruptID,
					Question:     agentEvent.Detail,
					IsClarify:    agentEvent.IsClarify,
					Options:      agentEvent.ClarifyOptions,
					SetAt:        time.Now(),
				}
				raw, _ := json.Marshal(pcData)
				if sErr := s.sessionRepo.SetPendingCheckpoint(ctx, sessionID, raw); sErr != nil {
					logger.Errorf("存储 pending checkpoint 失败: %v", sErr)
				} else {
					logger.Infof("[ChatService] 已存储 pending checkpoint: sessionID=%s", sessionID)
				}

				// clarify 中断额外存到 pending_clarify（兼容旧恢复流程）
				if agentEvent.IsClarify {
					clarifyData := &entity.PendingClarifyData{
						Question: agentEvent.ClarifyQuestion,
						Options:  agentEvent.ClarifyOptions,
						SetAt:    time.Now(),
					}
					clarifyRaw, _ := json.Marshal(clarifyData)
					if sErr := s.sessionRepo.SetPendingClarify(ctx, sessionID, clarifyRaw); sErr != nil {
						logger.Errorf("存储 pending clarify 失败: %v", sErr)
					}
				}
			}
			eventch.Send(ctx, eventCh, toStreamEvent(agentEvent))
			return
		}

		if agentEvent.Type == agent.EventAnswer {
			fullContent += agentEvent.Content
		}
		if agentEvent.Type == agent.EventToolCall {
			toolEventSeen = true
			toolCallsN++
		}
		if agentEvent.Type == agent.EventToolResult {
			toolEventSeen = true
			if agentEvent.Status == "error" {
				toolErrorsN++
			}
		}
		if agentEvent.Type == agent.EventError {
			agentErrorSeen = true
			llm.ReduceContextBudgetOnError(req.ModelID, fmt.Errorf("%s", agentEvent.Error))
		}

		eventch.Send(ctx, eventCh, toStreamEvent(agentEvent))

		if len(agentEvent.Sources) > 0 {
			agentSources = agentEvent.Sources
		}
		applyReasoningStep(&reasoningSteps, agentEvent)
	}

	// ── 执行成功后清除 pending 状态 ──
	if agentReq.CheckpointID != "" {
		if cErr := s.sessionRepo.ClearPendingCheckpoint(ctx, sessionID); cErr != nil {
			logger.Warnf("清除 pending checkpoint 失败: %v", cErr)
		} else {
			logger.Infof("[ChatService] 恢复执行完成，已清除 pending checkpoint: sessionID=%s", sessionID)
		}
		_ = s.sessionRepo.ClearPendingClarify(ctx, sessionID)
	}
	obsObserve(ctx, s.obs, "chat_deep_agent_seconds", map[string]string{"model_id": req.ModelID}, time.Since(t1).Seconds())
	obsIncr(ctx, s.obs, "agent_runs_total", map[string]string{
		"error_seen": fmt.Sprintf("%t", agentErrorSeen),
		"tool_calls": fmt.Sprintf("%d", toolCallsN),
	}, 1)
	obsAddRootAttrs(ctx, s.obs, observability.Attrs{
		"tool_calls":      toolCallsN,
		"tool_errors":     toolErrorsN,
		"steps_n":         len(reasoningSteps),
		"rag_docs_n":      len(agentSources),
		"agent_error":     agentErrorSeen,
		"tool_used":       toolEventSeen,
		"assistant_chars": len([]rune(fullContent)),
	})

	if agentErrorSeen {
		obsMarkError(ctx, s.obs, fmt.Errorf("agent 执行过程中发生错误"))
		return
	}
	if !toolEventSeen && looksLikeExecutionPlan(fullContent) {
		logger.Warnf("深度模式未产生工具调用，仅返回执行计划，sessionID=%s, content=%q", sessionID, fullContent)
		obsIncr(ctx, s.obs, "agent_plan_without_tool_total", nil, 1)
		obsMarkError(ctx, s.obs, fmt.Errorf("深度模式未产生工具调用"))
		eventch.Send(ctx, eventCh, dto.StreamEvent{
			Type:      "error",
			Title:     "深度推理未完成",
			Detail:    "当前模型没有正确发起工具调用，请重试或切换支持工具调用的模型",
			Retryable: true,
			Done:      true,
		})
		return
	}
	// 空回答守卫（与快速模式共用 rejectEmptyAnswer，口径一致）。
	//
	// 旧实现这里是裸 return：不落库，但也不发任何终态事件，SSE 流就此断掉，
	// 前端只收到 start 和中间事件、永远等不到 done 或 error，只能一直停在「正在生成」。
	// 更糟的是原条件里的 len(reasoningSteps) == 0 —— 「有推理步骤但没正文」的空回答
	// 会直接穿过该分支，被下面的 emitDoneAndSave 当成功收尾：用户看到空白气泡，
	// 且空 assistant 消息进入后续 history（部分厂商对空 content 直接 400），
	// 一次空回答污染该会话之后每一轮。
	if rejectEmptyAnswer(ctx, eventCh, s.obs, "deep", sessionID, req.ModelID, assistantMsgID,
		fullContent, fmt.Sprintf("sources=%d, steps=%d", len(agentSources), len(reasoningSteps))) {
		return
	}

	var metadata datatypes.JSON
	metaMap := map[string]any{}
	if len(reasoningSteps) > 0 {
		metaMap["reasoning_steps"] = reasoningSteps
	}
	if s.obs != nil {
		metaMap["trace_id"] = observability.TraceIDFromContext(ctx)
	}
	if len(metaMap) > 0 {
		metadata = datatypes.JSON(mustMarshal(metaMap))
	}
	s.emitDoneAndSave(ctx, eventCh, sessionID, assistantMsgID, fullContent, req, agentSources, metadata, nil)

	s.refreshContextAsync(ctx, userID, sessionID, enhancedCtx.History, chatModel)
}

// refreshContextAsync 异步更新会话摘要和提取用户记忆
//
// 关键修复（P1-① 幂等+重试）：
//   - 旧实现 fire-and-forget，数据库抖动就把摘要写丢，下次 BuildContext 拿 existing=nil
//     → 历史越跑越长真的爆窗口。现在 SummarizeSession / ExtractMemories 各自独立 3 次指数退避。
//   - 任何一步失败都记 Obs 指标（summary_refresh_errors_total / memory_extract_errors_total），
//     后面上线可从 /metrics 直接看成功率。
//   - trace 连续性：后台任务用 DetachedTraceContext 派生上下文，而不是 context.Background()。
//     前者只切断取消信号、保留 SpanContext，两个后台 span 因而仍挂在本次请求的同一条 trace 下；
//     后者会让它们成为独立根 trace（三方平台上的「孤儿 trace」）。
func (s *chatService) refreshContextAsync(ctx context.Context, userID, sessionID string, history []entity.ChatMessage, chatModel model.BaseChatModel) {
	if s == nil || s.contextSvc == nil {
		return
	}
	obs := s.obs

	// 后台刷新要脱离请求 ctx（响应已返回，不能被请求取消掐断），
	// 但不能连 trace 上下文一起丢 —— 直接用 context.Background() 会让
	// ctx.summarize / ctx.extract_memories 失去父节点、各自成为独立根 trace，
	// 在三方平台上表现为「孤儿 trace」且拿不到 user/session 归属。
	// DetachedTraceContext 只搬 SpanContext、不搬取消信号，正好是这里要的语义。
	// 在 goroutine 外先取一次，避免与父 span 结束产生竞态。
	baseCtx := observability.DetachedTraceContext(ctx)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf("refreshContextAsync panic 已恢复: sessionID=%s, err=%v", sessionID, r)
				obsIncr(context.Background(), obs, "ctx_refresh_panic_total", nil, 1)
			}
		}()

		obsIncr(context.Background(), obs, "ctx_refresh_requests_total", nil, 1)

		summaryOK := true
		if err := runWithRetry(3, "ctx.summary", func(attempt int) error {
			refreshCtx, cancel := context.WithTimeout(baseCtx, 45*time.Second+time.Duration(attempt*15)*time.Second)
			defer cancel()
			_, err := s.contextSvc.SummarizeSession(refreshCtx, sessionID, chatModel)
			return err
		}); err != nil {
			summaryOK = false
			logger.Warnf("生成会话摘要失败（已重试 3 次）: sessionID=%s, err=%v", sessionID, err)
			obsIncr(context.Background(), obs, "ctx_summary_refresh_errors_total", nil, 1)
		}

		memoryOK := true
		if err := runWithRetry(3, "ctx.memory", func(attempt int) error {
			refreshCtx, cancel := context.WithTimeout(baseCtx, 45*time.Second+time.Duration(attempt*15)*time.Second)
			defer cancel()
			_, err := s.contextSvc.ExtractMemories(refreshCtx, userID, sessionID, history, chatModel)
			return err
		}); err != nil {
			memoryOK = false
			logger.Warnf("提取用户记忆失败（已重试 3 次）: sessionID=%s, err=%v", sessionID, err)
			obsIncr(context.Background(), obs, "ctx_memory_extract_errors_total", nil, 1)
		}

		labels := map[string]string{
			"summary_ok": ctxBoolLabel(summaryOK),
			"memory_ok":  ctxBoolLabel(memoryOK),
		}
		obsIncr(context.Background(), obs, "ctx_refresh_runs_total", labels, 1)
	}()
}

// runWithRetry 做 1..maxAttempts 次指数退避重试。
// fn 每次调用 attempt 从 1 开始，成功即返回。
func runWithRetry(maxAttempts int, tag string, fn func(attempt int) error) error {
	if maxAttempts <= 0 {
		maxAttempts = 1
	}
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		if err := fn(attempt); err != nil {
			lastErr = err
			if attempt < maxAttempts {
				backoff := time.Duration(1<<uint(attempt-1)) * 500 * time.Millisecond
				if backoff > 4*time.Second {
					backoff = 4 * time.Second
				}
				logger.Warnf("%s 第 %d 次失败，%v 后重试: err=%v", tag, attempt, backoff, err)
				time.Sleep(backoff)
			}
			continue
		}
		return nil
	}
	return lastErr
}

func ctxBoolLabel(b bool) string {
	if b {
		return "ok"
	}
	return "fail"
}

// ─── 共享辅助方法 ───────────────────────────────────────────

// emitDoneAndSave 发送 done 事件并异步保存助手消息。
// ctx 已取消（客户端断连）时 done 事件被丢弃、不阻塞（见 pkg/eventch）；落库仍走独立超时 ctx，不受影响。
// 注意：保存失败只记日志，禁止再向 eventCh 写事件（外层 defer close 后会 panic）
func (s *chatService) emitDoneAndSave(ctx context.Context, eventCh chan<- dto.StreamEvent, sessionID, msgID, content string, req requestdto.SendMessageRequest, sources []dto.SourceInfo, metadata datatypes.JSON, metaHook func(map[string]any)) {
	finalMeta := metadata
	if metaHook != nil && len(metadata) == 0 {
		m := map[string]any{}
		metaHook(m)
		if len(m) > 0 {
			finalMeta = datatypes.JSON(mustMarshal(m))
		}
	} else if metaHook != nil && len(metadata) > 0 {
		var m map[string]any
		if err := json.Unmarshal(metadata, &m); err == nil {
			metaHook(m)
			finalMeta = datatypes.JSON(mustMarshal(m))
		}
	}
	eventch.Send(ctx, eventCh, dto.StreamEvent{Type: "done", MessageID: msgID, Content: content, Sources: sources, Done: true})
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf("保存助手消息 panic 已恢复: messageID=%s, err=%v", msgID, r)
			}
		}()
		saveCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.saveAssistantMessage(saveCtx, sessionID, msgID, content, req, sources, finalMeta); err != nil {
			logger.Errorf("保存助手消息失败, messageID=%s: %v", msgID, err)
		}
	}()
}

// providerLabel 返回 LLM 客户端的可观测性标签
func providerLabel(client *llm.OpenAIClient) string {
	if client == nil {
		return "unknown"
	}
	return "openai_compatible"
}

// excludeByMessageID 按消息 ID 剔除本轮刚落库的 user 消息
// SendMessage 会先落库 user 消息，FindRecent/FindBySessionID 会把它带回 history
// 用 ID 精确匹配可以避免连续两次问相同问题时误删上一轮对话
func excludeByMessageID(history []entity.ChatMessage, excludeID string) []entity.ChatMessage {
	if excludeID == "" || len(history) == 0 {
		return history
	}
	result := make([]entity.ChatMessage, 0, len(history))
	for _, m := range history {
		if m.ID == excludeID {
			continue
		}
		result = append(result, m)
	}
	return result
}

// toStreamEvent 将 Agent 事件转换为 SSE 流式事件
func toStreamEvent(e agent.Event) dto.StreamEvent {
	se := dto.StreamEvent{
		Type:      e.Type,
		Title:     e.Title,
		Detail:    e.Detail,
		Status:    e.Status,
		Content:   e.Content,
		Error:     e.Error,
		Done:      e.Done,
		Retryable: e.Retryable,
	}
	if len(e.Sources) > 0 {
		se.Sources = e.Sources
	}
	// citation 事件的字段映射
	if e.Type == agent.EventCitation {
		se.CitationID = e.CitationID
		se.CitationChunkID = e.CitationChunkID
		se.CitationFileName = e.CitationFileName
		se.CitationContent = e.CitationContent
	}
	// interrupt 事件：携带 checkpointID / interruptID / info
	if e.Type == agent.EventInterrupt {
		se.CheckpointID = e.CheckpointID
		se.InterruptID = e.InterruptID
		se.InterruptInfo = e.InterruptInfo
		if e.IsClarify {
			se.Clarify = &dto.ClarifyPayload{
				Question: e.ClarifyQuestion,
				Options:  e.ClarifyOptions,
			}
		}
	}
	return se
}

// looksLikeExecutionPlan 判断内容是否只是执行计划而没有真正调用工具
func looksLikeExecutionPlan(content string) bool {
	text := strings.TrimSpace(content)
	if text == "" {
		return false
	}
	if len([]rune(text)) > 120 {
		return false
	}
	planPrefixes := []string{"我会先", "我将先", "我先", "先查一下", "我会查", "我将查"}
	for _, prefix := range planPrefixes {
		if strings.HasPrefix(text, prefix) {
			return true
		}
	}
	return false
}

// applyReasoningStep 从 Agent 事件更新推理步骤列表（用于持久化）
//
// thinking (running) → 追加新步骤
// thinking (success) → 将上一个 matching 步骤标记为 success（不再追加新条目）
// plan / tool_call / tool_result / warning → 直接追加
func applyReasoningStep(steps *[]dto.ReasoningStep, e agent.Event) {
	switch e.Type {
	case agent.EventThinking:
		if e.Status == "success" {
			// 反向查找最后一条 thinking running 步骤，将其标记为 success
			for i := len(*steps) - 1; i >= 0; i-- {
				if (*steps)[i].Type == agent.EventThinking && (*steps)[i].Status == "running" {
					(*steps)[i].Status = "success"
					break
				}
			}
			return
		}
		*steps = append(*steps, dto.ReasoningStep{
			Type:    e.Type,
			Content: e.Title,
			Detail:  e.Detail,
			Status:  e.Status,
		})
	case agent.EventToolCall, agent.EventToolResult, agent.EventWarning:
		*steps = append(*steps, dto.ReasoningStep{
			Type:    e.Type,
			Content: e.Title,
			Detail:  e.Detail,
			Status:  e.Status,
		})
	}
}

// parseApprovalResult 将前端返回的审批文本映射为官方结构化 ApprovalResult。
// 这是 Host 的职责：官方 Host 直接发送 ApprovalResult，本项目前端目前发送"同意/拒绝"等文本，
// 这里做一层等价映射，使中间件可沿用官方 approval_wrapper.go 的 *ApprovalResult 数据契约。
// 同时兼容官方风格的 JSON：{"approved":true,"disapprove_reason":"..."}（前端若直接发结构化体也支持）。
func parseApprovalResult(content string) *agent.ApprovalResult {
	content = strings.TrimSpace(content)
	if content == "" {
		reason := "未提供审批内容"
		return &agent.ApprovalResult{Approved: false, DisapproveReason: &reason}
	}
	// 官方风格的结构化 JSON
	var structured agent.ApprovalResult
	if json.Unmarshal([]byte(content), &structured) == nil && (structured.Approved || structured.DisapproveReason != nil) {
		return &structured
	}
	// 兼容旧前端：关键词匹配
	switch strings.ToLower(content) {
	case "approve", "同意", "确认", "yes", "y", "ok":
		return &agent.ApprovalResult{Approved: true}
	case "reject", "拒绝", "取消", "no", "n":
		return &agent.ApprovalResult{Approved: false, DisapproveReason: &content}
	default:
		// 无法识别：默认拒绝，原始文本作为原因便于排查
		return &agent.ApprovalResult{Approved: false, DisapproveReason: &content}
	}
}
