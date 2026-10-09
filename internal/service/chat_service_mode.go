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

	client, enhancedCtx, err := s.initContext(ctx, userID, sessionID, req.ModelID, req.ModelType, req.Content, preToolsTokens)
	if err != nil {
		sendErrorEvent(ctx, eventCh, err, profile)
		return
	}
	history := excludeByMessageID(enhancedCtx.History, userMsgID)
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
	agentReq.SessionID = sessionID
	agentReq.Profile = profile

	s.applyResumeParams(ctx, sessionID, req, &agentReq)
	agentEventCh, err := s.agentEngine.Execute(agentCtx, agentReq, chatModel)
	if err != nil {
		logger.Errorf("Agent 执行失败, sessionID=%s: %v", sessionID, err)
		llm.ReduceContextBudgetOnError(req.ModelID, apperrors.Classify(err))
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
				agentSources = agentEvent.Sources
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
			// 失败原因只认种类，不再拿错误文本去撞关键词表 ——
			// 此前把 agentEvent.Error 原样喂给关键词表，裸词 "exceeds" 让
			// 「exceeds max iterations」也被判成上下文超长，把模型窗口从 64000 砍到 8192。
			failureDetail = messageFor(agentEvent.Kind, profile).Detail
			llm.ReduceContextBudgetOnError(req.ModelID, agentEvent.Kind)
		}

		eventch.Send(ctx, eventCh, toStreamEvent(agentEvent, profile))

		if len(agentEvent.Sources) > 0 {
			agentSources = agentEvent.Sources
		}
		applyReasoningStep(&reasoningSteps, agentEvent)
	}

	if agentErrorSeen || ctx.Err() != nil {
		if executionPlan != nil {
			executionPlan.Status, executionPlan.Phase = agent.TaskError, "failed"
			if ctx.Err() != nil {
				executionPlan.Status = agent.TaskCanceled
				failureDetail = "执行已取消"
			}
			for i := range executionPlan.Tasks {
				if status := executionPlan.Tasks[i].Status; status == agent.TaskPending || status == agent.TaskRunning || status == agent.TaskWaiting {
					executionPlan.Tasks[i].Status = agent.TaskCanceled
				}
			}
			metadata := datatypes.JSON(mustMarshal(map[string]any{"execution_plan": executionPlan, "reasoning_steps": reasoningSteps, "execution_error": failureDetail}))
			saveCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err := s.saveAssistantMessage(saveCtx, sessionID, assistantMsgID, "深度任务未完成："+failureDetail, req, agentSources, metadata); err != nil {
				logger.Errorf("保存深度任务失败记录失败: sessionID=%s, err=%v", sessionID, err)
			}
			if agentReq.CheckpointID != "" {
				_ = s.sessionRepo.ClearPendingCheckpoint(saveCtx, sessionID)
				_ = s.sessionRepo.ClearPendingClarify(saveCtx, sessionID)
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

// applyResumeParams 判断本次请求要不要恢复上次中断的执行，是则把恢复参数写进 agentReq。
//
// 三种情况都表示「本次走首次执行」，不改 agentReq：
//   - session 上没有 pending checkpoint（正常首轮）
//   - checkpoint 已超时（字节可能已被后台 Ticker 清理，恢复必然失败）
//   - 有 checkpoint 但用户这次没带审批内容（前端重开了页面）
//
// 抽出来的原因是这段判断原本埋在事件消费循环上方：不产生事件、只改两个字段，
// 混在里面既读不出来也测不了 —— 抽出后可覆盖上面三种分支 + 两种 ResumeData 形态。
func (s *chatService) applyResumeParams(ctx context.Context, sessionID string, req requestdto.SendMessageRequest, agentReq *agent.Request) {
	session, err := s.sessionRepo.FindByID(ctx, sessionID)
	if err != nil {
		// 查不到 session 时按「无 pending」继续，会让用户以为审批已提交、系统却重跑一遍。
		// 至少留下 sessionID 便于对账。
		logger.Warnf("[ChatService] 读取 session 失败，无法判断是否有待恢复 checkpoint: sessionID=%s, err=%v", sessionID, err)
		return
	}
	if session == nil || !session.HasPendingCheckpoint() {
		return
	}
	pc, _ := session.GetPendingCheckpoint()
	if pc == nil {
		return
	}

	// clarify 等的是「用户回答问题」，10min 够了；danger 等的是「用户做审批决策」，24h。
	// 两个阈值混用会把还在慢慢审的请求当成过期直接清掉。
	timeout := entity.CheckpointDefaultTimeout
	if pc.IsClarify {
		timeout = entity.ClarifyDefaultTimeout
	}
	if pc.IsExpired(timeout) {
		logger.Warnf("[ChatService] pending checkpoint 已超时（%v），放弃恢复并清除: checkpointID=%s", timeout, pc.CheckpointID)
		s.clearPendingResume(ctx, sessionID)
		return
	}

	if req.Content == "" {
		logger.Warnf("[ChatService] 有 pending checkpoint 但用户未提供审批内容，走首次执行: checkpointID=%s", pc.CheckpointID)
		s.clearPendingResume(ctx, sessionID)
		return
	}

	logger.Infof("[ChatService] 设置恢复参数: checkpointID=%s, interruptID=%s, isClarify=%v", pc.CheckpointID, pc.InterruptID, pc.IsClarify)
	agentReq.CheckpointID = pc.CheckpointID
	if pc.IsClarify {
		// clarify 的恢复数据是用户的回答文本，与官方 FollowUpTool 的 UserAnswer 语义一致。
		agentReq.ResumeData = map[string]any{pc.InterruptID: req.Content}
		return
	}
	// danger 的恢复数据必须是结构化 ApprovalResult —— 前端发的是「同意/拒绝」这类中文文本。
	agentReq.ResumeData = map[string]any{pc.InterruptID: parseApprovalResult(req.Content)}
}

// clearPendingResume 清掉待恢复标记。checkpoint 与 clarify 两处标记成对写入，必须成对清除。
func (s *chatService) clearPendingResume(ctx context.Context, sessionID string) {
	if err := s.sessionRepo.ClearPendingCheckpoint(ctx, sessionID); err != nil {
		logger.Warnf("[ChatService] 清除 pending checkpoint 失败: sessionID=%s, err=%v", sessionID, err)
	}
	if err := s.sessionRepo.ClearPendingClarify(ctx, sessionID); err != nil {
		logger.Warnf("[ChatService] 清除 pending clarify 失败: sessionID=%s, err=%v", sessionID, err)
	}
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

// toStreamEvent 将 Agent 事件转换为 SSE 流式事件。
//
// 错误事件的文案在这里做**唯一的一次翻译**：agent 层只上报 Kind，面向用户的
// Title/Detail 在这里按 Kind × 模式取自 chat_error_messages.go。
// 因此快速模式的失败不可能再显示深度模式的措辞，同一句话也不会出现第二个来源。
func toStreamEvent(e agent.Event, profile agent.Profile) dto.StreamEvent {
	se := dto.StreamEvent{
		Type:         e.Type,
		Title:        e.Title,
		Detail:       e.Detail,
		Status:       e.Status,
		Content:      e.Content,
		Error:        e.Error,
		Done:         e.Done,
		Retryable:    e.Retryable,
		PlanID:       e.PlanID,
		PlanVersion:  e.PlanVersion,
		Phase:        e.Phase,
		ReplanReason: e.ReplanReason,
		Round:        e.Round,
		TaskID:       e.TaskID,
		ParentTaskID: e.ParentTaskID,
		AgentName:    e.AgentName,
		TaskStatus:   e.TaskStatus,
		TaskIndex:    e.TaskIndex,
		TaskTotal:    e.TaskTotal,
	}
	if e.Type == agent.EventError {
		msg := messageFor(e.Kind, profile)
		se.Title, se.Detail, se.Retryable = msg.Title, msg.Detail, msg.Retryable
	}
	if e.Type == agent.EventPlan {
		if e.Plan != nil {
			se.Plan = agentPlanToDTO(e.Plan)
		} else {
			se.Plan = &dto.ExecutionPlan{ID: e.PlanID, Version: e.PlanVersion, Phase: e.Phase, Reason: e.ReplanReason, Strategy: "sequential", Status: e.TaskStatus}
		}
	}
	if e.Type == agent.EventTaskCreated || e.Type == agent.EventTaskUpdated || e.Type == agent.EventTaskResult {
		se.Task = &dto.TaskProgress{ID: e.TaskID, Agent: e.AgentName, Title: e.Title, DependsOn: e.DependsOn, Status: e.TaskStatus, Summary: e.Detail}
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

func agentPlanToDTO(plan *agent.ExecutionPlan) *dto.ExecutionPlan {
	if plan == nil {
		return nil
	}
	out := &dto.ExecutionPlan{ID: plan.ID, Version: plan.Version, Phase: plan.Phase, Reason: plan.Reason, Round: plan.Round, Strategy: plan.Strategy, Status: plan.Status, Tasks: make([]dto.TaskProgress, 0, len(plan.Tasks))}
	for _, task := range plan.Tasks {
		out.Tasks = append(out.Tasks, dto.TaskProgress{ID: task.ID, Agent: task.Agent, Title: task.Title, DependsOn: append([]string(nil), task.DependsOn...), Status: task.Status, Summary: task.Summary, AddedVersion: task.AddedVersion})
	}
	return out
}

func upsertTaskProgress(plan *dto.ExecutionPlan, event agent.Event) {
	if plan == nil || event.TaskID == "" || (event.PlanID != "" && event.PlanID != plan.ID) || event.PlanVersion < plan.Version {
		return
	}
	if event.PlanVersion > plan.Version {
		plan.Version = event.PlanVersion
	}
	if event.Phase != "" {
		plan.Phase = event.Phase
	}
	if event.ReplanReason != "" {
		plan.Reason = event.ReplanReason
	}
	if event.Round > 0 {
		plan.Round = event.Round
	}
	progress := dto.TaskProgress{ID: event.TaskID, Agent: event.AgentName, Title: event.Title, DependsOn: append([]string(nil), event.DependsOn...), Status: event.TaskStatus, Summary: event.Detail, AddedVersion: plan.Version}
	for i := range plan.Tasks {
		if plan.Tasks[i].ID == progress.ID {
			plan.Tasks[i] = mergeTaskProgress(plan.Tasks[i], progress)
			return
		}
	}
	plan.Tasks = append(plan.Tasks, progress)
}

func mergeTaskProgress(current, update dto.TaskProgress) dto.TaskProgress {
	update.AddedVersion = current.AddedVersion
	if update.ID == "" {
		update.ID = current.ID
	}
	if update.Agent == "" {
		update.Agent = current.Agent
	}
	if update.Title == "" {
		update.Title = current.Title
	}
	if len(update.DependsOn) == 0 {
		update.DependsOn = current.DependsOn
	}
	if update.Status == "" {
		update.Status = current.Status
	}
	if update.Summary == "" {
		update.Summary = current.Summary
	}
	return update
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
