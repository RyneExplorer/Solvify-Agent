package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"
	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"
	"gorm.io/datatypes"

	"solvify-agent/internal/agent"
	"solvify-agent/internal/llm"
	requestdto "solvify-agent/internal/model/dto/request"
	dto "solvify-agent/internal/model/dto/response"
	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/observability"
	"solvify-agent/internal/rag"
	"solvify-agent/pkg/config"
	"solvify-agent/pkg/logger"
)

// queryRewritePronouns 查询改写触发表（包级变量，初始化一次）
// 注意：不要放「这/那/其」等单字（过宽误伤），保持在 2 个字及以上的常见指代短语
var queryRewritePronouns = []string{
	// 代词类
	"它是", "他是", "她是", "它们是", "这个", "那个", "这些", "那些", "此人", "此物",
	// 方位指代
	"前者", "后者", "上面", "下面", "前边", "后边", "前面", "后面", "上述", "前述", "如下", "如上", "此前", "此后",
	// 时间/对话位置指代
	"刚才", "刚刚", "刚说", "刚提到", "刚才说的", "刚才提到的", "前面说", "前面聊", "之前说", "之前聊",
	"上面聊的", "前面讨论", "之前讨论", "刚才讨论", "上一步", "上一条", "刚刚那条",
	// 事物指代前缀
	"该问题", "该方法", "该方案", "该文档", "该内容", "该结论", "该资料", "该文件",
	"其优势", "其缺点", "其原理", "其内容", "其原因", "其细节", "其区别", "其用途",
	// 追问语气词
	"怎么样", "如何呢", "怎么说", "呢？", "呢?",
	// 英文
	"it ", " this", " that", " these", " those", " they", " them", "the above", "previous",
	" the first one", " the second one", "latter",
}

// 极短问题长度（rune 数 ≤ 此阈值时无条件触发改写）
const queryRewriteShortRunes = 8

// ─── 快速检索模式 ───────────────────────────────────────────

// processMessage 处理消息的核心流程（快速检索模式）
//
// 速度优化策略：
// 1. 仅当问题含指代/省略时才触发 LLM 查询改写，否则跳过改写直接检索
// 2. 需要改写时：改写与原始检索并行；改写有变化则以改写结果为准（不污染 merge）
// 3. history 剔除本轮刚落库的 user 消息，避免 Prompt 重复
func (s *chatService) processMessage(ctx context.Context, userID, sessionID, userMsgID string, req requestdto.SendMessageRequest, eventCh chan<- dto.StreamEvent) {
	obsOk := s.obs != nil
	if obsOk {
		_, span := s.obs.StartSpan(ctx, "chat.quick", observability.ComponentServiceChat, observability.Attrs{
			"session_id": sessionID,
			"user_id":    userID,
			"model_id":   req.ModelID,
			"search_mode": "quick",
		})
		defer func() {
			status := observability.SpanStatusOK
			var errVal error
			if r := recover(); r != nil {
				status = observability.SpanStatusError
				errVal = fmt.Errorf("panic: %v", r)
				sendErrorEvent(eventCh, fmt.Errorf("内部错误"), "处理过程中发生未预期错误")
			}
			s.obs.EndSpan(ctx, span, status, errVal, nil)
			if s.obs != nil {
				s.obs.AddRootAttrs(ctx, observability.Attrs{
					"assistant_message_id": span.Attrs["assistant_message_id"],
					"rag_hit":              span.Attrs["rag_hit"],
					"intent":               span.Attrs["intent"],
				})
			}
		}()
		s.obs.Incr(ctx, "chat_quick_requests_total", map[string]string{
			"model_id": req.ModelID,
		}, 1)
	}

	sendProgressEvent(eventCh, "正在加载上下文...")
	t0 := time.Now()
	client, enhancedCtx, err := s.initContext(ctx, userID, sessionID, req.ModelID, req.ModelType, req.Content)
	if err != nil {
		if obsOk {
			s.obs.AddRootAttrs(ctx, observability.Attrs{"init_ctx_error": err.Error()})
			s.obs.Incr(ctx, "chat_quick_errors_total", map[string]string{"stage": "init_ctx"}, 1)
		}
		sendErrorEvent(eventCh, err, err.Error())
		return
	}
	history := excludeByMessageID(enhancedCtx.History, userMsgID)
	chatModel := client.ChatModel()
	if obsOk {
		s.obs.Observe(ctx, "chat_quick_init_ctx_seconds", map[string]string{"model_id": req.ModelID}, time.Since(t0).Seconds())
	}

	sendProgressEvent(eventCh, "正在分析您的意图...")
	rewritten := FallbackOriginalRewritten(req.Content)
	t1 := time.Now()
	if len(history) > 0 {
		rewritten = s.rewriteQuery(ctx, chatModel, history, req.Content, enhancedCtx.Summary)
	} else {
		quickIntent := AnalyzeIntent(req.Content)
		rewritten.Intent = quickIntent.Intent
	}
	if obsOk {
		s.obs.Observe(ctx, "chat_quick_rewrite_seconds", map[string]string{"intent": string(rewritten.Intent)}, time.Since(t1).Seconds())
		s.obs.AddRootAttrs(ctx, observability.Attrs{"intent": string(rewritten.Intent), "rewritten": fmt.Sprintf("%t", rewritten.Rewritten)})
	}

	var sources []dto.SourceInfo
	var retrieveResult rag.Result

	switch rewritten.Intent {
	case IntentGreeting, IntentChitchat:
		sendProgressEvent(eventCh, "正在整理回答...")

	case IntentIdentity, IntentMeta, IntentListQuery:
		fallthrough

	default:
		sendProgressEvent(eventCh, "正在检索知识库...")
		queries := make([]string, 0, 1+len(rewritten.ExpandedQueries))
		queries = append(queries, rewritten.MainQuery)
		for _, eq := range rewritten.ExpandedQueries {
			queries = append(queries, eq)
		}
		origTrimmed := strings.TrimSpace(req.Content)
		if origTrimmed != "" && origTrimmed != rewritten.MainQuery {
			queries = append(queries, origTrimmed)
		}
		t2 := time.Now()
		if len(queries) == 1 {
			var err2 error
			sources, retrieveResult, err2 = s.retrieveContext(ctx, userID, rewritten.MainQuery, req.KnowledgeBaseIDs)
			if err2 != nil {
				if obsOk {
					s.obs.Incr(ctx, "chat_quick_errors_total", map[string]string{"stage": "retrieve"}, 1)
				}
				logger.Errorf("知识库检索失败, sessionID=%s: %v", sessionID, err2)
				sendErrorEvent(eventCh, err2, "知识库检索失败")
				return
			}
		} else {
			type retPair struct {
				srcs []dto.SourceInfo
				res  rag.Result
			}
			mu := &sync.Mutex{}
			results := make([]retPair, 0, len(queries))
			g, gCtx := errgroup.WithContext(ctx)
			g.SetLimit(3)
			for _, q := range queries {
				q := q
				g.Go(func() error {
					s, r, e := s.retrieveContext(gCtx, userID, q, req.KnowledgeBaseIDs)
					if e != nil {
						logger.Warnf("多路检索单路失败 query=%q err=%v", q, e)
						return nil
					}
					mu.Lock()
					results = append(results, retPair{s, r})
					mu.Unlock()
					return nil
				})
			}
			_ = g.Wait()

			seen := map[string]struct{}{}
			var (
				mergedDocs []rag.Document
				mergedSrc  []dto.SourceInfo
				mergeHit   bool
			)
			for _, rp := range results {
				if rp.res.Hit {
					mergeHit = true
				}
				for _, d := range rp.res.Documents {
					key := d.Title + "|" + d.ID
					if _, ok := seen[key]; ok {
						continue
					}
					seen[key] = struct{}{}
					mergedDocs = append(mergedDocs, d)
				}
				for _, src := range rp.srcs {
					key := src.DocumentID + "|" + src.Title
					if _, ok := seen["src__"+key]; ok {
						continue
					}
					seen["src__"+key] = struct{}{}
					mergedSrc = append(mergedSrc, src)
				}
			}
			retrieveResult = rag.Result{Hit: mergeHit, Documents: mergedDocs}
			sources = mergedSrc
		}
		if obsOk {
			s.obs.Observe(ctx, "chat_quick_retrieve_seconds", map[string]string{
				"queries": fmt.Sprintf("%d", len(queries)),
				"hit":     fmt.Sprintf("%t", retrieveResult.Hit),
			}, time.Since(t2).Seconds())
			s.obs.Incr(ctx, "rag_retrievals_total", map[string]string{
				"mode":      "quick",
				"hit":       fmt.Sprintf("%t", retrieveResult.Hit),
				"queries_n": fmt.Sprintf("%d", len(queries)),
			}, 1)
			s.obs.AddRootAttrs(ctx, observability.Attrs{
				"rag_hit":       retrieveResult.Hit,
				"rag_docs_n":    len(retrieveResult.Documents),
				"rag_queries_n": len(queries),
			})
		}
	}

	sendProgressEvent(eventCh, "正在整理资料...")
	pb := NewPromptBuilder(PromptModeQuick, quickModeSystemPrompt, enhancedCtx.Summary, enhancedCtx.Memories, enhancedCtx.UserCtx).
		WithProfile(enhancedCtx.Profile).
		WithPreference(enhancedCtx.Preference)
	messages := pb.BuildMessagesQuick(history, req.Content, retrieveResult, enhancedCtx.RetrievalBudget)

	assistantMsgID := uuid.New().String()
	if obsOk {
		s.obs.AddRootAttrs(ctx, observability.Attrs{"assistant_message_id": assistantMsgID})
	}
	t3 := time.Now()
	fullContent, err := s.streamAndCollect(ctx, chatModel, messages, assistantMsgID, eventCh)
	if obsOk {
		s.obs.Observe(ctx, "chat_quick_llm_stream_seconds", map[string]string{"model_id": req.ModelID}, time.Since(t3).Seconds())
		s.obs.Incr(ctx, "llm_stream_requests_total", map[string]string{
			"provider": providerLabel(client),
			"model_id": req.ModelID,
			"success":  fmt.Sprintf("%t", err == nil),
		}, 1)
	}
	if err != nil {
		llm.ReduceContextBudgetOnError(req.ModelID, err)
		return
	}
	if fullContent == "" {
		eventCh <- dto.StreamEvent{Type: "done", MessageID: assistantMsgID, Content: "", Sources: sources, Done: true}
		return
	}
	if obsOk {
		s.obs.AddRootAttrs(ctx, observability.Attrs{
			"assistant_chars": len([]rune(fullContent)),
		})
	}
	s.emitDoneAndSave(eventCh, sessionID, assistantMsgID, fullContent, req, sources, nil, func(meta map[string]any) {
		if obsOk && meta != nil {
			meta["trace_id"] = observability.TraceIDFromContext(ctx)
		}
	})

	s.refreshContextAsync(ctx, userID, sessionID, enhancedCtx.History, chatModel)
}

// ─── 深度思考模式 ───────────────────────────────────────────

// processDeepMode 深度思考模式处理流程
// 使用 eino ReAct Agent，自动管理 Think → Act → Observe 循环
func (s *chatService) processDeepMode(ctx context.Context, userID, sessionID, userMsgID string, req requestdto.SendMessageRequest, eventCh chan<- dto.StreamEvent) {
	obsOk := s.obs != nil
	if obsOk {
		_, span := s.obs.StartSpan(ctx, "chat.deep", observability.ComponentAgentEngine, observability.Attrs{
			"session_id":  sessionID,
			"user_id":     userID,
			"model_id":    req.ModelID,
			"search_mode": "deep",
		})
		defer func() {
			status := observability.SpanStatusOK
			var errVal error
			if r := recover(); r != nil {
				status = observability.SpanStatusError
				errVal = fmt.Errorf("panic: %v", r)
				eventCh <- dto.StreamEvent{Type: "error", Detail: "处理过程中发生未预期错误", Done: true}
			}
			s.obs.EndSpan(ctx, span, status, errVal, nil)
		}()
		s.obs.Incr(ctx, "chat_deep_requests_total", map[string]string{"model_id": req.ModelID}, 1)
	}

	assistantMsgID := uuid.New().String()
	if obsOk {
		s.obs.AddRootAttrs(ctx, observability.Attrs{"assistant_message_id": assistantMsgID})
	}

	sendProgressEvent(eventCh, "正在加载上下文...")
	t0 := time.Now()
	client, enhancedCtx, err := s.initContext(ctx, userID, sessionID, req.ModelID, req.ModelType, req.Content)
	if err != nil {
		if obsOk {
			s.obs.Incr(ctx, "chat_deep_errors_total", map[string]string{"stage": "init_ctx"}, 1)
		}
		sendErrorEvent(eventCh, err, err.Error())
		return
	}
	history := excludeByMessageID(enhancedCtx.History, userMsgID)
	chatModel := client.ChatModel()
	if obsOk {
		s.obs.Observe(ctx, "chat_deep_init_ctx_seconds", map[string]string{"model_id": req.ModelID}, time.Since(t0).Seconds())
	}

	eventCh <- dto.StreamEvent{Type: "start", MessageID: assistantMsgID}

	sendProgressEvent(eventCh, "正在深度推理...")
	agentPB := NewPromptBuilder(PromptModeDeep, "", enhancedCtx.Summary, enhancedCtx.Memories, enhancedCtx.UserCtx).
		WithProfile(enhancedCtx.Profile).
		WithPreference(enhancedCtx.Preference)
	agentReq := agentPB.BuildAgentRequestFields(userID, req.Content, req.ModelID, req.ModelType, req.KnowledgeBaseIDs, history)
	t1 := time.Now()
	agentEventCh, err := s.agentEngine.Execute(ctx, agentReq, chatModel)
	if err != nil {
		if obsOk {
			s.obs.Incr(ctx, "chat_deep_errors_total", map[string]string{"stage": "agent_execute"}, 1)
		}
		logger.Errorf("Agent 执行失败, sessionID=%s: %v", sessionID, err)
		llm.ReduceContextBudgetOnError(req.ModelID, err)
		sendErrorEvent(eventCh, err, "Agent 执行失败")
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

		eventCh <- toStreamEvent(agentEvent)

		if len(agentEvent.Sources) > 0 {
			agentSources = agentEvent.Sources
		}
		applyReasoningStep(&reasoningSteps, agentEvent)
	}
	if obsOk {
		s.obs.Observe(ctx, "chat_deep_agent_seconds", map[string]string{"model_id": req.ModelID}, time.Since(t1).Seconds())
		s.obs.Incr(ctx, "agent_runs_total", map[string]string{
			"error_seen": fmt.Sprintf("%t", agentErrorSeen),
			"tool_calls": fmt.Sprintf("%d", toolCallsN),
		}, 1)
		s.obs.AddRootAttrs(ctx, observability.Attrs{
			"tool_calls":    toolCallsN,
			"tool_errors":   toolErrorsN,
			"steps_n":       len(reasoningSteps),
			"rag_docs_n":    len(agentSources),
			"agent_error":   agentErrorSeen,
			"tool_used":     toolEventSeen,
			"assistant_chars": len([]rune(fullContent)),
		})
	}

	if agentErrorSeen {
		return
	}
	if !toolEventSeen && looksLikeExecutionPlan(fullContent) {
		logger.Warnf("深度模式未产生工具调用，仅返回执行计划，sessionID=%s, content=%q", sessionID, fullContent)
		if obsOk {
			s.obs.Incr(ctx, "agent_plan_without_tool_total", nil, 1)
		}
		eventCh <- dto.StreamEvent{
			Type:      "error",
			Title:     "深度推理未完成",
			Detail:    "当前模型没有正确发起工具调用，请重试或切换支持工具调用的模型",
			Retryable: true,
			Done:      true,
		}
		return
	}
	if fullContent == "" && len(reasoningSteps) == 0 {
		return
	}

	var metadata datatypes.JSON
	metaMap := map[string]any{}
	if len(reasoningSteps) > 0 {
		metaMap["reasoning_steps"] = reasoningSteps
	}
	if obsOk {
		metaMap["trace_id"] = observability.TraceIDFromContext(ctx)
	}
	if len(metaMap) > 0 {
		metadata = datatypes.JSON(mustMarshal(metaMap))
	}
	s.emitDoneAndSave(eventCh, sessionID, assistantMsgID, fullContent, req, agentSources, metadata, nil)

	s.refreshContextAsync(ctx, userID, sessionID, enhancedCtx.History, chatModel)
}

// refreshContextAsync 异步更新会话摘要和提取用户记忆
func (s *chatService) refreshContextAsync(ctx context.Context, userID, sessionID string, history []entity.ChatMessage, chatModel model.BaseChatModel) {
	if s.contextSvc == nil {
		return
	}
	go func() {
		refreshCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		if _, err := s.contextSvc.SummarizeSession(refreshCtx, sessionID, chatModel); err != nil {
			logger.Warnf("生成会话摘要失败: %v", err)
		}
		if _, err := s.contextSvc.ExtractMemories(refreshCtx, userID, sessionID, history, chatModel); err != nil {
			logger.Warnf("提取用户记忆失败: %v", err)
		}
	}()
}

// ─── 共享辅助方法 ───────────────────────────────────────────

// emitDoneAndSave 发送 done 事件并异步保存助手消息
// 注意：保存失败只记日志，禁止再向 eventCh 写事件（外层 defer close 后会 panic）
func (s *chatService) emitDoneAndSave(eventCh chan<- dto.StreamEvent, sessionID, msgID, content string, req requestdto.SendMessageRequest, sources []dto.SourceInfo, metadata datatypes.JSON, metaHook func(map[string]any)) {
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
	eventCh <- dto.StreamEvent{Type: "done", MessageID: msgID, Content: content, Sources: sources, Done: true}
	go func() {
		saveCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.saveAssistantMessage(saveCtx, sessionID, msgID, content, req, sources, finalMeta); err != nil {
			logger.Errorf("保存助手消息失败, messageID=%s: %v", msgID, err)
		}
	}()
}

func providerLabel(client *llm.OpenAIClient) string {
	if client == nil {
		return "unknown"
	}
	return "openai_compatible"
}

// rewriteQuery 用 LLM 结合历史对话改写用户问题，一次返回结构化结果（主查询+扩展查询+关键词+意图）
func (s *chatService) rewriteQuery(ctx context.Context, chatModel interface {
	Generate(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.Message, error)
}, history []entity.ChatMessage, question string, summary *entity.ChatSummary) RewrittenQuery {
	rewriteCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	messages := BuildRewritePrompt(history, question, summary)
	msg, err := chatModel.Generate(rewriteCtx, messages)
	if err != nil || msg == nil {
		// LLM 调用失败：回退为原问题，用正则兜底抽关键词
		return FallbackOriginalRewritten(question)
	}

	var r RewrittenQuery
	r.PostProcess(msg.Content, question)
	return r
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

// needsQueryRewrite 启发式判断是否需要 LLM 改写（指代/省略）
// 无指代时直接跳过改写，省掉 1 次 LLM 调用，显著加快快速检索
func needsQueryRewrite(question string) bool {
	q := strings.TrimSpace(question)
	if q == "" {
		return false
	}
	// 极短问题（字符数 ≤ 阈值，默认 8 rune）：大概率是指代/省略，直接改写
	if utf8.RuneCountInString(q) <= queryRewriteShortRunes {
		return true
	}
	// 匹配指代短语词表（包级变量，避免每次函数内重分配）
	lower := strings.ToLower(q)
	for _, p := range queryRewritePronouns {
		if strings.Contains(lower, strings.ToLower(p)) {
			return true
		}
	}
	return false
}

func (s *chatService) retrieveContext(ctx context.Context, userID, question string, knowledgeBaseIDs []string) ([]dto.SourceInfo, rag.Result, error) {
	logger.Infof("RAG 检索开始: userID=%s, question=%q, kbIDs=%v", userID, question, knowledgeBaseIDs)
	obsOk := s.obs != nil
	var span *observability.Span
	if obsOk {
		_, span = s.obs.StartSpan(ctx, "rag.retrieve", observability.ComponentRAGRetriever, observability.Attrs{
			"kb_n": fmt.Sprintf("%d", len(knowledgeBaseIDs)),
		})
		defer func() {
			if span != nil {
				s.obs.EndSpan(ctx, span, observability.SpanStatusOK, nil, nil)
			}
		}()
	}
	ragCfg := config.Get().RAG
	topK := ragCfg.TopK
	if ragCfg.Reranker.Enabled {
		if ragCfg.RecallK > 0 {
			topK = ragCfg.RecallK
		} else if topK > 0 {
			topK = topK * 5
		} else {
			topK = 20
		}
	}
	retrieveResult, err := s.retriever.Retrieve(ctx, rag.Query{
		Question:         question,
		TopK:             topK,
		KnowledgeBaseIDs: knowledgeBaseIDs,
		UserID:           userID,
	})
	if err != nil {
		if obsOk && span != nil {
			span.Status = observability.SpanStatusError
			span.Error = err.Error()
		}
		return nil, rag.Result{}, err
	}
	sources := groupDocumentsToSources(retrieveResult.Documents)
	if obsOk && span != nil {
		if span.Attrs == nil {
			span.Attrs = observability.Attrs{}
		}
		span.Attrs["top_k"] = topK
		span.Attrs["hit"] = retrieveResult.Hit
		span.Attrs["docs_n"] = len(retrieveResult.Documents)
	}
	logger.Infof("RAG 检索完成: hit=%v, 命中 %d 篇文档, 共 %d 个 chunk",
		retrieveResult.Hit, len(sources), len(retrieveResult.Documents))
	return sources, retrieveResult, nil
}

func (s *chatService) streamAndCollect(ctx context.Context, chatModel interface {
	Stream(ctx context.Context, input []*schema.Message, opts ...model.Option) (*schema.StreamReader[*schema.Message], error)
}, messages []*schema.Message, assistantMsgID string, eventCh chan<- dto.StreamEvent) (string, error) {
	sendProgressEvent(eventCh, "正在生成回答...")
	obsOk := s.obs != nil
	var span *observability.Span
	if obsOk {
		_, span = s.obs.StartSpan(ctx, "llm.stream", observability.ComponentLLMClient, observability.Attrs{})
		defer func() {
			if span != nil {
				s.obs.EndSpan(ctx, span, observability.SpanStatusOK, nil, nil)
			}
		}()
	}
	t0 := time.Now()
	streamReader, err := chatModel.Stream(ctx, messages)
	if err != nil {
		if obsOk {
			s.obs.Incr(ctx, "llm_stream_errors_total", map[string]string{"stage": "open_stream"}, 1)
			if span != nil {
				span.Status = observability.SpanStatusError
				span.Error = err.Error()
			}
		}
		logger.Errorf("LLM 调用失败: %v", err)
		sendErrorEvent(eventCh, err, "LLM 调用失败")
		return "", err
	}
	defer streamReader.Close()

	eventCh <- dto.StreamEvent{
		Type:      "start",
		MessageID: assistantMsgID,
	}

	if obsOk {
		ttftMs := time.Since(t0).Milliseconds()
		s.obs.Observe(ctx, "llm_stream_ttft_seconds", nil, float64(ttftMs)/1000.0)
		if span != nil {
			if span.Attrs == nil {
				span.Attrs = observability.Attrs{}
			}
			span.Attrs["ttft_ms"] = ttftMs
		}
	}

	var fullContent string
	for {
		msg, recvErr := streamReader.Recv()
		if recvErr != nil {
			if recvErr == io.EOF {
				break
			}
			if ctx.Err() != nil {
				logger.Infof("用户暂停，已收集 %d 字符", len(fullContent))
				return fullContent, recvErr
			}
			if obsOk {
				s.obs.Incr(ctx, "llm_stream_errors_total", map[string]string{"stage": "recv"}, 1)
				if span != nil {
					span.Status = observability.SpanStatusError
					span.Error = recvErr.Error()
				}
			}
			logger.Errorf("LLM 流式生成错误: %v", recvErr)
			sendErrorEvent(eventCh, recvErr, "LLM 流式生成错误")
			return fullContent, recvErr
		}
		if msg == nil || msg.Content == "" {
			continue
		}
		fullContent += msg.Content
		eventCh <- dto.StreamEvent{
			Type:    "content",
			Content: msg.Content,
		}
	}
	if obsOk && span != nil {
		if span.Attrs == nil {
			span.Attrs = observability.Attrs{}
		}
		span.Attrs["chars"] = len(fullContent)
	}

	return fullContent, nil
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
	return se
}

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
