package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/google/uuid"
	"gorm.io/datatypes"

	"solvify-agent/internal/agent"
	requestdto "solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
	apperrors "solvify-agent/pkg/errors"
	"solvify-agent/pkg/eventch"
	"solvify-agent/pkg/logger"
	"solvify-agent/pkg/traceid"
)

// ─── Agent 模式（快速 / 深度共用） ──────────────────────────

// processAgentMode 是快速与深度两个模式共用的 Agent 执行流程。
// 快速模式使用 ReAct，深度模式使用 Plan-and-Execute；
// 两个模式的差异都在 Agent 层，
// 这里只靠 profile 决定工具 token 预估口径与收尾判据。
func (s *chatService) processAgentMode(ctx context.Context, userID, sessionID, userMsgID string, req requestdto.SendMessageRequest, profile agent.Profile, eventCh chan<- dto.StreamEvent) {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("[%s] processAgentMode panic 已恢复: %v", profile, r)
			msg := messageFor(apperrors.KindUnhandled, profile)
			eventch.Send(ctx, eventCh, dto.StreamEvent{Type: "error", Title: msg.Title, Detail: msg.Detail, Retryable: msg.Retryable, Done: true})
		}
	}()

	assistantMsgID := uuid.New().String()

	// P0-④ 关键修复：在 initContext 之前，先把"将发送给模型的工具定义"预构建并按真 BPE 算总 token，
	// 之后把 toolsTokens 传给 initContext，calculateContextBudgets 会先从 maxCtx 扣除，
	// 避免工具定义悄悄吃掉历史/检索预算，导致最终请求超上下文长度。
	sendProgressEvent(ctx, eventCh, "正在加载上下文...")
	preToolsTokens := 0
	agentCtx := ctx
	if s.agentEngine != nil {
		var tErr error
		// 先用"仅创建客户端拿到 modelName"解析出 ModelName，再交给 PrebuildTools 估算工具 token。
		client, cErr := s.resolveClient(ctx, userID, req.ModelID, req.ModelType)
		if cErr == nil {
			modelName := client.ModelName()
			preToolsTokens, agentCtx, tErr = s.agentEngine.EstimateToolsTokens(ctx, userID, req.KnowledgeBaseIDs, modelName, profile, req.MCPDisabled(), req.MCPIDs()...)
			if tErr != nil {
				logger.Warnf("预估算工具定义 token 失败，按 0 处理: %v", tErr)
				preToolsTokens = 0
				agentCtx = ctx
			}
		}
	}

	client, enhancedCtx, err := s.initContext(ctx, userID, sessionID, req.ModelID, req.ModelType, req.Content, userMsgID, preToolsTokens)
	if err != nil {
		sendErrorEvent(ctx, eventCh, err, profile)
		return
	}
	history := s.modelHistory(enhancedCtx, userMsgID)
	chatModel := client.ChatModel()
	eventch.Send(ctx, eventCh, dto.StreamEvent{Type: "start", MessageID: assistantMsgID})

	progress := "正在深度推理..."
	if profile == agent.ProfileQuick {
		progress = "正在快速分析..."
	}
	sendProgressEvent(ctx, eventCh, progress)
	agentPB := NewPromptBuilder("", enhancedCtx.Summary, enhancedCtx.Memories, enhancedCtx.UserCtx).
		WithProfile(enhancedCtx.Profile).
		WithPreference(enhancedCtx.Preference)
	agentReq := agentPB.BuildAgentRequestFields(userID, req.Content, req.ModelID, req.ModelType, req.KnowledgeBaseIDs, req.MCPIDs(), req.MCPDisabled(), history)
	recentIDs := make(map[string]bool, len(enhancedCtx.History))
	for _, message := range enhancedCtx.History {
		recentIDs[message.ID] = true
	}
	recalledIDs := make(map[string]bool, len(enhancedCtx.RecallHistory))
	for _, message := range enhancedCtx.RecallHistory {
		recalledIDs[message.ID] = !recentIDs[message.ID]
	}
	for _, message := range history {
		if recalledIDs[message.ID] {
			agentReq.ContextData = append(agentReq.ContextData, agent.ContextMaterial{Kind: "recall", Content: message.Content, SourceID: message.ID})
		} else if strings.HasSuffix(message.Content, truncationMarker) {
			agentReq.ContextData = append(agentReq.ContextData, agent.ContextMaterial{Kind: "history_fragment", Content: message.Content, SourceID: message.ID})
		}
	}
	agentReq.SessionID = sessionID
	agentReq.Profile = profile
	// agent 层拿不到 llm 客户端，预算检查需要的窗口与模型名由这里带过去。
	if err := client.CheckAgentTools(); err != nil {
		sendErrorEvent(ctx, eventCh, err, profile)
		return
	}
	agentReq.InputLimit = client.InputLimit()
	agentReq.OutputLimit = client.OutputLimit()
	agentReq.ModelName = client.ModelName()
	agentReq.MaxContextLength = client.MaxContextLength()

	if err := s.applyResumeParams(ctx, sessionID, req, &agentReq); err != nil {
		sendErrorEvent(ctx, eventCh, err, profile)
		return
	}
	agentEventCh, err := s.agentEngine.Execute(agentCtx, agentReq, chatModel)
	if err != nil {
		logger.Errorf("Agent 执行失败, sessionID=%s: %v", sessionID, err)
		sendErrorEvent(ctx, eventCh, err, profile)
		return
	}

	var fullContent string
	var agentSources []dto.SourceInfo
	var reasoningSteps []dto.ReasoningStep
	var executionPlan *dto.ExecutionPlan
	toolEventSeen := false
	agentErrorSeen := false
	var failureDetail string
	var failureRetryable bool

	for agentEvent := range agentEventCh {
		if agentEvent.Type == agent.EventPlan && agentEvent.Plan != nil {
			if executionPlan != nil && executionPlan.ID == agentEvent.Plan.ID && agentEvent.Plan.Version < executionPlan.Version {
				continue
			}
			nextPlan := agentPlanToDTO(agentEvent.Plan)
			if executionPlan != nil && executionPlan.ID == nextPlan.ID {
				for i := range nextPlan.Tasks {
					for _, previous := range executionPlan.Tasks {
						if previous.ID == nextPlan.Tasks[i].ID {
							nextPlan.Tasks[i].AddedVersion = previous.AddedVersion
							break
						}
					}
				}
			}
			executionPlan = nextPlan
		}
		if (agentEvent.Type == agent.EventTaskCreated || agentEvent.Type == agent.EventTaskUpdated || agentEvent.Type == agent.EventTaskResult) && executionPlan != nil {
			if (agentEvent.PlanID != "" && agentEvent.PlanID != executionPlan.ID) || agentEvent.PlanVersion < executionPlan.Version {
				continue
			}
			upsertTaskProgress(executionPlan, agentEvent)
		}
		if agentEvent.Type == agent.EventDone {
			if agentEvent.Content != "" {
				fullContent = agentEvent.Content
			}
			if len(agentEvent.Sources) > 0 {
				agentSources = agentSourcesToDTO(agentEvent.Sources)
			}
			if executionPlan != nil {
				executionPlan.Status = "success"
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
				if executionPlan != nil {
					pcData.ExecutionPlan = datatypes.JSON(mustMarshal(executionPlan))
				}
				if toolName, ok := agentEvent.InterruptInfo["tool_name"].(string); ok {
					pcData.ToolName = toolName
				}
				raw, _ := json.Marshal(pcData)
				if sErr := s.sessionRepo.SetPendingCheckpoint(ctx, sessionID, raw); sErr != nil {
					logger.Errorf("存储 pending checkpoint 失败: %v", sErr)
					sendErrorEvent(ctx, eventCh, fmt.Errorf("保存待审批状态失败: %w", sErr), profile)
					return
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
			eventch.Send(ctx, eventCh, toStreamEvent(agentEvent, profile))
			return
		}

		if agentEvent.Type == agent.EventAnswer {
			fullContent += agentEvent.Content
		}
		if agentEvent.Type == agent.EventToolCall {
			toolEventSeen = true
		}
		if agentEvent.Type == agent.EventToolResult {
			toolEventSeen = true
		}
		if agentEvent.Type == agent.EventError {
			agentErrorSeen = true
			// 在错误发送前清理：前端收到错误即查询状态，不能读到已消费的澄清卡。
			if agentReq.CheckpointID != "" {
				clearCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				s.clearPendingResume(clearCtx, sessionID)
				cancel()
			}
			// 失败原因只认种类，不再拿错误文本去撞关键词表 ——
			// 此前把 agentEvent.Error 原样喂给关键词表，裸词 "exceeds" 让
			// 「exceeds max iterations」也被判成上下文超长，把模型窗口从 64000 砍到 8192。
			failure := messageFor(agentEvent.Kind, profile)
			failureDetail, failureRetryable = failure.Detail, failure.Retryable
		}

		eventch.Send(ctx, eventCh, toStreamEvent(agentEvent, profile))

		if len(agentEvent.Sources) > 0 {
			agentSources = agentSourcesToDTO(agentEvent.Sources)
		}
		applyReasoningStep(&reasoningSteps, agentEvent)
	}

	if agentErrorSeen || ctx.Err() != nil {
		if failureDetail == "" {
			failureDetail = "执行已取消"
		}
		if !agentErrorSeen && agentReq.CheckpointID != "" {
			clearCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			s.clearPendingResume(clearCtx, sessionID)
			cancel()
		}
		if executionPlan != nil {
			executionPlan.Status, executionPlan.Phase = agent.TaskError, "failed"
			// 客户端收到错误终态后会关闭 SSE；不能用随后的连接取消覆盖真实失败。
			if ctx.Err() != nil && !agentErrorSeen {
				executionPlan.Status = agent.TaskCanceled
				failureDetail = "执行已取消"
			}
			for i := range executionPlan.Tasks {
				if status := executionPlan.Tasks[i].Status; status == agent.TaskPending || status == agent.TaskRunning || status == agent.TaskWaiting {
					executionPlan.Tasks[i].Status = agent.TaskCanceled
				}
			}
		}
		if fullContent != "" || executionPlan != nil {
			content := fullContent + "\n\n（回答已中断，内容不完整）"
			if fullContent == "" {
				content = "深度任务未完成：" + failureDetail
			}
			metadata := datatypes.JSON(mustMarshal(map[string]any{"execution_plan": executionPlan, "reasoning_steps": reasoningSteps, "execution_error": failureDetail, "execution_retryable": failureRetryable}))
			saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			if err := s.saveAssistantMessage(saveCtx, sessionID, assistantMsgID, content, req, agentSources, metadata); err != nil {
				logger.Errorf("保存回答中断记录失败: sessionID=%s, err=%v", sessionID, err)
			}
			cancel()
		}
		return
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
	// 深度模式要求真的产生工具调用；快速模式允许直接回答（寒暄/常识等），不适用这条判据。
	if profile == agent.ProfileDeep && !toolEventSeen && looksLikeExecutionPlan(fullContent) {
		logger.Warnf("深度模式未产生工具调用，仅返回执行计划，sessionID=%s, content=%q", sessionID, fullContent)
		msg := messageFor(apperrors.KindToolNotInvoked, profile)
		eventch.Send(ctx, eventCh, dto.StreamEvent{
			Type:      "error",
			Title:     msg.Title,
			Detail:    msg.Detail,
			Retryable: msg.Retryable,
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
	if rejectEmptyAnswer(ctx, eventCh, string(profile), sessionID, req.ModelID, assistantMsgID,
		fullContent, fmt.Sprintf("sources=%d, steps=%d", len(agentSources), len(reasoningSteps))) {
		return
	}

	var metadata datatypes.JSON
	metaMap := map[string]any{}
	if len(reasoningSteps) > 0 {
		metaMap["reasoning_steps"] = reasoningSteps
	}
	if profile == agent.ProfileDeep {
		if executionPlan != nil {
			executionPlan.Status = "success"
			metaMap["execution_plan"] = executionPlan
		}
	}
	if traceID := traceid.FromContext(ctx); traceID != "" {
		metaMap["trace_id"] = traceID
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
func (s *chatService) refreshContextAsync(ctx context.Context, userID, sessionID string, history []entity.ChatMessage, chatModel model.BaseChatModel) {
	if s == nil || s.contextSvc == nil {
		return
	}

	// 后台刷新要脱离请求 ctx（响应已返回，不能被请求取消掐断），
	// 但也不该换成 context.Background()：那会把 ctx 上的值（含本次请求的关联 id）一起丢掉。
	// context.WithoutCancel 只切断取消信号、保留全部值，正好是这里要的语义。
	// 在 goroutine 外先取一次，避免与父 ctx 结束产生竞态。
	baseCtx := context.WithoutCancel(ctx)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf("refreshContextAsync panic 已恢复: sessionID=%s, err=%v", sessionID, r)
			}
		}()

		if err := runWithRetry(3, "ctx.summary", func(attempt int) error {
			refreshCtx, cancel := context.WithTimeout(baseCtx, 45*time.Second+time.Duration(attempt*15)*time.Second)
			defer cancel()
			_, err := s.contextSvc.SummarizeSession(refreshCtx, sessionID, chatModel)
			return err
		}); err != nil {
			logger.Warnf("生成会话摘要失败（已重试 3 次）: sessionID=%s, err=%v", sessionID, err)
		}

		if err := runWithRetry(3, "ctx.memory", func(attempt int) error {
			refreshCtx, cancel := context.WithTimeout(baseCtx, 45*time.Second+time.Duration(attempt*15)*time.Second)
			defer cancel()
			_, err := s.contextSvc.ExtractMemories(refreshCtx, userID, sessionID, history, chatModel)
			return err
		}); err != nil {
			logger.Warnf("提取用户记忆失败（已重试 3 次）: sessionID=%s, err=%v", sessionID, err)
		}
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
	// 把最终答复推给三方平台（未接三方 / 答复为空时内部直接返回）。
	//
	// 上报点选这里，是因为这是两个模式共用的【唯一一处成功收尾】—— 错误路径、中断路径
	// 都不会走到这里，而它们本来也没有「最终答复」可报。放到别处会变成
	// 「收尾时回头找答复」，那是第二个来源。
	s.tracer.End(ctx, content)

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

// modelHistory 拼出真正发给模型的那份历史：近期对话 + 召回的相关历史。
//
// 两个容器分别裁剪过预算（见 contextService.BuildContext），这里只做装配：
//   - 召回项按时间插回时间线，不单独堆在末尾 —— 堆在末尾的话模型会把它当成
//     "最后发生的事"，而它其实是几个月前聊的；
//   - 召回项加「历史片段 + 时间」前缀，否则同一句话出现在上下文里两次
//     （一次在近期、一 次在召回），模型会当成重复消息而忽略；
//   - 同一 ID 只保留一份：召回结果与近期重叠是常态，不是异常。
//
// 只对召回项加标注：近期对话是原原本本的对话历史，加了前缀反而在污染它；
// 而 refreshContextAsync 消费的是 enhancedCtx.History 原文，不经过这里，
// 摘要与记忆抽取拿到的仍然是干净原文。
func (s *chatService) modelHistory(enhancedCtx *EnhancedContext, excludeID string) []entity.ChatMessage {
	if enhancedCtx == nil {
		return nil
	}
	recent := excludeByMessageID(enhancedCtx.History, excludeID)

	// 先按 ID 排除已在近期里的，再加前缀 —— 反过来做会给同一句话加两次标注。
	inRecent := make(map[string]struct{}, len(recent))
	for _, m := range recent {
		inRecent[m.ID] = struct{}{}
	}

	merged := make([]entity.ChatMessage, 0, len(recent)+len(enhancedCtx.RecallHistory))
	merged = append(merged, recent...)
	for _, m := range excludeByMessageID(enhancedCtx.RecallHistory, excludeID) {
		if _, dup := inRecent[m.ID]; dup {
			continue
		}
		m.Content = fmt.Sprintf("[历史片段 · %s] %s", m.CreatedAt.Format("2006-01-02 15:04"), m.Content)
		merged = append(merged, m)
	}
	return sortMessagesByTime(merged)
}

// sortMessagesByTime 按时间正序排列（同值按 ID 裁决，保证全序 —— 红线 26）。
func sortMessagesByTime(msgs []entity.ChatMessage) []entity.ChatMessage {
	sort.SliceStable(msgs, func(i, j int) bool {
		if !msgs[i].CreatedAt.Equal(msgs[j].CreatedAt) {
			return msgs[i].CreatedAt.Before(msgs[j].CreatedAt)
		}
		return msgs[i].ID < msgs[j].ID
	})
	return msgs
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
