package service

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/rag"
	"solvify-agent/internal/repository"
	"solvify-agent/pkg/logger"
	"solvify-agent/pkg/tokenutil"
)

// contextService 上下文管理服务实现
type contextService struct {
	messageRepo repository.ChatMessageRepo
	memoryRepo  repository.UserMemoryRepo
	summaryRepo repository.SummaryRepo
}

// NewContextService 创建上下文管理服务。
func NewContextService(
	messageRepo repository.ChatMessageRepo,
	memoryRepo repository.UserMemoryRepo,
	summaryRepo repository.SummaryRepo,
) ContextServiceInterface {
	return &contextService{
		messageRepo: messageRepo,
		memoryRepo:  memoryRepo,
		summaryRepo: summaryRepo,
	}
}

// BuildContext 构建增强后的对话上下文
func (s *contextService) BuildContext(ctx context.Context, userID, sessionID, currentQuery string, cfg BuildContextConfig, chatModel model.BaseChatModel) (*EnhancedContext, error) {
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
		summary   *entity.ChatSummary
		memories  []entity.UserMemory
		recent    []entity.ChatMessage
		kwRecall  []entity.ChatMessage
		vecRecall []entity.ChatMessage
		err       error
	}

	// 提前算好关键词——纯计算，直接在主线程做
	var keywords []string
	if currentQuery != "" {
		keywords = extractKeywords(currentQuery)
	}

	resultCh := make(chan loadResult, 5)

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

	go func() {
		var relevant []entity.ChatMessage
		if len(keywords) > 0 {
			var err error
			relevant, err = s.messageRepo.SearchRecentByKeywords(ctx, sessionID, keywords, recallTopK)
			if err != nil {
				logger.Warnf("关键词召回失败: %v", err)
			}
		}
		resultCh <- loadResult{kwRecall: relevant}
	}()

	// 语义召回通道：查询侧向量由调用方传入（决策 ① = 当前问题）。
	// 两路都失败时只剩近期对话，仍能正常回答 —— 召回是补充通道，不是主通道。
	go func() {
		var recalled []entity.ChatMessage
		if len(cfg.QueryEmbedding) > 0 {
			vec := make([]float32, len(cfg.QueryEmbedding))
			for i, v := range cfg.QueryEmbedding {
				vec[i] = float32(v)
			}
			matched, err := s.messageRepo.SearchRecentByVector(ctx, sessionID, vec, recallTopK, 0)
			if err != nil {
				logger.Warnf("语义召回失败（关键词通道不受影响）: %v", err)
			} else {
				recalled = matched
			}
		}
		resultCh <- loadResult{vecRecall: recalled}
	}()

	var (
		summary   *entity.ChatSummary
		memories  []entity.UserMemory
		recent    []entity.ChatMessage
		kwRecall  []entity.ChatMessage
		vecRecall []entity.ChatMessage
	)
	for i := 0; i < 5; i++ {
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
		if r.kwRecall != nil {
			kwRecall = r.kwRecall
		}
		if r.vecRecall != nil {
			vecRecall = r.vecRecall
		}
	}

	// ── 两个容器各自裁剪，不合并后再裁 ──
	// 旧顺序是「合并 → 摘要过滤 → 从尾部裁」，而召回的必然是老消息（排在头部），
	// 于是每次都被第一个裁掉：语义召回接上了也没有实际效果。
	recentBudget, recallBudget := splitHistoryBudget(cfg.MaxTokens)
	history := truncateHistoryByTokens(s.applySummary(recent, summary), recentBudget, cfg.ModelName)
	recall := buildRecallContainer(kwRecall, vecRecall, recallBudget, cfg.ModelName)
	memories = truncateMemoriesByTokens(memories, cfg.MemoryBudget, cfg.ModelName)

	return &EnhancedContext{
		History:         history,
		RecallHistory:   recall,
		Summary:         summary,
		Memories:        memories,
		HistoryBudget:   recentBudget,
		RecallBudget:    recallBudget,
		RetrievalBudget: cfg.RetrievalBudget,
	}, nil
}

// recallTopK 单条召回通道取多少条候选。两条通道的候选合并去重后再按预算裁。
const recallTopK = 5

// summaryTailKeep 摘要永远不覆盖末尾这么多条消息（它们以原文留在历史里）。
const summaryTailKeep = 5

// summaryMinSegment 少于这么多条就不值得摘要：压缩损耗大于收益。
const summaryMinSegment = 5

// summaryMaxInputTokens 送进摘要模型的那段对话的 token 上限。
//
// 超了必须截，原因是硬性的：SummarizeSession 跑在**请求关键路径之外的**后台协程里，
// 而它调用的是与主链路同一个 chatModel。一段 30000 token 的对话摘要请求会把模型
// 打到接近窗口上限，既可能直接报错，也会和用户的正常请求抢配额 —— 表现为
// 「用户问得好好的，突然收到一次上下文超长的错误」。
//
// 截断策略是**保留最近的一段**（用户刚说的内容比几十轮前的更重要），
// 与 truncateHistoryByTokens 的取舍一致。
const summaryMaxInputTokens = 6000

// recallBudgetRatio 是「相关历史」容器占历史总预算的比例。
// 近期对话是必留的（当前这轮的问题与紧跟其后的答案），召回只是补充，所以大头留给近期；
// 剩下的这一份在默认预算下够放 5~8 条片段。
const recallBudgetRatio = 0.35

// splitHistoryBudget 把历史总预算拆成「近期对话 + 相关历史」两份。
func splitHistoryBudget(total int) (recent, recall int) {
	if total <= 0 {
		return 0, 0
	}
	recall = int(float64(total) * recallBudgetRatio)
	if recall < 1 && total > 1 {
		recall = 1
	}
	if recall >= total {
		recall = total / 2
	}
	return total - recall, recall
}

// buildRecallContainer 合并关键词与语义两路召回结果，按自己的预算裁剪。
//
// 与近期对话分开预算的原因只有一个：老消息在「按时间从尾部裁」里必然先被裁。
// 这里反过来——**按相关度保留最靠前的**，被裁掉的永远是最不相关的那几条。
func buildRecallContainer(kw, vec []entity.ChatMessage, budget int, modelName string) []entity.ChatMessage {
	if budget <= 0 {
		return nil
	}
	// 语义召回在前、关键词在后：语义分更可信，同一条被两路命中时靠前者胜出。
	merged := dedupMessages(append(append([]entity.ChatMessage{}, vec...), kw...))
	if len(merged) == 0 {
		return nil
	}

	// 逐条累加，超预算即停（保留的是最相关的前缀）。
	out := make([]entity.ChatMessage, 0, len(merged))
	total := 0
	for _, m := range merged {
		cost := tokenutil.CountTokens(m.Content, modelName)
		if total+cost > budget {
			break
		}
		total += cost
		out = append(out, m)
	}
	return out
}

// dedupMessages 按 ID 去重并按时间正序排列（同值时按 ID 裁决，保证全序）。
func dedupMessages(msgs []entity.ChatMessage) []entity.ChatMessage {
	seen := make(map[string]struct{}, len(msgs))
	out := make([]entity.ChatMessage, 0, len(msgs))
	for _, m := range msgs {
		if _, ok := seen[m.ID]; ok {
			continue
		}
		seen[m.ID] = struct{}{}
		out = append(out, m)
	}
	return sortMessagesByTime(out)
}

// SummarizeSession 对会话生成或更新摘要
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

	// 覆盖边界：**摘要只覆盖 [startIdx, endIdx)**，末尾 5 条永远留在明文历史里。
	//
	// 为什么要留尾巴：摘要是对话的低保真压缩，最近几轮的具体措辞（用户刚给的参数、
	// 上一句的指代对象）经过一次压缩就会失真，而这几轮恰恰是模型最该看到原文的。
	// 留 5 条 ≈ 最近 2~3 轮，是个经验值：少于 2 轮接不上话，多了摘要就名存实亡。
	//
	// ⚠️ 这里用 endIdx（开区间）而不是「最后一条的下标」：写错一个 off-by-one 会让
	// 最后一条消息既在摘要里、又在明文历史里，模型看到同一段对话出现两次。
	// CoveredCount 记的是区间长度，与 LastMessageID 指向的**那条**保持一致。
	endIdx := len(messages) - summaryTailKeep
	if endIdx <= startIdx {
		// 尾部就够长时没有可摘要的区间 —— 直接返回已有摘要，不做无意义的重算
		return existing, nil
	}
	if endIdx > len(messages) {
		endIdx = len(messages)
	}

	summaryMessages := messages[startIdx:endIdx]
	if len(summaryMessages) < summaryMinSegment {
		return existing, nil
	}

	dialogue, ok := clampSummaryInput(buildDialogueText(summaryMessages))
	if !ok {
		// 截完连一行都放不下：与其发一次必然失败的请求，不如跳过这一轮，
		// 等更多消息进来后区间自然变大。
		logger.Warnf("[Summary] 会话 %s 的待摘要文本在 %d token 内放不下，本轮跳过", sessionID, summaryMaxInputTokens)
		return existing, nil
	}
	summaryText, err := s.generateSummary(ctx, chatModel, dialogue, existing)
	if err != nil {
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
	return newSummary, nil
}

// clampSummaryInput 把待摘要的对话文本压到 summaryMaxInputTokens 以内。
//
// 保留**尾部**（最近的说法）而不是头部：一轮对话里用户最新的要求、模型最后给的
// 结论，信息量集中在末尾；开头常常是已被后续内容推翻的中间结论。
//
// 返回 false 表示「压完已经不成其为文本」，调用方应当跳过这一轮而不是发空请求 ——
// 与其让摘要请求必然失败（并把同一个错误报到用户那次聊天的日志里），不如等
// 区间自然变大再摘要。
//
// ⚠️ 按 rune 而不是按 token 估：tokenutil 的精确切分要求能定位 token 边界，
// 而这里是在**已拼好的对话文本**上截断，从中间切断会把一句话劈成两半；
// 用 0.75 的比例给「rune→token」的波动留余量，宁可少截也不让请求超窗。
func clampSummaryInput(dialogue string) (string, bool) {
	const runeToTokenRatio = 0.75

	if strings.TrimSpace(dialogue) == "" {
		return "", false
	}
	if tokenutil.CountTokens(dialogue, "") <= summaryMaxInputTokens {
		return dialogue, true
	}

	runes := []rune(dialogue)
	keep := int(float64(summaryMaxInputTokens) * runeToTokenRatio)
	if keep < 1 {
		keep = 1
	}
	if keep >= len(runes) {
		return dialogue, true
	}
	cut := strings.TrimSpace(string(runes[len(runes)-keep:]))
	if cut == "" {
		return "", false
	}
	return cut, true
}

// ExtractMemories 从消息中提取用户长期记忆
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

	if len(messages) == 0 {
		return nil, nil
	}

	dialogue := buildDialogueText(messages)
	rawMemories, err := s.generateMemories(ctx, chatModel, dialogue)
	if err != nil {
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
	return result, nil
}

// applySummary 移除已被摘要覆盖的早期消息，只保留"摘要分界点之后的新消息尾"。
//
// 关键修复：不再伪造一条 Role=assistant 的「摘要消息」塞进历史。
// 旧做法会导致模型把摘要误判为"自己前一轮已经输出过的内容"，直接产生幻觉（比如
// 明明没有回答过某问题，却因为摘要里提到，就接着往下编）。
//
// 正确做法：摘要内容由 PromptBuilder.BuildSystem() 注入到 System Prompt，
// 与用户画像/偏好同层语义，统一且无歧义。
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

	// 保证新历史的第一条始终是 user（防止"截断后只剩半截 assistant"的非法开头）
	first := cutIdx + 1
	for first < len(messages) && messages[first].Role == "assistant" {
		first++
	}
	if first >= len(messages) {
		return nil
	}
	return messages[first:]
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

// maxHistoryKeywords 「相关历史」通道最多使用的关键词条数：每个词项都会变成一个 ILIKE 条件
const maxHistoryKeywords = 5

// extractKeywords 提取「相关历史」通道用的关键词。
//
// 必须复用 rag.ExtractKeywords（gse 分词），不要在这里另写一套切词规则：
// 旧实现用 tokenRegexp = [\x{4e00}-\x{9fff}]+ 切分，会把一整段连续中文当成**一个**词项，
// 于是 SearchRecentByKeywords 真正执行的是 content ILIKE '%那网络安全这块你们是怎么做的%'
// —— 历史消息里不可能出现这么长的整串，一条都匹配不上，「相关历史」通道等于空转；
// gse 切出的短词（"网络安全"）才可能命中。
//
// 第二个理由是口径统一：关键词召回与知识库检索用的是同一套切词规则，
// 两条通道的「词」才是一回事；否则同一个词在两路里指不同的东西。
func extractKeywords(query string) []string {
	keywords := rag.ExtractKeywords(query)
	if len(keywords) > maxHistoryKeywords {
		keywords = keywords[:maxHistoryKeywords]
	}
	return keywords
}

// truncateMemoriesByTokens 按真 BPE token 预算截断记忆，优先保留"重要度+更新时间"综合靠前的。
//
// 关键修复（P1-⑥）：旧实现直接按 updated_at 倒序 TopK，会把和当前 query 完全无关的记忆硬塞，
// 污染 prompt。这里先用 Importance×Recency 粗排，再按真 BPE 从顶塞到预算。
// 当前 query 相关度判断在 P1 级改造会走 embedding 语义召回，这里先保证不超预算。
func truncateMemoriesByTokens(memories []entity.UserMemory, maxTokens int, modelName string) []entity.UserMemory {
	if maxTokens <= 0 {
		return nil
	}
	if len(memories) == 0 {
		return memories
	}

	type scored struct {
		idx int
		sc  float64
	}
	scoredList := make([]scored, 0, len(memories))
	now := time.Now()
	for i, m := range memories {
		imp := m.Confidence
		if imp <= 0 {
			imp = 1
		}
		// 简单 recency：更新时间 30 天内不打折，超过按 1/(1+days/30) 衰减
		days := now.Sub(m.UpdatedAt).Hours() / 24
		if days < 0 {
			days = 0
		}
		rec := 1.0
		if days > 30 {
			rec = 1.0 / (1.0 + (days-30)/30.0)
		}
		scoredList = append(scoredList, scored{i, imp * rec})
	}
	// 稳定排序：得分降序，同分用 UpdatedAt 新的在前
	sort.SliceStable(scoredList, func(a, b int) bool {
		if scoredList[a].sc != scoredList[b].sc {
			return scoredList[a].sc > scoredList[b].sc
		}
		return memories[scoredList[a].idx].UpdatedAt.After(memories[scoredList[b].idx].UpdatedAt)
	})

	var total int
	result := make([]entity.UserMemory, 0, len(scoredList))
	for _, it := range scoredList {
		m := memories[it.idx]
		cost := tokenutil.CountTokens(m.Content, modelName)
		if total+cost > maxTokens {
			remain := maxTokens - total
			if remain >= 40 {
				cut, actual := tokenutil.TruncateByTokens(m.Content, modelName, remain)
				if actual > 0 {
					m.Content = cut + "（记忆过长已截断）"
					result = append(result, m)
					total += actual
				}
			}
			break
		}
		total += cost
		result = append(result, m)
	}
	return result
}

// mergeMessages 合并两组消息并去重，按时间排序
//
// 已无生产调用者（相关历史改成独立容器后由 dedupMessages 承担），
// 仅保留给既有测试 —— 删掉它会让「同创建时间按 ID 裁决」这条规则失去唯一的测试锚点。
func mergeMessages(a, b []entity.ChatMessage) []entity.ChatMessage {
	merged := make([]entity.ChatMessage, 0, len(a)+len(b))
	merged = append(merged, a...)
	merged = append(merged, b...)
	return dedupMessages(merged)
}
