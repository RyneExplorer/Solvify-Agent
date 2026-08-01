package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/observability"
	"solvify-agent/internal/repository"
	"solvify-agent/pkg/logger"
	"solvify-agent/pkg/tokenutil"
)

var tokenRegexp = regexp.MustCompile(`[\x{4e00}-\x{9fff}]+|[a-zA-Z0-9]+`)

type contextService struct {
	messageRepo repository.ChatMessageRepo
	memoryRepo  repository.UserMemoryRepo
	summaryRepo repository.SummaryRepo
	obs         observability.Recorder
}

func NewContextService(
	messageRepo repository.ChatMessageRepo,
	memoryRepo repository.UserMemoryRepo,
	summaryRepo repository.SummaryRepo,
	obs ...observability.Recorder,
) ContextServiceInterface {
	s := &contextService{
		messageRepo: messageRepo,
		memoryRepo:  memoryRepo,
		summaryRepo: summaryRepo,
	}
	if len(obs) > 0 && obs[0] != nil {
		s.obs = obs[0]
	}
	return s
}

func (s *contextService) SetObservability(obs observability.Recorder) {
	s.obs = obs
}

func (s *contextService) BuildContext(ctx context.Context, userID, sessionID, currentQuery string, cfg BuildContextConfig, chatModel model.BaseChatModel) (*EnhancedContext, error) {
	obsOk := s.obs != nil
	var span *observability.Span
	if obsOk {
		_, span = s.obs.StartSpan(ctx, "ctx.build", observability.ComponentServiceContext, observability.Attrs{
			"session_id": sessionID,
			"has_query":  fmt.Sprintf("%t", currentQuery != ""),
		})
		defer func() {
			if span != nil {
				s.obs.EndSpan(ctx, span, observability.SpanStatusOK, nil, nil)
			}
		}()
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 1500
	}
	if cfg.MaxMemories <= 0 {
		cfg.MaxMemories = 10
	}
	if cfg.MaxRecentMessages <= 0 {
		cfg.MaxRecentMessages = 20
	}
	if cfg.RetrievalBudget <= 0 {
		cfg.RetrievalBudget = 2000
	}
	if cfg.MemoryBudget <= 0 {
		cfg.MemoryBudget = 800
	}

	type loadResult struct {
		summary  *entity.ChatSummary
		memories []entity.UserMemory
		recent   []entity.ChatMessage
		err      error
	}

	resultCh := make(chan loadResult, 3)

	go func() {
		summary, err := s.summaryRepo.GetBySessionID(ctx, sessionID)
		resultCh <- loadResult{summary: summary, err: err}
	}()

	go func() {
		memories, err := s.memoryRepo.ListActive(ctx, userID, cfg.MaxMemories)
		resultCh <- loadResult{memories: memories, err: err}
	}()

	go func() {
		recent, err := s.messageRepo.FindRecent(ctx, sessionID, cfg.MaxRecentMessages)
		resultCh <- loadResult{recent: recent, err: err}
	}()

	var summary *entity.ChatSummary
	var memories []entity.UserMemory
	var recent []entity.ChatMessage
	for i := 0; i < 3; i++ {
		r := <-resultCh
		if r.err != nil {
			logger.Warnf("加载上下文组件失败: %v", r.err)
			continue
		}
		if r.summary != nil {
			summary = r.summary
		}
		if r.memories != nil {
			memories = r.memories
		}
		if r.recent != nil {
			recent = r.recent
		}
	}

	var relevant []entity.ChatMessage
	if currentQuery != "" {
		keywords := cfg.PreExtractedKeywords
		if len(keywords) == 0 {
			keywords = extractKeywords(currentQuery)
		}
		if len(keywords) > 0 {
			var err error
			relevant, err = s.messageRepo.SearchRecentByKeywords(ctx, sessionID, keywords, 5)
			if err != nil {
				logger.Warnf("检索相关历史失败: %v", err)
			}
		}
	}

	history := mergeMessages(recent, relevant)
	history = s.applySummary(history, summary)
	history = truncateHistoryByTokens(history, cfg.MaxTokens)
	memories = truncateMemoriesByTokens(memories, cfg.MemoryBudget)

	if obsOk && span != nil {
		if span.Attrs == nil {
			span.Attrs = observability.Attrs{}
		}
		span.Attrs["history_n"] = len(history)
		span.Attrs["memories_n"] = len(memories)
		span.Attrs["has_summary"] = summary != nil
	}

	return &EnhancedContext{
		History:         history,
		Summary:         summary,
		Memories:        memories,
		HistoryBudget:   cfg.MaxTokens,
		RetrievalBudget: cfg.RetrievalBudget,
	}, nil
}

func (s *contextService) SummarizeSession(ctx context.Context, sessionID string, chatModel model.BaseChatModel) (summary *entity.ChatSummary, retErr error) {
	if s == nil {
		return nil, nil
	}
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("SummarizeSession panic 已恢复: sessionID=%s, err=%v", sessionID, r)
			retErr = fmt.Errorf("summarize panic: %v", r)
			summary = nil
		}
	}()
	if s.messageRepo == nil || s.summaryRepo == nil {
		return nil, nil
	}
	if sessionID == "" || chatModel == nil {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	obsOk := s.obs != nil
	var span *observability.Span
	if obsOk {
		_, span = s.obs.StartSpan(ctx, "ctx.summarize", observability.ComponentServiceContext, observability.Attrs{"session_id": sessionID})
		defer func() {
			if span != nil {
				status := observability.SpanStatusOK
				var errVal error
				if retErr != nil {
					status = observability.SpanStatusError
					errVal = retErr
				}
				s.obs.EndSpan(ctx, span, status, errVal, nil)
			}
		}()
	}
	messages, err := s.messageRepo.FindBySessionIDForContext(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("加载会话消息失败: %w", err)
	}

	if len(messages) < 10 {
		return nil, nil
	}

	existing, err := s.summaryRepo.GetBySessionID(ctx, sessionID)
	if err != nil {
		logger.Warnf("查询会话摘要失败: %v", err)
	}

	var startIdx int
	if existing != nil && existing.LastMessageID != nil {
		for i, m := range messages {
			if m.ID == *existing.LastMessageID {
				startIdx = i + 1
				break
			}
		}
	}

	if startIdx >= len(messages) {
		return existing, nil
	}

	endIdx := len(messages) - 5
	if endIdx <= startIdx {
		endIdx = len(messages)
	}
	if endIdx > len(messages) {
		endIdx = len(messages)
	}

	summaryMessages := messages[startIdx:endIdx]
	if len(summaryMessages) < 5 {
		return existing, nil
	}

	dialogue := buildDialogueText(summaryMessages)
	summaryText, err := s.generateSummary(ctx, chatModel, dialogue, existing)
	if err != nil {
		if obsOk {
			s.obs.Incr(ctx, "ctx_summary_errors_total", nil, 1)
		}
		return nil, fmt.Errorf("生成摘要失败: %w", err)
	}

	lastMsgID := summaryMessages[len(summaryMessages)-1].ID
	coveredPrev := 0
	if existing != nil {
		coveredPrev = existing.CoveredCount
	}
	newSummary := &entity.ChatSummary{
		SessionID:     sessionID,
		Summary:       summaryText,
		CoveredCount:  len(summaryMessages) + coveredPrev,
		LastMessageID: &lastMsgID,
	}
	if existing != nil {
		newSummary.ID = existing.ID
	}

	if err := s.summaryRepo.Upsert(ctx, newSummary); err != nil {
		return nil, fmt.Errorf("保存摘要失败: %w", err)
	}
	if obsOk {
		s.obs.Incr(ctx, "ctx_summary_updates_total", nil, 1)
	}

	return newSummary, nil
}

func (s *contextService) ExtractMemories(ctx context.Context, userID, sessionID string, messages []entity.ChatMessage, chatModel model.BaseChatModel) (memories []entity.UserMemory, retErr error) {
	if s == nil {
		return nil, nil
	}
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("ExtractMemories panic 已恢复: userID=%s, sessionID=%s, err=%v", userID, sessionID, r)
			retErr = fmt.Errorf("extract memories panic: %v", r)
			memories = nil
		}
	}()
	if s.memoryRepo == nil {
		return nil, nil
	}
	if userID == "" || chatModel == nil {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	obsOk := s.obs != nil
	var span *observability.Span
	if obsOk {
		_, span = s.obs.StartSpan(ctx, "ctx.extract_memories", observability.ComponentServiceContext, observability.Attrs{
			"user_id": userID,
			"msgs_n":  fmt.Sprintf("%d", len(messages)),
		})
		defer func() {
			if span != nil {
				status := observability.SpanStatusOK
				var errVal error
				if retErr != nil {
					status = observability.SpanStatusError
					errVal = retErr
				}
				s.obs.EndSpan(ctx, span, status, errVal, nil)
			}
		}()
	}
	if len(messages) == 0 {
		return nil, nil
	}

	dialogue := buildDialogueText(messages)
	rawMemories, err := s.generateMemories(ctx, chatModel, dialogue)
	if err != nil {
		if obsOk {
			s.obs.Incr(ctx, "ctx_memory_errors_total", nil, 1)
		}
		return nil, fmt.Errorf("提取记忆失败: %w", err)
	}

	var result []entity.UserMemory
	for _, m := range rawMemories {
		m.UserID = userID
		if m.SourceSession == nil || *m.SourceSession == "" {
			m.SourceSession = &sessionID
		}
		m.Confidence = 1.0
		m.IsActive = true
		m.CreatedAt = time.Now()
		m.UpdatedAt = time.Now()

		if err := s.memoryRepo.Upsert(ctx, &m); err != nil {
			logger.Warnf("保存用户记忆失败: %v", err)
			continue
		}
		result = append(result, m)
	}
	if obsOk {
		s.obs.Incr(ctx, "ctx_memory_extracted_total", nil, int64(len(result)))
	}

	return result, nil
}

// applySummary 用摘要替换被摘要覆盖的早期消息
func (s *contextService) applySummary(messages []entity.ChatMessage, summary *entity.ChatSummary) []entity.ChatMessage {
	if summary == nil || summary.LastMessageID == nil {
		return messages
	}

	cutIdx := -1
	for i, m := range messages {
		if m.ID == *summary.LastMessageID {
			cutIdx = i
			break
		}
	}
	if cutIdx < 0 {
		return messages
	}

	// 把被覆盖的消息替换为一条 summary 消息
	summaryMsg := entity.ChatMessage{
		ID:        "summary-" + summary.SessionID,
		SessionID: summary.SessionID,
		Role:      "assistant",
		Content:   "【对话摘要】" + summary.Summary,
	}

	return append([]entity.ChatMessage{summaryMsg}, messages[cutIdx+1:]...)
}

// generateSummary 调用 LLM 生成摘要
func (s *contextService) generateSummary(ctx context.Context, chatModel model.BaseChatModel, dialogue string, existing *entity.ChatSummary) (string, error) {
	prompt := summarizePrompt(dialogue, existing)
	msg, err := chatModel.Generate(ctx, []*schema.Message{
		schema.SystemMessage(prompt),
		schema.UserMessage("请生成对话摘要："),
	})
	if err != nil {
		return "", err
	}
	if msg == nil {
		return "", fmt.Errorf("LLM 返回空消息")
	}
	return strings.TrimSpace(msg.Content), nil
}

// generateMemories 调用 LLM 提取记忆
func (s *contextService) generateMemories(ctx context.Context, chatModel model.BaseChatModel, dialogue string) ([]entity.UserMemory, error) {
	prompt := memoryExtractionPrompt()
	msg, err := chatModel.Generate(ctx, []*schema.Message{
		schema.SystemMessage(prompt),
		schema.UserMessage("以下是对话内容：\n\n" + dialogue),
	})
	if err != nil {
		return nil, err
	}
	if msg == nil || msg.Content == "" {
		return nil, nil
	}

	return parseMemories(msg.Content), nil
}

// summarizePrompt 生成摘要 Prompt
func summarizePrompt(dialogue string, existing *entity.ChatSummary) string {
	prefix := "你是对话摘要助手。请对以下对话生成简洁摘要，保留用户核心问题、系统关键结论、用户偏好/约束、未解决事项。"
	if existing != nil && existing.Summary != "" {
		prefix += "\n\n已有摘要：\n" + existing.Summary + "\n\n请基于已有摘要和新增对话生成新摘要。"
	}
	return prefix + "\n\n要求：\n- 不超过 300 字\n- 用第三人称客观描述\n- 不要遗漏关键决策信息\n\n对话内容：\n" + dialogue
}

// memoryExtractionPrompt 记忆提取 Prompt
func memoryExtractionPrompt() string {
	return `你是对话记忆提取助手。请从对话中提取值得长期保存的用户信息。
只提取以下类型：
- fact：用户明确说过的事实（如"我是财务部员工"）
- preference：用户偏好（如"喜欢用表格回答"）
- constraint：用户要求的约束条件（如"回答不要超过 300 字"）
- decision：已确认的决策结论

输出格式（每行一条 JSONL，不要输出任何解释）：
{"type": "fact", "content": "..."}
{"type": "preference", "content": "..."}

如果没有值得记忆的内容，输出空即可。`
}

// parseMemories 解析 LLM 输出的 JSONL 记忆
func parseMemories(content string) []entity.UserMemory {
	var memories []entity.UserMemory
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var item struct {
			Type    string `json:"type"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			logger.Warnf("解析记忆 JSON 失败: line=%q, err=%v", line, err)
			continue
		}
		if item.Type == "" || item.Content == "" {
			continue
		}
		memories = append(memories, entity.UserMemory{
			MemoryType: item.Type,
			Content:    item.Content,
		})
	}
	return memories
}

// buildDialogueText 把消息列表转成对话文本
func buildDialogueText(messages []entity.ChatMessage) string {
	var sb strings.Builder
	for _, m := range messages {
		switch m.Role {
		case "user":
			sb.WriteString("用户: ")
		case "assistant":
			sb.WriteString("助手: ")
		default:
			sb.WriteString(m.Role + ": ")
		}
		sb.WriteString(m.Content)
		sb.WriteString("\n")
	}
	return sb.String()
}

// extractKeywords 从查询中提取关键词
func extractKeywords(query string) []string {
	// 简单停用词表
	stopWords := map[string]struct{}{
		"的": {}, "了": {}, "是": {}, "我": {}, "你": {}, "他": {}, "她": {}, "它": {},
		"我们": {}, "你们": {}, "他们": {}, "这个": {}, "那个": {}, "什么": {}, "怎么": {},
		"为什么": {}, "如何": {}, "多少": {}, "哪些": {}, "谁": {}, "哪里": {}, "何时": {},
		"可以": {}, "能够": {}, "需要": {}, "想要": {}, "请": {}, "谢谢": {}, "你好": {},
		"这": {}, "那": {}, "什": {}, "么": {}, "怎": {}, "样": {},
		"a": {}, "an": {}, "the": {}, "is": {}, "are": {}, "was": {}, "were": {},
		"i": {}, "you": {}, "he": {}, "she": {}, "it": {}, "we": {}, "they": {},
		"this": {}, "that": {}, "these": {}, "those": {}, "what": {}, "how": {}, "why": {},
		"where": {}, "when": {}, "who": {}, "which": {}, "can": {}, "could": {}, "do": {},
		"does": {}, "did": {}, "will": {}, "would": {}, "should": {}, "may": {}, "might": {},
		"in": {}, "on": {}, "at": {}, "of": {}, "to": {}, "for": {}, "with": {},
	}

	// 按中文连续序列 或 英文/数字连续序列切分（复用包级已编译正则，避免每次重复编译）
	parts := tokenRegexp.FindAllString(query, -1)

	seen := make(map[string]struct{})
	var keywords []string
	for _, p := range parts {
		p = strings.TrimSpace(strings.ToLower(p))
		if p == "" {
			continue
		}
		if _, ok := stopWords[p]; ok {
			continue
		}
		if len([]rune(p)) < 2 {
			continue
		}
		// 纯中文且每个字都是停用词，则跳过
		if isChineseString(p) && allRunesInSet(p, stopWords) {
			continue
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		keywords = append(keywords, p)
	}

	// 最多返回 5 个关键词
	if len(keywords) > 5 {
		keywords = keywords[:5]
	}
	return keywords
}

// isChineseString 判断字符串是否全部由中文组成
func isChineseString(s string) bool {
	for _, r := range s {
		if r < '\u4e00' || r > '\u9fff' {
			return false
		}
	}
	return true
}

// allRunesInSet 判断字符串中每个 rune 是否都在集合中
func allRunesInSet(s string, set map[string]struct{}) bool {
	for _, r := range s {
		if _, ok := set[string(r)]; !ok {
			return false
		}
	}
	return true
}

// truncateMemoriesByTokens 按 token 预算截断记忆，优先保留最近更新的记忆
func truncateMemoriesByTokens(memories []entity.UserMemory, maxTokens int) []entity.UserMemory {
	if maxTokens <= 0 {
		return memories
	}

	// 已按 updated_at DESC 从仓库返回，从最后一条往前加，保留最新的
	var total int
	var result []entity.UserMemory
	for i := len(memories) - 1; i >= 0; i-- {
		cost := tokenutil.Estimate(memories[i].Content)
		if total+cost > maxTokens {
			break
		}
		total += cost
		result = append([]entity.UserMemory{memories[i]}, result...)
	}
	return result
}

// mergeMessages 合并两组消息并去重，按时间排序
func mergeMessages(a, b []entity.ChatMessage) []entity.ChatMessage {
	seen := make(map[string]struct{})
	var result []entity.ChatMessage

	add := func(msgs []entity.ChatMessage) {
		for _, m := range msgs {
			if _, ok := seen[m.ID]; ok {
				continue
			}
			seen[m.ID] = struct{}{}
			result = append(result, m)
		}
	}

	add(a)
	add(b)

	// 按创建时间排序
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})

	return result
}
