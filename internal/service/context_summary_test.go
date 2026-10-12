package service

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
	"solvify-agent/pkg/tokenutil"
)

// ─── 摘要输入限长 ─────────────────────────────────────────────────────────
//
// 回归背景：SummarizeSession 把 [startIdx, endIdx) 整段拼成文本丢给模型，
// 段长没有任何上限。一段 30000 token 的对话摘要请求会把模型打到接近窗口上限，
// 表现为「用户问得好好的，突然收到一次上下文超长的错误」——
// 因为它跑在后台，用的是**同一个 chatModel**，与用户的正常请求抢配额。

// 超长文本必须被压到上限以内，且保留尾部（最近的说法信息量最大）。
func TestClampSummaryInputCapsLongDialogue(t *testing.T) {
	// 头部有独特标记，用来验证「保留尾部」这个取舍
	head := "【很早的开头】"
	body := strings.Repeat("这是一段对话内容，", 5000) // 远超 6000 token
	tail := "【最近的关键结论】"
	dialogue := head + body + tail

	got, ok := clampSummaryInput(dialogue)
	if !ok {
		t.Fatal("超长但非空的对话不该被判定为不可摘要")
	}
	if cost := tokenCountForTest(got); cost > summaryMaxInputTokens {
		t.Errorf("压完仍有 %d token，超出上限 %d", cost, summaryMaxInputTokens)
	}
	if !strings.Contains(got, "【最近的关键结论】") {
		t.Error("尾部（最近的说法）被截掉了 —— 摘要会丢掉用户最新的要求")
	}
	if strings.Contains(got, head) {
		t.Error("保留了最老的开头而不是最近的内容，取舍反了")
	}
}

// 负向对照：没超限的输入必须**原样返回**。
// 没有它，上面的用例在「无条件截断成固定长度」的错误实现下也照样会红。
func TestClampSummaryInputLeavesShortDialogueUntouched(t *testing.T) {
	const dialogue = "用户: 你好\n助手: 你好，有什么可以帮你？"

	got, ok := clampSummaryInput(dialogue)
	if !ok {
		t.Fatal("短对话不该被判为不可摘要")
	}
	if got != dialogue {
		t.Errorf("未超限的输入被改动了:\n得到: %q\n期望: %q", got, dialogue)
	}
}

// 空/纯空白必须返回 false，让调用方跳过这一轮而不是发空请求。
func TestClampSummaryInputRejectsBlank(t *testing.T) {
	for _, s := range []string{"", "   ", "\n\t\n"} {
		if _, ok := clampSummaryInput(s); ok {
			t.Errorf("输入 %q 不该被判为可摘要", s)
		}
	}
}

// ─── 摘要覆盖边界 ─────────────────────────────────────────────────────────
//
// endIdx = len - summaryTailKeep，末尾 5 条永远以原文留在历史里。
// 这条守护的是「摘要吃掉保留区」这个缺陷：一旦吃掉，最近几轮会同时出现在
// 摘要和明文历史里，模型看到同一段对话两遍。

// 用例数据固定成 20 条消息，已有摘要覆盖到下标 8（startIdx = 9）。
// 期望：endIdx=15，LastMessageID 指向下标 14 的消息，末尾 5 条（下标 15~19）不进摘要。
//
// ⚠️ 这里的「5」写成字面量而**不引用**生产常量 summaryTailKeep：
// 引用它的话，把常量改成 0（保留区消失）时断言的期望值会跟着变，测试照样全绿 ——
// 实测过：注入 `summaryTailKeep = 0` 后本用例毫无反应，而它本该是第一个红的。
func TestSummaryKeepsTailOutOfCoverage(t *testing.T) {
	const total = 20
	const lastSummarizedIdx = 8 // 已有摘要覆盖到下标 8 ⇒ startIdx = 9
	const startIdx = lastSummarizedIdx + 1
	const wantTailKeep = 5 // 字面量：见上方说明

	svc, repo, chatModel := newSummaryTestFixture(t, total, lastSummarizedIdx)

	got, err := svc.SummarizeSession(testCtx(), "sess-1", chatModel)
	if err != nil {
		t.Fatalf("SummarizeSession: %v", err)
	}
	if got == nil {
		t.Fatal("应生成新摘要")
	}

	if repo.summaryRepo.gotDialogue == "" {
		t.Fatal("模型侧没收到任何对话文本")
	}
	// 末尾 5 条的消息 ID 不得出现在送给模型的文本里
	for i := total - wantTailKeep; i < total; i++ {
		if strings.Contains(repo.summaryRepo.gotDialogue, msgID(i)) {
			t.Errorf("第 %d 条（末尾保留区）被写进了摘要输入 —— 最近 %d 条必须留在明文历史里",
				i+1, wantTailKeep)
		}
	}
	// 覆盖到的最后一条应是下标 total-tailKeep-1（开区间）
	wantLast := msgID(total - wantTailKeep - 1)
	if got.LastMessageID == nil || *got.LastMessageID != wantLast {
		t.Errorf("LastMessageID=%s，期望 %s（开区间 off-by-one 检查点）",
			ptrStr(got.LastMessageID), wantLast)
	}
	// CoveredCount 必须是新区间长度，且把已有摘要的覆盖数累加上
	wantCovered := (total - wantTailKeep - startIdx) + lastSummarizedIdx
	if got.CoveredCount != wantCovered {
		t.Errorf("CoveredCount=%d，期望 %d（新覆盖 %d + 已有 %d）",
			got.CoveredCount, wantCovered, total-wantTailKeep-startIdx, lastSummarizedIdx)
	}
}

// 没有足够可摘要区间时不得重算：返回已有摘要，保持覆盖不前进。
// 旧实现在 endIdx <= startIdx 时把 endIdx 抬到 len，等于把保留区吃掉。
//
// ⚠️ 用例数据的构造必须让「强行抬到 len」这条路**真的走得通**，两个条件同时成立：
//  ① endIdx = len - 5 <= startIdx  ⇔ startIdx >= 15
//  ② 抬到 len 后区间仍 ≥ summaryMinSegment(5) ⇔ len - startIdx >= 5 ⇔ startIdx <= 15
// 两者交集只有 startIdx = 15。取别的值会得出「缺陷形态被掩盖 ⇒ 测试全绿」的假结论：
//  - startIdx < 15（如初版的 13）：endIdx 判定为假，根本不进那个分支；
//  - startIdx > 15（如初版的 18）：抬到 len 后区间不足 5 条，被下一道关卡拦住。
func TestSummaryDoesNotAdvanceWithoutRoom(t *testing.T) {
	const total = 20
	const startIdx = total - summaryMinSegment // = 15，同时满足 ①②
	const lastSummarizedIdx = startIdx - 1

	svc, repo, _ := newSummaryTestFixture(t, total, lastSummarizedIdx)

	got, err := svc.SummarizeSession(testCtx(), "sess-1", newStubChatModel("新摘要"))
	if err != nil {
		t.Fatalf("SummarizeSession: %v", err)
	}
	if repo.summaryRepo.gotDialogue != "" {
		t.Error("没有可摘要区间却仍发起了模型请求")
	}
	if repo.summaryRepo.saved != nil {
		t.Errorf("没有可摘要区间却仍写了摘要: %+v", repo.summaryRepo.saved)
	}
	if got == nil || got.Summary != "旧摘要" {
		t.Errorf("应原样返回已有摘要，实际 %+v", got)
	}
	if got.CoveredCount != lastSummarizedIdx {
		t.Errorf("覆盖数被改动：%d，期望保持 %d", got.CoveredCount, lastSummarizedIdx)
	}
}

// ─── 测试脚手架 ───────────────────────────────────────────────────────────

// summaryFixtureRepo 汇总摘要链路要用的两个仓库替身。
type summaryFixtureRepo struct {
	messageRepo *summaryMessageRepo
	summaryRepo *summarySummaryRepo
}

// summaryMessageRepo 按需返回构造好的消息列表。
type summaryMessageRepo struct {
	repository.ChatMessageRepo
	msgs []entity.ChatMessage
}

func (r *summaryMessageRepo) FindBySessionIDForContext(context.Context, string) ([]entity.ChatMessage, error) {
	return r.msgs, nil
}

// summarySummaryRepo 记住已有摘要与实际写回的内容。
type summarySummaryRepo struct {
	repository.SummaryRepo
	existing  *entity.ChatSummary
	gotDialogue string
	saved    *entity.ChatSummary
}

func (r *summarySummaryRepo) GetBySessionID(context.Context, string) (*entity.ChatSummary, error) {
	return r.existing, nil
}

func (r *summarySummaryRepo) Upsert(_ context.Context, s *entity.ChatSummary) error {
	r.saved = s
	return nil
}

// stubChatModel 记录收到的 system prompt（其中含对话文本），并返回固定摘要。
type stubChatModel struct {
	reply    string
	onPrompt func(prompt string)
}

func newStubChatModel(reply string) *stubChatModel { return &stubChatModel{reply: reply} }

func (m *stubChatModel) Generate(_ context.Context, in []*schema.Message, _ ...model.Option) (*schema.Message, error) {
	for _, msg := range in {
		if msg.Role == schema.System {
			if m.onPrompt != nil {
				m.onPrompt(msg.Content)
			}
			break
		}
	}
	return schema.AssistantMessage(m.reply, nil), nil
}

// Stream 是 BaseChatModel 接口的一部分，摘要链路走不到但接口要求实现。
func (m *stubChatModel) Stream(context.Context, []*schema.Message, ...model.Option) (*schema.StreamReader[*schema.Message], error) {
	return nil, errors.New("stubChatModel 不支持流式")
}

// msgID 构造可出现在对话文本里的消息标记（buildDialogueText 会带上内容）。
func msgID(i int) string { return "MSG" + strconv.Itoa(i) }

// newSummaryTestFixture 造一个「共 total 条、已有摘要覆盖到 lastSummarizedIdx」的会话。
// 返回的第二个值是仓库替身，第三个是 chatModel。
func newSummaryTestFixture(t *testing.T, total, lastSummarizedIdx int) (*contextService, *summaryFixtureRepo, *stubChatModel) {
	t.Helper()

	base := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	msgs := make([]entity.ChatMessage, 0, total)
	for i := 0; i < total; i++ {
		role := "user"
		if i%2 == 1 {
			role = "assistant"
		}
		msgs = append(msgs, entity.ChatMessage{
			ID:        msgID(i),
			Role:      role,
			Content:   "内容 " + msgID(i),
			CreatedAt: base.Add(time.Duration(i) * time.Minute),
		})
	}

	lastID := msgID(lastSummarizedIdx)
	repo := &summaryFixtureRepo{
		messageRepo: &summaryMessageRepo{msgs: msgs},
		summaryRepo: &summarySummaryRepo{
			existing: &entity.ChatSummary{
				SessionID: "sess-1", Summary: "旧摘要",
				CoveredCount: lastSummarizedIdx, LastMessageID: &lastID,
			},
		},
	}
	model := newStubChatModel("新摘要")
	model.onPrompt = func(p string) { repo.summaryRepo.gotDialogue = p }
	svc := &contextService{
		messageRepo: repo.messageRepo,
		summaryRepo: repo.summaryRepo,
		memoryRepo:  recallTestMemoryRepo{},
	}
	return svc, repo, model
}

func tokenCountForTest(s string) int { return tokenutil.CountTokens(s, "") }

func ptrStr(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

func testCtx() context.Context { return context.Background() }
