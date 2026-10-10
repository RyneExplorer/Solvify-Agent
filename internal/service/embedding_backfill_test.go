package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"

	"solvify-agent/internal/model/entity"
	"solvify-agent/internal/repository"
)

// ─── 历史消息向量回填 ─────────────────────────────────────────────────────
//
// 这个任务的价值全在「跑得完、跑得对、跑不完时说得清楚」上：
// 存量消息可能上千条，embedding 服务可能没起、维度可能错配、中途可能被 kill。
// 下面每条用例对应其中一种失败形态。

// backfillRepo 是回填要用的两个仓库方法的替身。
// 按 keyset 分页吐数据，并记录实际收到的 afterID —— 「可续跑」只能靠这个验。
type backfillRepo struct {
	repository.ChatMessageRepo

	all      []entity.ChatMessage // 缺向量的全量（按 ID 升序）
	pageSize int                  // 记录每次调用时的 limit，验证分页参数
	batches  []string              // 每次收到的 afterID
	written  []string              // BatchUpdateEmbedding 写入的 ID
	writeErr error
}

func (r *backfillRepo) FindMessagesMissingEmbedding(_ context.Context, afterID string, limit int) ([]entity.ChatMessage, error) {
	r.batches = append(r.batches, afterID)
	r.pageSize = limit
	out := make([]entity.ChatMessage, 0, limit)
	for _, m := range r.all {
		if afterID != "" && m.ID <= afterID {
			continue
		}
		out = append(out, m)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (r *backfillRepo) BatchUpdateEmbedding(_ context.Context, items []repository.MessageEmbeddingUpdate) error {
	if r.writeErr != nil {
		return r.writeErr
	}
	for _, it := range items {
		r.written = append(r.written, it.ID)
	}
	return nil
}

func newBackfillFixture(n int) (*backfillRepo, []entity.ChatMessage) {
	msgs := make([]entity.ChatMessage, 0, n)
	for i := 0; i < n; i++ {
		msgs = append(msgs, entity.ChatMessage{
			// 定长零填充：生产的 id 是 uuid，keyset 用的是 uuid 有序比较。
			// 这里若用变长数字串，字符串比较会把 "m10" 排到 "m9" 之前，
			// 翻页就会跳行/漏行 —— 那是测试替身的缺陷，不是生产代码的。
			ID:      fmt.Sprintf("m%06d", i),
			Role:    "user",
			Content: "内容 " + strconv.Itoa(i),
		})
	}
	return &backfillRepo{all: msgs}, msgs
}

func okEmbed(context.Context, string) ([]float64, error) { return []float64{0.1, 0.2}, nil }

// 基本路径：全部消息都写上了，且分页游标按批推进（证明可续跑，不是从头再来）。
func TestBackfillWritesAllMissingEmbeddings(t *testing.T) {
	repo, msgs := newBackfillFixture(backfillBatchSize*2 + 7)

	res := NewMessageEmbeddingBackfiller(repo, okEmbed).BackfillMessageEmbeddings(context.Background())

	if res.Written != len(msgs) {
		t.Errorf("写入 %d 条，期望 %d 条", res.Written, len(msgs))
	}
	if res.Failed != 0 {
		t.Errorf("失败 %d 条，期望 0", res.Failed)
	}
	if res.CircuitB {
		t.Error("不该熔断")
	}
	if len(repo.written) != len(msgs) {
		t.Errorf("仓库实际收到 %d 条写入，期望 %d", len(repo.written), len(msgs))
	}
	// 游标：首批空串，第二批是首批最后一条 ID —— 这就是「续跑」的凭据
	if len(repo.batches) < 2 {
		t.Fatalf("至少应取两批，实际 %d 批", len(repo.batches))
	}
	if repo.batches[0] != "" {
		t.Errorf("首批游标应为（从头开始），实际 %q", repo.batches[0])
	}
	if repo.batches[1] != msgs[backfillBatchSize-1].ID {
		t.Errorf("第二批游标=%q，期望 %q（上一批最后一条）",
			repo.batches[1], msgs[backfillBatchSize-1].ID)
	}
	if repo.pageSize != backfillBatchSize {
		t.Errorf("分页大小=%d，期望 %d", repo.pageSize, backfillBatchSize)
	}
}

// 续跑幂等：已写过向量的消息不会被再次取到（判据在仓库侧的 embedding IS NULL），
// 这里验证「第一批跑完后重跑，集合里已经没有它 ⇒ 不会重复写」。
func TestBackfillIsResumable(t *testing.T) {
	repo, msgs := newBackfillFixture(backfillBatchSize + 5)

	// 第一轮：把 55 条一次跑完
	first := &partialRepo{full: repo}
	res1 := NewMessageEmbeddingBackfiller(first, okEmbed).BackfillMessageEmbeddings(context.Background())
	if res1.Written != len(msgs) {
		t.Fatalf("第一轮应写入 %d 条，实际 %d", len(msgs), res1.Written)
	}
	if len(first.writtenIDs) != len(msgs) {
		t.Fatalf("第一轮实际写入 %d 条", len(first.writtenIDs))
	}

	// 第二轮：全部已有向量 ⇒ 集合为空，一条都不该再写
	second := &partialRepo{full: repo, skip: len(msgs)}
	res2 := NewMessageEmbeddingBackfiller(second, okEmbed).BackfillMessageEmbeddings(context.Background())
	if res2.Written != 0 {
		t.Errorf("第二轮重复写入 %d 条 —— 回填不是幂等的", res2.Written)
	}

	// 断点续跑：只处理「跳过前 skip 条」之后的剩余数据
	third := &partialRepo{full: repo, skip: backfillBatchSize}
	res3 := NewMessageEmbeddingBackfiller(third, okEmbed).BackfillMessageEmbeddings(context.Background())
	want := len(msgs) - backfillBatchSize
	if res3.Written != want {
		t.Errorf("续跑写入 %d 条，期望 %d 条（跳过已完成的前 %d 条）",
			res3.Written, want, backfillBatchSize)
	}
	// 续跑写的必须正好是「前 50 条之外」的那些（不重不漏）
	wantIDs := make(map[string]bool, want)
	for _, m := range msgs[backfillBatchSize:] {
		wantIDs[m.ID] = true
	}
	if len(third.writtenIDs) != want {
		t.Fatalf("续跑实际写入 %d 条，期望 %d 条", len(third.writtenIDs), want)
	}
	for _, id := range third.writtenIDs {
		if !wantIDs[id] {
			t.Errorf("续跑写了不该写的消息 %s —— 已完成的条目被重复处理", id)
		}
	}
}

// partialRepo 支持「前 skip 条已完成（已有向量）」的续跑场景。
type partialRepo struct {
	repository.ChatMessageRepo
	full       *backfillRepo
	skip       int      // 前 skip 条视为已完成（已有向量），从集合里排除
	writtenIDs []string
}

func (r *partialRepo) FindMessagesMissingEmbedding(_ context.Context, afterID string, limit int) ([]entity.ChatMessage, error) {
	out := make([]entity.ChatMessage, 0, limit)
	for idx, m := range r.full.all {
		// 按下标排除而不是按 ID 猜后缀 —— 「前 skip 条」是集合位置，不是字符串形态
		if idx < r.skip {
			continue
		}
		if afterID != "" && m.ID <= afterID {
			continue
		}
		out = append(out, m)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (r *partialRepo) BatchUpdateEmbedding(_ context.Context, items []repository.MessageEmbeddingUpdate) error {
	for _, it := range items {
		r.writtenIDs = append(r.writtenIDs, it.ID)
	}
	return nil
}

// 熔断：embedding 侧整体不可用（服务没起）时必须提前中止，
// 而不是把上千条全跑成失败、把第一条根因埋掉（红线 19）。
func TestBackfillCircuitBreaksWhenEmbeddingDown(t *testing.T) {
	repo, _ := newBackfillFixture(backfillBatchSize * 3)
	downErr := errors.New("connection refused")
	res := NewMessageEmbeddingBackfiller(repo, func(context.Context, string) ([]float64, error) {
		return nil, downErr
	}).BackfillMessageEmbeddings(context.Background())

	if !res.CircuitB {
		t.Fatal("embedding 全程不可用却没熔断 —— 会把每一批都跑成失败刷屏")
	}
	if res.Written != 0 {
		t.Errorf("熔断时不应有任何写入，实际 %d", res.Written)
	}
	// 三批的数据量不该被全部尝试完：熔断后立即返回
	if res.Scanned > backfillBatchSize*2 {
		t.Errorf("熔断前扫描了 %d 条，超过 2 批 —— 说明还在继续尝试", res.Scanned)
	}
	if len(res.FailedID) == 0 {
		t.Error("失败的消息 ID 必须记下来，否则运维无从知道哪些消息召不到")
	}
}

// 负向对照：只是**个别**消息失败（内容超长之类）时不得熔断 ——
// 部分成功已经证明通道是通的，此时熔断会让一条脏数据把整个回填卡死。
//
// ⚠️ 失败条数必须**攒得够多**：熔断阈值是 backfillCircuitLimit(=50)，而一批也是 50。
// 初版只在两批里各失败 1 条，累加起来远够不到阈值，于是在「部分失败也累加」的
// 错误实现下照样全绿 —— 注入实测确认过（该实现当时 0 条红）。
// 现在每批失败 30 条（< 50 ⇒ 整批未废），三批累计 90 ⇒ 错误实现必然熔断，
// 而正确实现每批都清零，永远不会触发。
func TestBackfillDoesNotTripOnIsolatedFailures(t *testing.T) {
	// 每批 50 条里失败 30 条：批未全废（30 < 50），通道是通的
	const perBatchFailure = 30
	total := backfillBatchSize*3 + 5 // 跑 3 批多，累计失败 90 条，远超阈值 50
	repo, _ := newBackfillFixture(total)

	res := NewMessageEmbeddingBackfiller(repo, func(_ context.Context, content string) ([]float64, error) {
		// 每 5 条里挑 3 条失败（60%），保证任何一批都不是全废
		var n int
		if _, err := fmt.Sscanf(content, "内容 %d", &n); err != nil {
			n = 0
		}
		if n%5 < 3 {
			return nil, errors.New("内容超长")
		}
		return []float64{0.1}, nil
	}).BackfillMessageEmbeddings(context.Background())

	if res.CircuitB {
		t.Fatalf("每批都只有部分消息失败（%d/%d < %d），不该熔断 —— 一条脏数据把整个回填卡死了",
			perBatchFailure, backfillBatchSize, backfillCircuitLimit)
	}
	if res.Scanned != total {
		t.Errorf("扫描 %d 条，期望 %d 条（不该提前中止）", res.Scanned, total)
	}
	if res.Written == 0 {
		t.Error("不该一笔都没写成功")
	}
	if res.Written+res.Failed != res.Scanned {
		t.Errorf("写入 %d + 失败 %d != 扫描 %d", res.Written, res.Failed, res.Scanned)
	}
}

// 空内容不算成功也不算失败：算进「写入」会让账目与实际不符。
func TestBackfillSkipsBlankContentWithoutCounting(t *testing.T) {
	repo := &backfillRepo{all: []entity.ChatMessage{
		{ID: "a", Content: "有内容"},
		{ID: "b", Content: ""},
	}}
	res := NewMessageEmbeddingBackfiller(repo, okEmbed).BackfillMessageEmbeddings(context.Background())

	if res.Written != 1 {
		t.Errorf("写入 %d 条，期望 1（空内容不该计入写入）", res.Written)
	}
	if res.Failed != 0 {
		t.Errorf("空内容不该算失败，实际 %d", res.Failed)
	}
	for _, id := range repo.written {
		if id == "b" {
			t.Error("空内容的消息被写入了 NULL 向量")
		}
	}
}

// 前置条件缺失（embedding 没配）时必须空转，不能 panic。
func TestBackfillNoOpsWhenNotReady(t *testing.T) {
	repo, _ := newBackfillFixture(10)
	res := NewMessageEmbeddingBackfiller(repo, nil).BackfillMessageEmbeddings(context.Background())
	if res.Written != 0 || res.Scanned != 0 {
		t.Errorf("embedFn 为 nil 时应完全空转，实际 %+v", res)
	}

	res = NewMessageEmbeddingBackfiller(nil, okEmbed).BackfillMessageEmbeddings(context.Background())
	if res.Written != 0 {
		t.Errorf("仓库为 nil 时应完全空转，实际 %+v", res)
	}
}

// 批量写入失败时，逐条都要算失败并点名 —— 维度错配是这里最可能的根因。
func TestBackfillReportsWriteFailurePerMessage(t *testing.T) {
	repo, msgs := newBackfillFixture(5)
	repo.writeErr = errors.New("expected 1024 dimensions, not 3")

	res := NewMessageEmbeddingBackfiller(repo, okEmbed).BackfillMessageEmbeddings(context.Background())

	if res.Written != 0 {
		t.Errorf("写入失败却报告成功 %d 条", res.Written)
	}
	if res.Failed != len(msgs) {
		t.Errorf("失败数=%d，期望 %d", res.Failed, len(msgs))
	}
	if len(res.FailedID) != len(msgs) {
		t.Errorf("失败 ID 数=%d，期望 %d —— 运维需要知道哪些消息召不到", len(res.FailedID), len(msgs))
	}
}

