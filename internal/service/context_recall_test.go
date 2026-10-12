package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
)

// ─── 语义召回：旧消息与当前问题无关键词重叠时仍要能被召回到 ────────────────
//
// 回归背景：语义召回（SearchRecentByVector）此前接上了但**实际无效**，两个原因叠在一起：
//
//  1. 查询侧向量从来没算过 —— BuildContext 的 4 路并行里只有关键词召回，
//     语义那一路连参数都没有，是个空通道；
//  2. 就算接上了，RecallHistory 与近期对话是**同一个容器**，
//     装配完按时间从尾部裁 —— 而召回命中的必然是老消息（排在头部），第一个被裁掉。
//
// 「多轮指代」正是只能靠语义召回的场景：用户问「那这个的替代方案呢」，
// 与二十轮前那句「我们用了 X 方案」没有任何字面重叠，关键词通道必然为空。
// 所以这里的测试数据必须**零关键词重叠**，否则用例在关键词通道也成立 = 恒真。

// recallTestMessageRepo 是只实现召回所需方法的 ChatMessageRepo 替身。
// 嵌入接口而非逐个方法实现，是为了让「新增方法忘了改替身」变成编译期错误
// （未实现的方法一被调用就 nil panic，指向「测试没覆盖到那条路」而不是静默返回零值）。
type recallTestMessageRepo struct {
	repository.ChatMessageRepo

	recent    []entity.ChatMessage
	kwHits    []entity.ChatMessage
	vecHits   []entity.ChatMessage
	vecCalls  int
	vecLastQ  []float32
	kwLastKws []string
}

func (r *recallTestMessageRepo) FindRecent(context.Context, string, int) ([]entity.ChatMessage, error) {
	return r.recent, nil
}

func (r *recallTestMessageRepo) SearchRecentByKeywords(_ context.Context, _ string, keywords []string, _ int) ([]entity.ChatMessage, error) {
	r.kwLastKws = keywords
	return r.kwHits, nil
}

func (r *recallTestMessageRepo) SearchRecentByVector(_ context.Context, _ string, q []float32, _ int, _ float64) ([]entity.ChatMessage, error) {
	r.vecCalls++
	r.vecLastQ = q
	return r.vecHits, nil
}

// recallTestSummaryRepo / recallTestMemoryRepo 只实现 ListActive 这一个方法（其余是同步/写路径，
// BuildContext 用不到）。显式实现而不是嵌接口：这两个接口小，嵌入反而看不出「到底覆盖了谁」。
type recallTestSummaryRepo struct {
	repository.SummaryRepo
}

func (recallTestSummaryRepo) GetBySessionID(context.Context, string) (*entity.ChatSummary, error) {
	return nil, nil
}

type recallTestMemoryRepo struct {
	repository.UserMemoryRepo
}

func (recallTestMemoryRepo) ListActive(context.Context, string, int) ([]entity.UserMemory, error) {
	return nil, nil
}

// recallTestService 组一个只依赖召回相关方法的 contextService。
func recallTestService(repo *recallTestMessageRepo) *contextService {
	return &contextService{
		messageRepo: repo,
		summaryRepo: recallTestSummaryRepo{},
		memoryRepo:  recallTestMemoryRepo{},
	}
}

// 「二十轮前聊过的方案」与「那这个的替代方案呢」零字面重叠 —— 这条断言的全部意义所在。
const (
	recallOldMsgID = "old-msg-1"
	recallOldText  = "我们内部管线目前用的是 Kettle 那套抽取工具"
	recallQuery    = "那这个的替代方案呢"
)

// 核心用例：语义召回命中的老消息必须出现在最终模型输入里。
func TestSemanticRecallSurvivesIntoModelInput(t *testing.T) {
	old := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	now := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)

	repo := &recallTestMessageRepo{
		// 近期对话里没有那条老消息（否则它会走近期通道进模型，用例就测不到召回了）
		recent: []entity.ChatMessage{
			{ID: "new-1", Role: "user", Content: "今天天气不错", CreatedAt: now},
			{ID: "new-2", Role: "assistant", Content: "是啊", CreatedAt: now.Add(time.Second)},
		},
		kwHits:  nil, // 关键词通道确实没命中（真实场景下就是这样）
		vecHits: []entity.ChatMessage{{ID: recallOldMsgID, Role: "user", Content: recallOldText, CreatedAt: old}},
	}
	svc := recallTestService(repo)
	chatSvc := &chatService{}

	// 先钉住前提：这条老消息与当前问题没有共同词项 —— 否则本用例恒真
	kw := extractKeywords(recallQuery)
	for _, m := range repo.vecHits {
		for _, w := range kw {
			if strings.Contains(m.Content, w) {
				t.Fatalf("测试前提不成立：老消息含关键词 %q，关键词通道也能召回 → 本用例恒真", w)
			}
		}
	}

	enhanced, err := svc.BuildContext(context.Background(), "u1", "s1", recallQuery, BuildContextConfig{
		MaxTokens:      4000,
		ModelName:      "gpt-4",
		QueryEmbedding: []float64{0.1, 0.2, 0.3},
	}, nil)
	if err != nil {
		t.Fatalf("BuildContext: %v", err)
	}

	// 语义通道必须真的被调用，且拿到的是调用方传入的查询向量
	if repo.vecCalls != 1 {
		t.Fatalf("语义召回被调用 %d 次，期望 1 次 —— 查询向量没传进来，或这一路是死代码", repo.vecCalls)
	}
	if len(repo.vecLastQ) != 3 {
		t.Fatalf("传给语义召回的查询向量长度=%d，期望 3（应原样使用 cfg.QueryEmbedding）", len(repo.vecLastQ))
	}

	history := chatSvc.modelHistory(enhanced, "")

	var found bool
	for _, m := range history {
		if m.ID == recallOldMsgID {
			found = true
			if !strings.Contains(m.Content, recallOldText) {
				t.Errorf("召回到的消息正文被改坏了：%q", m.Content)
			}
			// 标注必须带来源与时间，否则模型会把它当成「刚刚发生的对话」
			if !strings.Contains(m.Content, "历史片段") {
				t.Errorf("召回消息缺少来源标注，模型无法区分它是历史片段：%q", m.Content)
			}
			if !strings.Contains(m.Content, "2026-09-01") {
				t.Errorf("召回消息缺少时间标注：%q", m.Content)
			}
		}
	}
	if !found {
		ids := make([]string, 0, len(history))
		for _, m := range history {
			ids = append(ids, m.ID)
		}
		t.Fatalf("语义召回命中的老消息没有进入模型输入（最终消息 ID: %v）", ids)
	}
}

// 反向对照：查询向量为空时语义通道必须关闭 —— 否则召回侧会拿空向量去查库。
// 没有它，上一条可能在「没传向量也照样召回」的错误实现下依然全绿。
func TestSemanticRecallDisabledWithoutQueryEmbedding(t *testing.T) {
	repo := &recallTestMessageRepo{
		vecHits: []entity.ChatMessage{{ID: recallOldMsgID, Content: recallOldText}},
	}
	svc := recallTestService(repo)

	enhanced, err := svc.BuildContext(context.Background(), "u1", "s1", recallQuery, BuildContextConfig{
		MaxTokens: 4000,
		ModelName: "gpt-4",
		// QueryEmbedding 缺省
	}, nil)
	if err != nil {
		t.Fatalf("BuildContext: %v", err)
	}
	if repo.vecCalls != 0 {
		t.Errorf("查询向量为空却仍发起了 %d 次语义召回 —— 会拿空向量查库，报维度错误", repo.vecCalls)
	}
	if len(enhanced.RecallHistory) != 0 {
		t.Errorf("查询向量为空却召回了 %d 条历史", len(enhanced.RecallHistory))
	}
}

// 预算隔离：召回容器与近期对话各有预算，召回的老消息不该挤掉近期对话。
// 旧实现是「合并后按时间从尾部裁」，召回命中的老消息会被第一个裁掉 —— 本用例守住修复。
func TestRecallAndRecentHaveSeparateBudgets(t *testing.T) {
	old := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	now := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)

	// 近期对话很长（足够吃满 recentBudget），召回侧只有一条老消息
	var recent []entity.ChatMessage
	for i := 0; i < 40; i++ {
		recent = append(recent, entity.ChatMessage{
			ID:        "recent-" + string(rune('a'+i)),
			Role:      "user",
			Content:   strings.Repeat("很长的近期内容", 30),
			CreatedAt: now.Add(time.Duration(i) * time.Second),
		})
	}

	repo := &recallTestMessageRepo{
		recent:  recent,
		vecHits: []entity.ChatMessage{{ID: recallOldMsgID, Role: "user", Content: recallOldText, CreatedAt: old}},
	}
	svc := recallTestService(repo)
	chatSvc := &chatService{}

	const totalBudget = 2000
	enhanced, err := svc.BuildContext(context.Background(), "u1", "s1", recallQuery, BuildContextConfig{
		MaxTokens:      totalBudget,
		ModelName:      "gpt-4",
		QueryEmbedding: []float64{0.1},
	}, nil)
	if err != nil {
		t.Fatalf("BuildContext: %v", err)
	}

	if enhanced.RecallBudget <= 0 || enhanced.HistoryBudget <= 0 {
		t.Fatalf("两个容器都应有独立预算：HistoryBudget=%d RecallBudget=%d", enhanced.HistoryBudget, enhanced.RecallBudget)
	}
	if enhanced.HistoryBudget+enhanced.RecallBudget != totalBudget {
		t.Errorf("两个容器预算之和=%d，期望等于总预算 %d（必须是把预算分开，而不是各自拿满）",
			enhanced.HistoryBudget+enhanced.RecallBudget, totalBudget)
	}

	// 关键断言：近期对话被裁到只剩很少几条时，召回的老消息仍然在
	history := chatSvc.modelHistory(enhanced, "")
	var recallKept, recentKept int
	for _, m := range history {
		if m.ID == recallOldMsgID {
			recallKept++
		} else if strings.HasPrefix(m.ID, "recent-") {
			recentKept++
		}
	}
	if recallKept == 0 {
		t.Fatalf("近期对话占了 %d 条、召回的老消息却一条没留 —— 这就是「合并后按时间裁」的老问题", recentKept)
	}
	if recentKept == 0 {
		t.Error("近期对话被裁光了：它是必留内容（当前问题的上下文）")
	}
}

// 标注不能污染近期对话原文：refreshContextAsync 消费的是 enhancedCtx.History，
// 摘要与记忆抽取拿到的是干净原文。
func TestRecallMarkerDoesNotPolluteRecentHistory(t *testing.T) {
	now := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	repo := &recallTestMessageRepo{
		recent: []entity.ChatMessage{{ID: "new-1", Role: "user", Content: "今天天气不错", CreatedAt: now}},
		vecHits: []entity.ChatMessage{{ID: recallOldMsgID, Role: "user", Content: recallOldText,
			CreatedAt: now.Add(-time.Hour)}},
	}
	svc := recallTestService(repo)
	chatSvc := &chatService{}

	enhanced, err := svc.BuildContext(context.Background(), "u1", "s1", recallQuery, BuildContextConfig{
		MaxTokens: 4000, ModelName: "gpt-4", QueryEmbedding: []float64{0.1},
	}, nil)
	if err != nil {
		t.Fatalf("BuildContext: %v", err)
	}

	// 模型侧那份带标注
	for _, m := range chatSvc.modelHistory(enhanced, "") {
		if m.ID == "new-1" && strings.Contains(m.Content, "历史片段") {
			t.Errorf("近期对话被加了召回标注：%q", m.Content)
		}
	}
	// 摘要/记忆侧那份必须是原文
	for _, m := range enhanced.History {
		if m.ID == "new-1" && m.Content != "今天天气不错" {
			t.Errorf("enhancedCtx.History 被污染了（它要喂给摘要与记忆抽取）：%q", m.Content)
		}
	}
}

// 本轮刚落库的 user 消息不能出现在历史里（否则重复喂给模型）。
func TestModelHistoryExcludesCurrentUserMessage(t *testing.T) {
	now := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	const curID = "just-inserted"
	repo := &recallTestMessageRepo{
		recent: []entity.ChatMessage{
			{ID: "old", Role: "user", Content: "上一轮问题", CreatedAt: now.Add(-time.Minute)},
			{ID: curID, Role: "user", Content: "本轮问题", CreatedAt: now},
		},
	}
	svc := recallTestService(repo)
	chatSvc := &chatService{}

	enhanced, err := svc.BuildContext(context.Background(), "u1", "s1", "本轮问题", BuildContextConfig{
		MaxTokens: 4000, ModelName: "gpt-4",
	}, nil)
	if err != nil {
		t.Fatalf("BuildContext: %v", err)
	}

	for _, m := range chatSvc.modelHistory(enhanced, curID) {
		if m.ID == curID {
			t.Errorf("本轮刚落库的 user 消息 %q 出现在历史里，会被重复喂给模型", m.ID)
		}
	}
}

// 接线用例：查询向量必须真的进到 BuildContextConfig 里。
//
// 这条与上面几条性质不同 —— 上面守的是「BuildContext 收到向量后怎么用」，
// 这里守的是「initContext 有没有把向量传进去」。曾用「不传向量」的注入验证过
// 上面的用例对这份接线免疫（注入改了 chat_service.go，全部单测仍全绿），
// 因为它们直接构造 contextService、自己传 QueryEmbedding，压根不经过 initContext。
// 所以接线必须单独有一条用例，否则语义召回可以整条断掉而测试察觉不到。
func TestBuildContextParamsCarriesQueryEmbedding(t *testing.T) {
	vec := []float64{0.11, 0.22, 0.33}

	cfg := buildContextParams(4000, 2000, 800, "gpt-4", 500, vec)

	if len(cfg.QueryEmbedding) != len(vec) {
		t.Fatalf("QueryEmbedding 长度=%d，期望 %d —— 查询向量没接进 BuildContextConfig，语义召回是死通道",
			len(cfg.QueryEmbedding), len(vec))
	}
	for i := range vec {
		if cfg.QueryEmbedding[i] != vec[i] {
			t.Fatalf("QueryEmbedding[%d]=%v，期望 %v", i, cfg.QueryEmbedding[i], vec[i])
		}
	}
	// 顺带钉住同一次组装里的其余字段：抽出来的组装点不该把任何一项丢掉
	if cfg.MaxTokens != 4000 || cfg.RetrievalBudget != 2000 || cfg.MemoryBudget != 800 {
		t.Errorf("预算字段在组装时串位: %+v", cfg)
	}
	if cfg.ModelName != "gpt-4" || cfg.ToolsTokens != 500 {
		t.Errorf("模型名/工具 token 在组装时串位: %+v", cfg)
	}
}

// queryEmbedding 的失败语义：必须返回 nil 而不是空非 nil 切片，
// 因为 BuildContext 用的是 len() > 0 判据，返回空切片与返回 nil 行为一致但意图易混；
// 更重要的是它必须**不 panic**（embedFn 为 nil 时直接返回）。
func TestQueryEmbeddingReturnsNilWhenUnavailable(t *testing.T) {
	svc := &chatService{} // embedFn 为 nil

	if got := svc.queryEmbedding(context.Background(), "随便什么问题"); got != nil {
		t.Errorf("embedFn 未配置时应返回 nil，实际 %v", got)
	}
	if got := svc.queryEmbedding(context.Background(), "  "); got != nil {
		t.Errorf("空白 query 应返回 nil，实际 %v", got)
	}
}

// 同一条消息既在近期又在召回时，只能出现一次 —— 否则模型看到同一句话出现两遍。
func TestModelHistoryDedupsOverlapBetweenRecentAndRecall(t *testing.T) {
	ts := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	const dupID = "both"
	repo := &recallTestMessageRepo{
		recent: []entity.ChatMessage{{ID: dupID, Role: "user", Content: "重复出现的内容", CreatedAt: ts}},
		vecHits: []entity.ChatMessage{{ID: dupID, Role: "user", Content: "重复出现的内容", CreatedAt: ts}},
	}
	svc := recallTestService(repo)
	chatSvc := &chatService{}

	enhanced, err := svc.BuildContext(context.Background(), "u1", "s1", recallQuery, BuildContextConfig{
		MaxTokens: 4000, ModelName: "gpt-4", QueryEmbedding: []float64{0.1},
	}, nil)
	if err != nil {
		t.Fatalf("BuildContext: %v", err)
	}

	var n int
	for _, m := range chatSvc.modelHistory(enhanced, "") {
		if m.ID == dupID {
			n++
		}
	}
	if n != 1 {
		t.Errorf("同一条消息出现 %d 次，期望 1 次", n)
	}
}
